//go:build unix

package launch

import (
	"errors"
	"fmt"
	"syscall"
)

// askStop asks a launcher to stop its worker tree: SIGTERM makes `seelegent __launch` stop the tree the way a
// cancel does, then exit.
func askStop(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop launcher %d: %w", pid, err)
	}
	return nil
}

// forceStop kills one process of a worker tree.
func forceStop(pid int) error {
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill process %d: %w", pid, err)
	}
	return nil
}
