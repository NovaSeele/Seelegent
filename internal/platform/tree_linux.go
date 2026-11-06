package platform

import (
	"os/exec"
	"syscall"
)

// parentDeathSignal makes Linux send SIGKILL to the root of the tree when the process that started it dies, so a
// worker does not outlive a launcher that crashed or was killed.
//
// Limits: the signal reaches the root process only (its own children get no signal; a CLI that dies takes its
// usual cleanup path, and Kill/Close still cover the whole group while the owner lives), and Linux ties it to the
// OS thread that started the process, not to the whole process. The Go runtime keeps its threads unless a
// goroutine exits while locked to its thread (runtime.LockOSThread), so StartTree must not be called from such a
// goroutine.
func parentDeathSignal(cmd *exec.Cmd) {
	sysProcAttr(cmd).Pdeathsig = syscall.SIGKILL
}
