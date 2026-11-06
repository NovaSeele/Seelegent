package platform

import (
	"os/exec"
	"sync"
	"time"
)

// Tree is a process started with StartTree together with every process it starts. Stopping the tree stops all of
// them, however deep: on Windows they all belong to a Job Object that the Tree owns (with kill-on-close, so the tree
// also dies if the owner of the Tree dies), on Unix they share a new process group.
//
// Known limit on Unix: a descendant that moves itself into another session or process group (a daemonising
// program) leaves the tree.
type Tree struct {
	cmd *exec.Cmd
	os  treeOS

	mu     sync.Mutex
	closed bool
}

// StartTree starts cmd hidden (no console window on Windows) as the root of a new process tree. cmd must not have
// been started. The caller must call Wait to reap the root process and Close when done with the tree.
func StartTree(cmd *exec.Cmd) (*Tree, error) {
	t := &Tree{cmd: cmd}
	if err := t.os.start(cmd); err != nil {
		return nil, err
	}
	return t, nil
}

// Pid returns the process id of the root process.
func (t *Tree) Pid() int {
	if t == nil || t.cmd == nil || t.cmd.Process == nil {
		return 0
	}
	return t.cmd.Process.Pid
}

// Wait waits for the root process to exit and returns its error like exec.Cmd.Wait. Descendants may still be
// running; Kill or Close stops them.
func (t *Tree) Wait() error {
	if t == nil || t.cmd == nil {
		return ErrNotStarted
	}
	return t.cmd.Wait()
}

// Kill stops every process of the tree. On Unix the group first gets SIGTERM and, if anything is left after grace,
// SIGKILL; grace <= 0 kills at once. On Windows the job is terminated at once. Killing a tree whose processes have
// all exited is not an error. Kill may be called while another goroutine waits in Wait.
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
	return t.os.close()
}
