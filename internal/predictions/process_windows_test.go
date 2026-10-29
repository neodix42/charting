//go:build windows

package predictions

import (
	"errors"
	"syscall"
	"testing"
	"time"
)

func assertRuntimeProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		handle, err := syscall.OpenProcess(syscall.SYNCHRONIZE, false, uint32(pid))
		if errors.Is(err, syscall.Errno(87)) {
			return
		}
		if err == nil {
			status, waitErr := syscall.WaitForSingleObject(handle, 0)
			syscall.CloseHandle(handle)
			if waitErr == nil && status == syscall.WAIT_OBJECT_0 {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("prediction descendant %d survived cancellation", pid)
}
