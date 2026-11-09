package platform

import (
	"errors"
	"os/exec"
	"sync"
	"time"
)

// ErrTreeNameTaken is returned by StartWorkerTree when a process tree with the requested name already exists
// (Windows: a Job Object of that name). Nobody can take over a worker tree by creating its name first.
var ErrTreeNameTaken = errors.New("a process tree with this name already exists")

// Tree is a process started with StartTree or StartWorkerTree together with every process it starts. Stopping the
// tree stops all of them, however deep:
//
//   - Windows: they all belong to a Job Object that the Tree owns, with kill-on-close (the tree also dies if the
//     owner of the Tree dies) and without breakaway, so no descendant can leave it.
//   - Unix: the root leads a new process group; stopping the tree signals the group and every descendant found by
//     walking parent ids (each checked against its start time, so a reused process id is never signaled). On
//     Linux the root also gets SIGKILL when the owner of the Tree dies; macOS and the BSDs have no such signal.
//
// A Unix descendant that moves itself into another session or process group and whose parent then exits (a
// daemonising program) is re-parented away from the tree. StartWorkerTree closes that gap on Linux by making the
// owner a child subreaper, so such orphans re-parent to the owner and stay in the tree; on macOS and the BSDs it
// stays best effort (WorkerTreesExact reports which).
type Tree struct {
	cmd *exec.Cmd
	pid int
	os  treeOS

	mu     sync.Mutex
	closed bool
}

// StartTree starts cmd hidden (no console window on Windows; a hidden console of its own, see HideWindow) as the
// root of a new process tree. cmd must not have been started. The caller must call Wait to reap the root process
// and Close when done with the tree. Seelegent's own fixed helpers share the daemon's console instead through
// StartTreeWith and TreeOptions.ShareConsole.
func StartTree(cmd *exec.Cmd) (*Tree, error) {
	return startTree(cmd, false, "", TreeOptions{})
}

// StartWorkerTree is StartTree for a process whose only job is to own this one tree, such as the worker launcher.
// The whole tree is what other processes see through WorkerTree and SocketOwnerInTrees:
//
//   - Windows: the Job Object gets name (from NewTreeName) so that other processes of the user can open it, also
//     after a restart of the daemon; if that name already exists the start fails with ErrTreeNameTaken. An empty
//     name gives an anonymous job.
//   - Linux: the calling process becomes a child subreaper (PR_SET_CHILD_SUBREAPER), so descendants that
//     double-fork or call setsid re-parent to it instead of init, and Kill and Close reach them. The Tree then
//     reaps every child of the process itself (the root and the adopted orphans), so the process must not start
//     and wait for other children, and cmd must have no context and only *os.File (or nil) standard streams:
//     exec.Cmd.Wait is never called for it.
//   - macOS and the BSDs: no subreaper; Kill walks the descendants of the calling process (best effort).
func StartWorkerTree(cmd *exec.Cmd, name string) (*Tree, error) {
	return startTree(cmd, true, name, TreeOptions{})
}

// Priority is the process priority of a tree (TreeOptions.Priority).
type Priority string

// Priorities. The zero value "" is PriorityNormal.
const (
	// PriorityLow: BELOW_NORMAL_PRIORITY_CLASS for every process of the Job Object on Windows (the job's priority
	// class limit, so descendants cannot raise it); nice 10 for the whole tree on Unix (StartTreeWith).
	PriorityLow Priority = "low"
	// PriorityNormal: the priority the tree would get anyway (the caller's).
	PriorityNormal Priority = "normal"
)

// TreeOptions are the resource limits of a tree started with StartTreeWith or StartWorkerTreeWith. The zero value
// starts the tree as StartTree and StartWorkerTree do. A limit the operating system cannot apply is ignored.
type TreeOptions struct {
	// Priority is the process priority of every process of the tree; "" is PriorityNormal.
	Priority Priority `json:"priority,omitempty"`
	// MemoryLimitMB caps the memory committed by the whole tree (Windows: JOB_OBJECT_LIMIT_JOB_MEMORY; ignored
	// elsewhere); 0 = no cap.
	MemoryLimitMB int `json:"memoryLimitMB,omitempty"`
	// CPURatePercent hard-caps the tree's CPU to this share of the whole machine, 1-100 (Windows: Job Object CPU
	// rate control; ignored elsewhere); 0 = no cap.
	CPURatePercent int `json:"cpuRatePercent,omitempty"`
	// ShareConsole starts the root with HideWindowShared instead of HideWindow (Windows; ignored elsewhere): for
	// Seelegent's own fixed helper programs only, never for a command line from a user, a repository or a worker.
	// It is a start option of the daemon's own trees, never stored (worker trees always get a console of their
	// own).
	ShareConsole bool `json:"-"`
}

