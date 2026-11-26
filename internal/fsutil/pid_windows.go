//go:build windows

package fsutil

import "os"

// processExists: on Windows FindProcess opens the process and fails when it is gone.
func processExists(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	_ = p.Release()
	return true
}
