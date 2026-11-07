//go:build !windows

package gate

import "os/exec"

// shellCommand runs cmdline through /bin/sh -c.
func shellCommand(cmdline string) *exec.Cmd {
	return exec.Command("/bin/sh", "-c", cmdline) //nolint:noctx // Exec kills the whole process tree when ctx ends
}
