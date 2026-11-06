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
