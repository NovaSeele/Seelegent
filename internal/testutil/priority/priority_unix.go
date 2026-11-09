//go:build unix

package priority

import (
	"runtime"
	"testing"

	"golang.org/x/sys/unix"
)

// lowNice is the nice value of a low-priority tree (platform's lowNice).
const lowNice = 10

// Of returns the nice value of process pid (of its main thread on Linux, where nice is per thread).
func Of(pid int) (int, error) {
	prio, err := unix.Getpriority(unix.PRIO_PROCESS, pid)
	if err != nil {
		return 0, err
	}
	if runtime.GOOS == "linux" {
		return 20 - prio, nil // the Linux system call returns 20 - nice
	}
	return prio, nil
}

// IsLow reports whether a nice value is that of a low-priority tree: nice adds to the caller's, so at least 10.
func IsLow(nice int) bool { return nice >= lowNice }

// AtNormal does nothing on Unix: an unprivileged process cannot raise its priority again, so a test that already
// runs at nice 10 or more skips its priority checks.
func AtNormal(testing.TB) {}
