//go:build unix

package launch

import (
	"os/exec"
	"syscall"
)

// detach starts cmd in a new session, out of the worker's process group, like a daemonising program.
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
