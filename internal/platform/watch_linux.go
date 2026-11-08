//go:build linux

package platform

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// watchProcess waits in poll on a pidfd (Linux 5.3 and later) and an eventfd written when ctx ends: the
// descriptor refers to the process itself, so a reused id cannot be taken for it once the start time read after
// opening it matches.
func watchProcess(ctx context.Context, pid int, start uint64) (<-chan struct{}, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	switch {
	case errors.Is(err, unix.ESRCH):
		return closedChan(), nil
	case errors.Is(err, unix.ENOSYS):
		return nil, fmt.Errorf("watch process %d: pidfd: %w", pid, errors.ErrUnsupported)
	case err != nil:
		return nil, fmt.Errorf("watch process %d: %w", pid, err)
	}
	if now, err := procStart(pid); err != nil || now != start {
		_ = unix.Close(fd)
		return closedChan(), nil // gone, or the id names another process now
	}
	efd, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("watch process %d: %w", pid, err)
	}
	stop := onCancel(ctx, func() { _, _ = unix.Write(efd, []byte{1, 0, 0, 0, 0, 0, 0, 0}) })
	done := make(chan struct{})
	go func() {
		defer func() {
			stop()
			_ = unix.Close(efd)
			_ = unix.Close(fd)
		}()
		fds := []unix.PollFd{
			{Fd: int32(fd), Events: unix.POLLIN},  //nolint:gosec // a descriptor fits in int32
			{Fd: int32(efd), Events: unix.POLLIN}, //nolint:gosec // a descriptor fits in int32
		}
		for {
			_, err := unix.Poll(fds, -1)
			if errors.Is(err, unix.EINTR) {
				continue
			}
			if err == nil && fds[0].Revents != 0 {
				close(done)
			}
			// Otherwise ctx ended, or the poll failed and the caller's slow checks decide: the channel stays open.
			return
		}
	}()
	return done, nil
}
