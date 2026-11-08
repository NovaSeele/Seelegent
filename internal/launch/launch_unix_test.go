//go:build unix

package launch

import (
	"os/exec"
	"runtime"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// detach starts cmd in a new session, out of the worker's process group, like a daemonising program.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}

// priorityOf returns the nice value of process pid (its main thread on Linux, where the system call returns
// 20 - nice).
func priorityOf(t *testing.T, pid int) int {
	t.Helper()
	prio, err := unix.Getpriority(unix.PRIO_PROCESS, pid)
	if err != nil {
		t.Fatalf("nice of %d: %v", pid, err)
	}
	if runtime.GOOS == "linux" {
		return 20 - prio
	}
	return prio
}

// isLow reports whether a nice value is that of a low-priority tree.
func isLow(nice int) bool { return nice >= 10 }

// atNormalPriority does nothing on Unix: an unprivileged process cannot raise its priority again.
func atNormalPriority(*testing.T) {}
