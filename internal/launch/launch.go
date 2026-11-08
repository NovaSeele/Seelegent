// Package launch runs one worker CLI turn in a detached launcher process so that the turn survives a restart of
// the daemon, and lets the daemon follow it through files.
//
// # Protocol
//
// Every launch owns one directory. The daemon writes the spec (launch.json) and starts `seelegent __launch <dir>`
// detached and without a console (platform.StartDetachedNoConsole). The launcher then:
//
//  1. takes an exclusive lock on launcher.lock and holds it for its whole life. The operating system releases the
//     lock when the launcher exits or crashes, so "lock held" is a liveness test that cannot be fooled by a reused
//     process id (the old version checked pids and sometimes started a second worker on the same conversation);
//  2. starts the worker as the root of a worker process tree (platform.StartWorkerTreeWith): on Windows a
//     kill-on-close Job Object named after launch.json's job name (the start fails if that name already exists);
//     on Linux a process group, with the launcher made a child subreaper so that descendants that double-fork or
//     call setsid re-parent to the launcher and stay in the tree (the launcher reaps them, and waits for the worker
//     itself with wait4 so the worker's exit code stays exact); on macOS and the BSDs a process group plus the
//     launcher's descendants, best effort. The tree gets the limits of Spec.Tree: low priority (the job's
//     priority class on Windows; on Unix the launcher lowers its own nice before the start), and on Windows the
//     job memory and CPU rate caps. Stdout is appended to stream.jsonl and stderr to stderr.txt; then it writes
//     ready.json (with its own start time and the job name, which describe the tree: Handle.Tree);
//  3. waits for the worker to exit, for the cancel file to appear (polled every 200 ms at first, backing off to
//     every second), for the timeout, or for its own context to end (SIGINT/SIGTERM on Unix). On cancel and
//     timeout it stops the whole tree: on Unix SIGTERM to the group and every descendant, SIGKILL after the grace
//     period, repeated until nothing is left; the job is terminated on Windows;
//  4. when the worker has exited, stops whatever the worker left running in its tree, writes exit.json atomically
//     and releases the lock.
//
// Cancel is a file (cancel) rather than a signal or a pipe because it works the same on every system, needs no
// handle or process id, and survives a daemon restart: a daemon that comes back simply creates the file. If the
// launcher itself is killed, Windows still stops the worker tree (the job is kill-on-close) and Linux kills the
// worker with its parent-death signal (SIGKILL); on macOS and the BSDs, which have no such signal, the worker may
// run on until it ends by itself. Either way the daemon reports the launch as lost.
//
// The daemon side is Start (new launch), Attach (an existing launch, after a daemon restart), Handle.Tail (follow
// the stream from a committed byte offset), Handle.Cancel, Handle.Wait and Handle.Tree (the worker's process tree,
// for telling worker connections from the lead's: platform.SocketOwnerInTrees).
package launch

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// File names inside a launch directory.
const (
	SpecFile   = "launch.json"
	StreamFile = "stream.jsonl"
	StderrFile = "stderr.txt"
	ReadyFile  = "ready.json"
	ExitFile   = "exit.json"
	CancelFile = "cancel"
	LockFile   = "launcher.lock"
)

// filePerm is the permission of every file of a launch: prompts and worker output are private.
const filePerm = 0o600

// dirPerm is the permission of a launch directory.
const dirPerm = 0o700

// Spec describes the worker process. It is stored in launch.json; the environment is not part of it (it can hold
// API keys) and travels in the launcher's own environment instead.
type Spec struct {
	// Argv is the worker command and its arguments. Argv[0] should be an absolute path.
	Argv []string `json:"argv"`
	// Dir is the worker's working directory.
	Dir string `json:"dir"`
	// StdinPath, when set, names a file whose content becomes the worker's standard input (the prompt for CLIs that
	// read it from stdin). Without it the worker's stdin is empty.
	StdinPath string `json:"stdinPath,omitempty"`
	// Timeout stops the worker tree after this long; zero means no limit.
	Timeout time.Duration `json:"timeout,omitempty"`
	// KillGrace is the time between SIGTERM and SIGKILL on Unix; zero means platform.DefaultKillGrace.
	KillGrace time.Duration `json:"killGrace,omitempty"`
	// Tree are the resource limits of the worker tree (priority, memory and CPU caps; settings workerPriority,
	// workerMemoryLimitMB, workerCPURatePercent). The zero value, and a launch.json written before M19, start the
	// tree without limits.
	Tree platform.TreeOptions `json:"tree,omitzero"`
}

