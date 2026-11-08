//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// stillActive is the exit code GetExitCodeProcess reports for a running process (STILL_ACTIVE).
const stillActive = 259

// setCmdLine sets the raw command line Windows passes to the program (cmd.exe parses its own).
func setCmdLine(cmd *exec.Cmd, line string) { sysProcAttr(cmd).CmdLine = line }

// hideWindow: a console program inherits the console of this process when there is one (the daemon's and the
// launcher's are hidden; a foreground CLI's is the user's own terminal), so no window opens and no console host
// (conhost.exe, about 8 MB) is started for it. Only a caller without a console gives it a hidden console of its own
// (CREATE_NO_WINDOW): without one, Windows would open a new, visible console window for it.
func hideWindow(cmd *exec.Cmd) {
	attr := sysProcAttr(cmd)
	attr.HideWindow = true
	if !hasConsole() {
		attr.CreationFlags |= windows.CREATE_NO_WINDOW
	}
}

// hasConsole reports whether this process is attached to a console, hidden or not.
func hasConsole() bool {
	_, err := windows.GetConsoleCP()
	return err == nil
}

// sysProcAttr returns cmd.SysProcAttr, creating it when needed.
func sysProcAttr(cmd *exec.Cmd) *syscall.SysProcAttr {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	return cmd.SysProcAttr
}

// startDetached starts newCmd's command in a new process group, broken away from the caller's job when the job
// allows it. With console it gets a hidden console of its own (CREATE_NO_WINDOW), which the console programs it
// starts inherit; without, it gets no console at all (DETACHED_PROCESS), so no console host runs for it.
func startDetached(newCmd func() *exec.Cmd, console bool) (*exec.Cmd, error) {
	flags := uint32(windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW)
	if !console {
		flags = windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS
	}
	cmd := newCmd()
	attr := sysProcAttr(cmd)
	attr.HideWindow = true
	attr.CreationFlags |= flags | windows.CREATE_BREAKAWAY_FROM_JOB
	err := cmd.Start()
	if err == nil {
		return cmd, nil
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return nil, err
	}
	// The caller's job does not allow breakaway. Start inside it: the process still gets its own group (and
	// console, or none).
	cmd = newCmd()
	attr = sysProcAttr(cmd)
	attr.HideWindow = true
	attr.CreationFlags |= flags
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func processAlive(pid int) bool {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid)) //nolint:gosec // pid > 0 checked by the caller
	if err != nil {
		// Access denied means the process exists but belongs to someone else.
		return errors.Is(err, windows.ERROR_ACCESS_DENIED)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return true
	}
	return code == stillActive
}

func killTree(ctx context.Context, pid int) error {
	cmd := exec.CommandContext(ctx, "taskkill", "/PID", strconv.Itoa(pid), "/T", "/F") //nolint:gosec // fixed program, numeric pid
	HideWindow(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		if !processAlive(pid) {
			return nil
		}
		return fmt.Errorf("kill process tree %d: %w: %s", pid, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// quoted wraps s in double quotes for a raw Windows command line; callers make sure s holds no quote.
func quoted(s string) string { return `"` + s + `"` }

func openURLCommand(ctx context.Context, u string) *exec.Cmd {
	return exec.CommandContext(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", u)
}
