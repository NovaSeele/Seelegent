package gate

import (
	"testing"

	"golang.org/x/sys/windows"
)

// ownPriority returns this process's priority class.
func ownPriority() int {
	class, err := windows.GetPriorityClass(windows.CurrentProcess())
	if err != nil {
		return -1
	}
	return int(class)
}

// isLow reports whether a priority class is the low one of a tree.
func isLow(class int) bool { return class == windows.BELOW_NORMAL_PRIORITY_CLASS }

// atNormalPriority runs the test at normal priority for its duration, so that a gentle `go test` at below normal
// can still tell a lowered tree from an inherited priority.
func atNormalPriority(t *testing.T) {
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