// StartTreeWith is StartTree with resource limits, for commands the daemon starts itself (gate commands):
//
//   - Windows: the Job Object gets the limits (priority class, job memory, CPU rate), and the root is created
//     with BELOW_NORMAL_PRIORITY_CLASS when Priority is low.
//   - Unix, Priority low: the command runs through `nice -n 10` when nice is on PATH, so the root and every
//     descendant start at nice 10 (or lower priority, as nice adds to the daemon's own). Without nice the process
//     group gets nice 10 right after the start; anything the root starts before that call and that has left the
//     group by then keeps the daemon's priority (a race of a few microseconds). The memory and CPU caps are
//     ignored on Unix.
func StartTreeWith(cmd *exec.Cmd, opt TreeOptions) (*Tree, error) {
	return startTree(cmd, false, "", opt)
}

// StartWorkerTreeWith is StartWorkerTree with resource limits:
//
//   - Windows: as StartTreeWith. The caller (the launcher) stays at its own priority; only the job is lowered.
//   - Unix, Priority low: the caller lowers its own nice to at least 10 before it starts cmd, so the worker and
//     everything it starts inherit it with no race. The caller's job is to own this one tree, and a lower nice
//     does not keep it from stopping the tree (same user). On Linux nice is per thread: the call is made on a
//     thread locked for the start (runtime.LockOSThread), so the forking thread is the lowered one; that thread
//     goes back to the runtime with nice 10, the caller's other threads keep theirs. On macOS and the BSDs the
//     whole caller is lowered. The memory and CPU caps are ignored on Unix.
func StartWorkerTreeWith(cmd *exec.Cmd, name string, opt TreeOptions) (*Tree, error) {
	return startTree(cmd, true, name, opt)
}

func startTree(cmd *exec.Cmd, worker bool, name string, opt TreeOptions) (*Tree, error) {
	t := &Tree{cmd: cmd}
	if err := t.os.start(cmd, worker, name, opt); err != nil {
		return nil, err
	}
	t.pid = cmd.Process.Pid
	return t, nil
}

// Pid returns the process id of the root process.
func (t *Tree) Pid() int {
	if t == nil {
		return 0
	}
	return t.pid
}

// WorkerTree describes the tree for SocketOwnerInTrees and WorkerTree.PIDs, with id as its WorkerTree.ID. For a
// tree from StartWorkerTree the root of the description is the calling process (the owner); for StartTree it is
// the root process.
func (t *Tree) WorkerTree(id string) *WorkerTree {
	if t == nil {
		return nil
	}
	return t.os.describe(id, t.pid)
}

// Wait waits for the root process to exit and returns its error like exec.Cmd.Wait (nil for exit code 0).
// Descendants may still be running; Kill or Close stops them.
func (t *Tree) Wait() error {
	if t == nil || t.cmd == nil {
		return ErrNotStarted
	}
	return t.os.wait(t.cmd)
}

// ExitCode returns the exit code of the root process once Wait has returned, or -1 if the root has not exited or
// was ended by a signal.
func (t *Tree) ExitCode() int {
	if t == nil || t.cmd == nil {
		return -1
	}
	return t.os.exitCode(t.cmd)
}

// MemoryLimitHit reports whether the tree ran into its TreeOptions.MemoryLimitMB. Call it after Wait and before
// Close. Windows: the job posted JOB_OBJECT_MSG_JOB_MEMORY_LIMIT to its completion port (an allocation was refused
// because of the cap), or, if that message was missed, the root exited with a non-zero code after the job's peak
// memory reached 90% of the cap. Always false without a cap, after Close, and on Unix (no caps).
func (t *Tree) MemoryLimitHit() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	return t.os.memoryLimitHit(t.os.exitCode(t.cmd))
}

// Poll lets the tree take in what its processes did so far; call it now and then while the tree runs (the
// launcher does on its cancel ticker). It reports whether the tree has run into its memory cap so far. Windows:
// it drains the job's completion port, whose packets would otherwise pile up in kernel memory over a long turn.
// Unix: nothing to do, false.
func (t *Tree) Poll() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return false
	}
	return t.os.poll()
}

// Unapplied names the limits of TreeOptions that were asked for and that the system refused, such as "low
// priority", "memory cap" or "CPU rate cap"; nil when everything asked for holds. The caps that do not exist on
// Unix are not listed: they are documented as ignored there.
func (t *Tree) Unapplied() []string {
	if t == nil {
		return nil
	}
	return t.os.unappliedLimits()
}

// Kill stops every process of the tree. On Unix the tree first gets SIGTERM and, if anything is left after grace,
// SIGKILL, repeated until nothing is left (bounded); grace <= 0 kills at once. On Windows the job is terminated at
// once. Killing a tree whose processes have all exited is not an error. Kill may be called while another
// goroutine waits in Wait.
func (t *Tree) Kill(grace time.Duration) error {
	if t == nil {
		return ErrNotStarted
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	return t.os.kill(grace)
}

// Close stops whatever is left of the tree and releases its resources. It is safe to call more than once.
func (t *Tree) Close() error {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil
	}
	t.closed = true
	return t.os.close(t.cmd)
}
