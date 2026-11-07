//go:build unix

package platform

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
)

func hideWindow(*exec.Cmd) {}

func setCmdLine(*exec.Cmd, string) {}

// sysProcAttr returns cmd.SysProcAttr, creating it when needed.
func sysProcAttr(cmd *exec.Cmd) *syscall.SysProcAttr {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	return cmd.SysProcAttr
}

func startDetached(newCmd func() *exec.Cmd) (*exec.Cmd, error) {
	cmd := newCmd()
	sysProcAttr(cmd).Setsid = true
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func killTree(_ context.Context, pid int) error {
	// A process started as a group leader takes its group with it; otherwise only the process itself is reachable.
	if err := syscall.Kill(-pid, syscall.SIGKILL); err == nil {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("kill process %d: %w", pid, err)
	}
	return nil
}
