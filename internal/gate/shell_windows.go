//go:build windows

package gate

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// shellCommand runs cmdline through cmd.exe: /d skips AutoRun, /s /c keeps the command line exactly as written
// between the outer quotes. The command line is set raw, so Go adds no quoting of its own.
func shellCommand(cmdline string) *exec.Cmd {
	comspec := os.Getenv("ComSpec")
	if comspec == "" || !filepath.IsAbs(comspec) {
		comspec = filepath.Join(os.Getenv("SystemRoot"), "System32", "cmd.exe")
	}
	cmd := exec.Command(comspec) //nolint:gosec,noctx // Exec kills the tree on ctx end; the gate command is the lead's or the repository's, run by design
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `"` + comspec + `" /d /s /c "` + cmdline + `"`}
	return cmd
}
