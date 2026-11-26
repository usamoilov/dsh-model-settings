package fsutil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAtomicWrite(t *testing.T) {
	file := filepath.Join(t.TempDir(), "sub", "f.yml")
	if err := WriteFileAtomic(file, []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileAtomic(file, []byte("b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file)
	entries, _ := os.ReadDir(filepath.Dir(file))
	if string(data) != "b\n" || len(entries) != 1 {
		t.Fatal(string(data), entries)
	}
}

func TestLock(t *testing.T) {
	file := filepath.Join(t.TempDir(), "package.json")
	var mu sync.Mutex
	inside, peak := 0, 0
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := WithFileLock(context.Background(), file, 5*time.Second, func() error {
				mu.Lock()
				inside++
				peak = max(peak, inside)
				mu.Unlock()
				time.Sleep(20 * time.Millisecond)
				mu.Lock()
				inside--
				mu.Unlock()
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if peak != 1 {
		t.Fatal("writers overlapped:", peak)
	}
	if _, err := os.Stat(file + ".lock"); !os.IsNotExist(err) {
		t.Fatal("lock left behind")
	}
	// a lock of a process that is gone is taken over; a live holder makes us wait and time out
	if err := os.WriteFile(file+".lock", []byte("2147483000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := WithFileLock(context.Background(), file, time.Second, func() error { return nil }); err != nil {
		t.Fatal("dead holder:", err)
	}
	if err := os.WriteFile(file+".lock", []byte("not a pid"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := WithFileLock(context.Background(), file, 100*time.Millisecond, func() error { return nil })
	if err == nil || !strings.Contains(err.Error(), "timed out waiting for the writer lock") {
		t.Fatal(err)
	}
}
