//go:build unix

package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
)

// openTerminal writes an sh script and asks the desktop to run it in a terminal window. The window is not a child
// we can wait for (terminal emulators hand windows to a server process), so Terminal.Wait follows the exit-code
// file the script writes.
func openTerminal(ctx context.Context, spec TerminalSpec, base, exitFile string) (*Terminal, error) {
	scriptPath := base + ".sh"
	if err := os.WriteFile(scriptPath, []byte(unixScript(spec, exitFile)), 0o700); err != nil { //nolint:gosec // the script must be executable by its owner
		return nil, fmt.Errorf("open terminal: %w", err)
	}
	argv, reason := terminalCommand(os.Getenv, exec.LookPath, scriptPath)
	if argv == nil {
		return nil, &NoTerminalError{Command: "sh " + shQuote(scriptPath), Reason: reason}
	}
	cmd := exec.CommandContext(context.WithoutCancel(ctx), argv[0], argv[1:]...) //nolint:gosec // argv built from a fixed list of terminals
	cmd.Env = spec.Env
	if err := cmd.Start(); err != nil {
		return nil, &NoTerminalError{Command: "sh " + shQuote(scriptPath), Reason: err.Error()}
	}
	go func() { _ = cmd.Wait() }() // reap the launcher process; the window is tracked through exitFile
	return &Terminal{exitFile: exitFile, pidFile: pidFileFor(exitFile)}, nil
}