// specFile is the content of launch.json: the spec and where its files are.
type specFile struct {
	Spec
	// Stream receives the worker's stdout, appended.
	Stream string `json:"stream"`
	// Stderr receives the worker's stderr and the launcher's own messages, appended.
	Stderr string `json:"stderr"`
	// Exit receives the exit record.
	Exit string `json:"exit"`
	// Cancel is the file whose appearance asks the launcher to stop the worker tree.
	Cancel string `json:"cancel"`
	// Job is the name of the worker tree (platform.NewTreeName: the Windows Job Object `Local\Seelegent-<hex>`;
	// empty on Unix). The daemon chooses it, so the tree can be found before ready.json exists.
	Job string `json:"job,omitempty"`
}

// Reason says why a launch ended.
type Reason string

// Reasons a launch ends.
const (
	// ReasonExited: the worker exited by itself; Exit.Code is its exit code.
	ReasonExited Reason = "exited"
	// ReasonCanceled: the cancel file appeared (or the launcher was told to stop) and the tree was stopped.
	ReasonCanceled Reason = "canceled"
	// ReasonTimeout: Spec.Timeout elapsed and the tree was stopped.
	ReasonTimeout Reason = "timeout"
	// ReasonStartFailed: the worker could not be started; Exit.Error says why.
	ReasonStartFailed Reason = "start_failed"
	// ReasonLost: the launcher is gone without writing an exit record (it was killed, or the machine rebooted).
	ReasonLost Reason = "lost"
)

// Exit is the exit record (exit.json).
type Exit struct {
	// Reason says why the launch ended.
	Reason Reason `json:"reason"`
	// Code is the worker's exit code, or -1 when there is none.
	Code int `json:"code"`
	// Error describes a failure of the launcher (not of the worker).
	Error string `json:"error,omitempty"`
	// StartedAt is when the worker started (zero if it never did).
	StartedAt time.Time `json:"startedAt,omitzero"`
	// MemoryLimitHit is true when the worker tree reached Spec.Tree.MemoryLimitMB (Windows only: the job reported
	// a refused allocation, or the worker failed after the job's peak memory reached 90% of the cap; see
	// platform.Tree.MemoryLimitHit), so the turn fails saying so.
	MemoryLimitHit bool `json:"memoryLimitHit,omitempty"`
	// EndedAt is when the launch ended.
	EndedAt time.Time `json:"endedAt"`
}

// Ready is the record the launcher writes once the worker runs (ready.json).
type Ready struct {
	// LauncherPID is the process id of the launcher.
	LauncherPID int `json:"launcherPid"`
	// LauncherStart is the launcher's start time (platform.ProcessStartTime), so that a reused LauncherPID is
	// never taken for the launcher.
	LauncherStart uint64 `json:"launcherStart,omitempty"`
	// Job is the name of the worker tree the launcher created (see specFile.Job).
	Job string `json:"job,omitempty"`
	// WorkerPID is the process id of the worker (the root of its tree).
	WorkerPID int `json:"workerPid"`
	// StartedAt is when the worker started.
	StartedAt time.Time `json:"startedAt"`
}

// Files are the paths of one launch directory.
type Files struct {
	Dir, Spec, Stream, Stderr, Ready, Exit, Cancel, Lock string
}

// FilesIn returns the paths of the launch directory dir.
func FilesIn(dir string) Files {
	return Files{
		Dir:    dir,
		Spec:   filepath.Join(dir, SpecFile),
		Stream: filepath.Join(dir, StreamFile),
		Stderr: filepath.Join(dir, StderrFile),
		Ready:  filepath.Join(dir, ReadyFile),
		Exit:   filepath.Join(dir, ExitFile),
		Cancel: filepath.Join(dir, CancelFile),
		Lock:   filepath.Join(dir, LockFile),
	}
}

// writeJSON writes v to path atomically.
func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.Base(path), err)
	}
	return fsutil.WriteFileAtomic(path, append(data, '\n'), filePerm)
}

// readJSON reads path into v. A missing file returns an error matching fs.ErrNotExist.
func readJSON(path string, v any) error {
	data, err := fsutil.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	return nil
}

// readExit returns the exit record if it exists.
func readExit(path string) (Exit, bool, error) {
	var ex Exit
	err := readJSON(path, &ex)
	if errors.Is(err, os.ErrNotExist) {
		return Exit{}, false, nil
	}
	if err != nil {
		return Exit{}, false, err
	}
	return ex, true, nil
}

// exists reports whether path exists.
func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
