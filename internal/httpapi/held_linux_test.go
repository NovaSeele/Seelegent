package httpapi_test

import (
	"os"
	"path/filepath"
	"testing"
)

// fileHeld reports whether this process (the daemon runs in the test process) has path open, from /proc/self/fd.
func fileHeld(t *testing.T, path string) (held, known bool) {
	t.Helper()
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	fds, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return false, false
	}
	for _, fd := range fds {
		if target, err := os.Readlink(filepath.Join("/proc/self/fd", fd.Name())); err == nil && target == want {
			return true, true
		}
	}
	return false, true
}
