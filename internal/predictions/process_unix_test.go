//go:build !windows

package predictions

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func assertRuntimeProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		if b, readErr := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat"); readErr == nil {
			if end := strings.LastIndex(string(b), ")"); end >= 0 && strings.HasPrefix(string(b[end+1:]), " Z") {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	t.Fatalf("prediction descendant %d survived cancellation", pid)
}
