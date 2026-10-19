//go:build windows

package storage

import (
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/windows"
)

func lockWorkspaceFile(file *os.File) error {
	// Locking byte zero works on an empty sidecar and the kernel releases it
	// when the handle closes or the process terminates. Never delete the file:
	// replacing it could admit two owners holding locks on different files.
	var offset windows.Overlapped
	err := windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &offset)
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return ErrLocked
	}
	if err != nil {
		return fmt.Errorf("lock workspace: %w", err)
	}
	return nil
}

func unlockWorkspaceFile(file *os.File) error {
	var offset windows.Overlapped
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &offset)
}
