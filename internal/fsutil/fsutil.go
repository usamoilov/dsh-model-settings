// Package fsutil writes profile files the way DSH itself does
// (`@deepseek-ai/dsh-atomic-write`), so this process, DSH's config editor and
// `dsh plugin` never overwrite each other:
//
//   - WriteFileAtomic writes a random-suffix sibling with exclusive create and
//     renames it over the target;
//   - WithFileLock serializes writers of one file through a `<file>.lock`
//     sibling that records the holder's PID; a lock whose holder process is
//     gone is taken over.
package fsutil

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	lockRetryInitial = 20 * time.Millisecond
	lockRetryMax     = 200 * time.Millisecond
	// DefaultLockWait is how long a contender waits for the lock (as in DSH).
	DefaultLockWait = 2 * time.Second
)

// WriteFileAtomic replaces filename with content in one step: readers see the
// old or the new complete file. Parent directories are created.
func WriteFileAtomic(filename string, content []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(filename), 0o755); err != nil {
		return err
	}
	suffix := make([]byte, 6)
	if _, err := rand.Read(suffix); err != nil {
		return err
	}
	temp := fmt.Sprintf("%s.%s.tmp", filename, hex.EncodeToString(suffix))
	f, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	_, err = f.Write(content)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = renameRetry(temp, filename)
	}
	if err != nil {
		_ = os.Remove(temp)
	}
	return err
}

// renameRetry retries the transient errors Windows reports while another
// process has the target open.
func renameRetry(from, to string) error {
	delay := 20 * time.Millisecond
	for retries := 0; ; retries++ {
		err := os.Rename(from, to)
		if err == nil || runtime.GOOS != "windows" || retries >= 8 || !errors.Is(err, os.ErrPermission) {
			return err
		}
		time.Sleep(delay)
		delay = min(delay*2, 200*time.Millisecond)
	}
}

var pidRecord = regexp.MustCompile(`^\d+\n$`)

// holderExited: the `<pid>\n` record names a process that no longer exists.
func holderExited(record string) bool {
	if !pidRecord.MatchString(record) {
		return false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(record))
	if err != nil || pid == 0 || pid > 2147483647 || pid == os.Getpid() {
		return false
	}
	return !processExists(pid)
}

func readRecord(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return string(data), true
}

// takeOverExitedLock removes the lock when its holder exited. Contenders that
// read the same record serialize on a claim file named after it.
func takeOverExitedLock(lockPath string) bool {
	record, ok := readRecord(lockPath)
	if !ok || !holderExited(record) {
		return false
	}
	sum := sha256.Sum256([]byte(record))
	claim := lockPath + ".takeover-" + hex.EncodeToString(sum[:])[:16]
	f, err := os.OpenFile(claim, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return false
	}
	_, _ = fmt.Fprintf(f, "%d\n", os.Getpid())
	_ = f.Close()
	defer os.Remove(claim)
	if again, ok := readRecord(lockPath); !ok || again != record || !holderExited(record) {
		return false
	}
	return os.Remove(lockPath) == nil
}

func isContention(err error, lockPath string) bool {
	if errors.Is(err, os.ErrExist) {
		return true
	}
	if !errors.Is(err, os.ErrPermission) {
		return false
	}
	_, statErr := os.Lstat(lockPath)
	return statErr == nil
}

// WithFileLock holds the cross-process writer lock of filename (`<filename>.lock`)
// around op. wait <= 0 means DefaultLockWait.
func WithFileLock(ctx context.Context, filename string, wait time.Duration, op func() error) error {
	if wait <= 0 {
		wait = DefaultLockWait
	}
	lockPath := filename + ".lock"
	deadline := time.Now().Add(wait)
	delay := lockRetryInitial
	retriedPermission := false
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		f, err := os.OpenFile(lockPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			_, werr := fmt.Fprintf(f, "%d\n", os.Getpid())
			_ = f.Close()
			if werr != nil {
				_ = os.Remove(lockPath)
				return werr
			}
			break
		}
		if !isContention(err, lockPath) {
			if runtime.GOOS != "windows" || !errors.Is(err, os.ErrPermission) || retriedPermission {
				return err
			}
			retriedPermission = true
		} else if takeOverExitedLock(lockPath) {
			continue
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("atomic-write: timed out waiting for the writer lock at %s", lockPath)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay = min(delay*2, lockRetryMax)
	}
	defer os.Remove(lockPath)
	if err := ctx.Err(); err != nil {
		return err
	}
	return op()
}
