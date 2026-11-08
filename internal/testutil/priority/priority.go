// Package priority reads process priorities for tests of low-priority process trees: the priority class on
// Windows, the nice value on Unix. Tests compare a process with the test's own (Of(os.Getpid())) and with IsLow.
package priority

import "testing"

// MustOf is Of that fails the test when the priority cannot be read.
func MustOf(t testing.TB, pid int) int {
	t.Helper()
	v, err := Of(pid)
	if err != nil {
		t.Fatalf("priority of process %d: %v", pid, err)
	}
	return v
}
