package launch

import (
	"os/exec"
	"syscall"
	"testing"

	"golang.org/x/sys/windows"
)

// detach starts cmd without a console and in its own process group, like a daemonising program. It stays in the
// worker's job: the job does not allow breakaway.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS}
}

// checkAdopted has nothing to check on Windows: membership comes from the job, not from parent ids.
func checkAdopted(*testing.T, int, int) {}

// priorityOf returns the priority class of process pid.
func priorityOf(t *testing.T, pid int) int {
	t.Helper()
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		t.Fatalf("open process %d: %v", pid, err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	class, err := windows.GetPriorityClass(h)
	if err != nil {
		t.Fatalf("priority class of %d: %v", pid, err)
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
