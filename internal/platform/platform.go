// Package platform isolates what differs between operating systems: starting processes without a console window,
// controlling a whole process tree (a Windows Job Object; on Unix a process group plus the descendants by parent
// id, with a child subreaper on Linux), describing a worker's tree so that another process can list it and tell
// whether a TCP connection came from it (WorkerTree, SocketOwnerInTrees), checking whether a process is alive,
// listing processes with their command lines (ListProcesses, Windows only), linking files into a profile, and
// opening a URL or a terminal window for an interactive sign-in.
//
// Worker trees are a bar, not a sandbox. A worker runs as the user and can ask a service of that user to start a
// process for it, outside its tree: on Windows through WMI (Win32_Process.Create), the Task Scheduler or an
// out-of-process COM server; on Linux through `systemd-run --user`, at or cron; on macOS through launchd. Such a
// process escapes cancel and timeout, and SocketOwnerInTrees sees it as the lead.
//
// Everything else in Seelegent stays free of build tags; it calls the functions here.
package platform

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"time"
)

// DefaultKillGrace is how long Tree.Kill waits on Unix between asking the tree to stop (SIGTERM) and forcing it
// (SIGKILL). Windows has no polite stop for console programs without a console of our own, so it kills at once.
const DefaultKillGrace = 3 * time.Second

// ErrNotStarted is returned by Tree methods when the tree was never started.
var ErrNotStarted = errors.New("process tree not started")

// HideWindow prepares cmd so that starting it never shows a console window on Windows: the process shares the
// caller's console when the caller has one (hidden for the daemon, started with StartDetached), and otherwise gets
// a hidden console of its own; its own children inherit that console, so they do not flash windows either. Sharing
// starts no console host per command. It does nothing elsewhere. Call it before cmd.Start for every helper command
// the daemon runs (quota readers, model lists, git, ...).
func HideWindow(cmd *exec.Cmd) { hideWindow(cmd) }

// SetCmdLine sets the raw command line of cmd on Windows, for programs such as cmd.exe that parse their own; it
// does nothing elsewhere.
func SetCmdLine(cmd *exec.Cmd, line string) { setCmdLine(cmd, line) }

// StartDetached starts a process that must outlive the current one, such as the worker launcher that keeps a turn
// running across a daemon restart. On Windows the process gets its own hidden console and process group and breaks
// away from the job of the caller when that job allows it (a host that put the daemon in a kill-on-close job would
// otherwise kill the launcher with the daemon). On Unix it starts a new session.
//
// newCmd must return a fresh, unstarted command each time it is called: if breaking away from the job is refused,
// the start is retried without it on a second command. The started command is returned.
func StartDetached(newCmd func() *exec.Cmd) (*exec.Cmd, error) {
	return startDetached(newCmd, true)
}

// StartDetachedNoConsole is StartDetached for a process that starts console programs only through HideWindow,
// StartTree or StartWorkerTree, such as the worker launcher: on Windows it gets no console at all
// (DETACHED_PROCESS), which saves a console host (conhost.exe) for as long as it runs. A console program it started
// without HideWindow would open a visible console window. Elsewhere it is StartDetached.
func StartDetachedNoConsole(newCmd func() *exec.Cmd) (*exec.Cmd, error) {
	return startDetached(newCmd, false)
}

// ProcessAlive reports whether a process with the given id exists. It cannot tell a reused id from the original
// process; callers that must be sure use a lock held by the process (see the launch package).
func ProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return processAlive(pid)
}

// KillTree forcefully stops the process pid and every process it started that still belongs to it (taskkill /T on
// Windows, the process group on Unix). Use Tree.Kill for trees started with StartTree; KillTree is for processes
// started some other way, such as a sign-in window.
func KillTree(ctx context.Context, pid int) error {
	if pid <= 0 {
		return fmt.Errorf("kill process tree: invalid pid %d", pid)
	}
	return killTree(ctx, pid)
}

// OpenURL opens an http or https URL in the user's default browser.
func OpenURL(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("open url: %w", err)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("open url %q: only http and https URLs are opened", rawURL)
	}
	cmd := openURLCommand(ctx, u.String())
	HideWindow(cmd)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("open url: %w", err)
	}
	return nil
}
