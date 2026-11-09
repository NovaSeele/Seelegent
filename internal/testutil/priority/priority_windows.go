package priority

import (
	"fmt"
	"testing"

	"golang.org/x/sys/windows"
)

// Of returns the priority class of process pid.
func Of(pid int) (int, error) {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // a process id
	if err != nil {
		return 0, fmt.Errorf("open process: %w", err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	class, err := windows.GetPriorityClass(h)
	if err != nil {
		return 0, fmt.Errorf("priority class: %w", err)
	}
	return int(class), nil
}

// IsLow reports whether a priority class is the one of a low-priority tree (BELOW_NORMAL).
func IsLow(class int) bool { return class == windows.BELOW_NORMAL_PRIORITY_CLASS }

// AtNormal runs the test at normal priority for its duration: a test started below normal (a gentle `go test` on
// a busy machine) could not tell a lowered tree from an inherited priority.
func AtNormal(t testing.TB) {
	t.Helper()
	self := windows.CurrentProcess()
	class, err := windows.GetPriorityClass(self)
	if err != nil || class == windows.NORMAL_PRIORITY_CLASS {
		return
	}
	if err := windows.SetPriorityClass(self, windows.NORMAL_PRIORITY_CLASS); err != nil {
		t.Logf("cannot run at normal priority: %v", err)
		return
	}
	t.Cleanup(func() { _ = windows.SetPriorityClass(self, class) })
}
