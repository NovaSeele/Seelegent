//go:build unix

package gate

import (
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

// ownPriority returns this process's nice value (the Linux system call returns 20 - nice).
func ownPriority() int {
	prio, err := unix.Getpriority(unix.PRIO_PROCESS, 0)
	if err != nil {
		return -100
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
