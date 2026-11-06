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

// StartTree starts cmd hidden (no console window on Windows) as the root of a new process tree. cmd must not have
// been started. The caller must call Wait to reap the root process and Close when done with the tree.
func StartTree(cmd *exec.Cmd) (*Tree, error) {
	return startTree(cmd, false, "")
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
	return startTree(cmd, true, name)
}

func startTree(cmd *exec.Cmd, worker bool, name string) (*Tree, error) {
	t := &Tree{cmd: cmd}
	if err := t.os.start(cmd, worker, name); err != nil {
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
