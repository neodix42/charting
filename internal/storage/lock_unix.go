//go:build !windows

package storage

import (
	"errors"
	"fmt"
	"os"
	"syscall"
)

func lockWorkspaceFile(file *os.File) error {
	err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
		return ErrLocked
	}
	if err != nil {
		return fmt.Errorf("lock workspace: %w", err)
	}
	return nil
}

func unlockWorkspaceFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
}
