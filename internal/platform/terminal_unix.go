//go:build unix

package platform

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Launcher check timings.
const (
	// launcherCheck bounds how long openTerminal waits to see whether the terminal launcher failed. It returns
	// earlier when the launcher exits or the script has started.
	launcherCheck = 3 * time.Second
	// launcherPoll is how often the script's pid file is looked for meanwhile.
	launcherPoll = 50 * time.Millisecond
	// launcherStderr caps the launcher's error output kept for the reason.
	launcherStderr = 1024
)

// openTerminal writes an sh script and asks the desktop to run it in a terminal window. The window is not a child
// we can wait for (terminal emulators hand windows to a server process), so Terminal.Wait follows the exit-code
// file the script writes.
//
// A launcher that fails at once (osascript refused the Automation permission, a broken x-terminal-emulator or
// $TERMINAL) is reported as a *NoTerminalError instead of a window that never opens. openTerminal waits for that
// at most launcherCheck, and returns as soon as the launcher exits successfully or the script has started.
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
	stderr := &cappedWriter{limit: launcherStderr}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, &NoTerminalError{Command: "sh " + shQuote(scriptPath), Reason: err.Error()}
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }() // also reaps the launcher; the window is tracked through exitFile
	term := &Terminal{exitFile: exitFile, pidFile: pidFileFor(exitFile)}
	if err := checkLauncher(ctx, exited, term.pidFile, launcherCheck); err != nil {
		why := fmt.Sprintf("%s failed: %v", filepath.Base(argv[0]), err)
		if msg := strings.TrimSpace(string(stderr.buf)); msg != "" { // the launcher has exited: no more writes
			why += ": " + msg
		}
		return nil, &NoTerminalError{Command: "sh " + shQuote(scriptPath), Reason: why}
	}
	return term, nil
}

// checkLauncher waits until the launcher exited, the script wrote its pid file, limit passed or ctx ended. It
// returns the launcher's error only when the launcher failed before the script started.
func checkLauncher(ctx context.Context, exited <-chan error, pidFile string, limit time.Duration) error {
	deadline := time.NewTimer(limit)
	defer deadline.Stop()
	tick := time.NewTicker(launcherPoll)
	defer tick.Stop()
	started := func() bool {
		_, err := os.Stat(pidFile)
		return err == nil
	}
	for {
		select {
		case err := <-exited:
			if err == nil || started() {
				return nil
			}
			return err
		case <-tick.C:
			if started() {
				return nil
			}
		case <-deadline.C:
			return nil // the launcher stays alive (it runs the window itself): treat it as opened
		case <-ctx.Done():
			return nil
		}
	}
}

// cappedWriter keeps the first limit bytes written to it and drops the rest.
type cappedWriter struct {
	buf   []byte
	limit int
}

// Write implements io.Writer; it never fails, so the launcher is not disturbed by a full buffer.
func (w *cappedWriter) Write(p []byte) (int, error) {
	if room := w.limit - len(w.buf); room > 0 {
		w.buf = append(w.buf, p[:min(len(p), room)]...)
	}
	return len(p), nil
}
