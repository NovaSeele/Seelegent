//go:build unix

package platform

import (
	"errors"
	"fmt"
	"os/exec"
	"syscall"
	"time"
)

// groupPoll is how often kill checks whether the process group is gone during the grace period.
const groupPoll = 20 * time.Millisecond

// treeOS holds the process group of a tree.
type treeOS struct {
	pgid int
}

// start runs cmd as the leader of a new process group; its descendants inherit the group. On Linux the root also
// gets SIGKILL when the owner of the tree dies (parentDeathSignal).
func (t *treeOS) start(cmd *exec.Cmd) error {
	sysProcAttr(cmd).Setpgid = true
	parentDeathSignal(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	t.pgid = cmd.Process.Pid
	return nil
}

// kill sends SIGTERM to the group, waits up to grace for it to empty, then sends SIGKILL.
func (t *treeOS) kill(grace time.Duration) error {
	if t.pgid <= 0 {
		return ErrNotStarted
	}
	if grace > 0 {
		if err := signalGroup(t.pgid, syscall.SIGTERM); err != nil {
			return err
		}
		deadline := time.Now().Add(grace)
		for time.Now().Before(deadline) {
			if !groupAlive(t.pgid) {
				return nil
			}
			time.Sleep(groupPoll)
		}
	}
	return signalGroup(t.pgid, syscall.SIGKILL)
}

func (t *treeOS) close() error {
	if t.pgid <= 0 {
		return nil
	}
	return signalGroup(t.pgid, syscall.SIGKILL)
}

// signalGroup sends sig to every process of group pgid; an empty group is not an error.
func signalGroup(pgid int, sig syscall.Signal) error {
	if err := syscall.Kill(-pgid, sig); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("signal process group %d: %w", pgid, err)
	}
	return nil
}

// groupAlive reports whether any process (zombies included) is left in group pgid.
func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
