//go:build !windows

package fsutil

import (
	"errors"
	"syscall"
)

// processExists probes a PID with signal 0: only ESRCH proves it gone.
func processExists(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || !errors.Is(err, syscall.ESRCH)
}
