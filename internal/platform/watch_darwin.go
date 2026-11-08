//go:build darwin

package platform

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

// watchProcess waits on a kqueue for the process's NOTE_EXIT, or a user event triggered when ctx ends. The start
// time is read after the registration: a match means the registered process is the one that started then.
func watchProcess(ctx context.Context, pid int, start uint64) (<-chan struct{}, error) {
	kq, err := unix.Kqueue()
	if err != nil {
		return nil, fmt.Errorf("watch process %d: %w", pid, err)
	}
	var ev unix.Kevent_t
	unix.SetKevent(&ev, pid, unix.EVFILT_PROC, unix.EV_ADD|unix.EV_ONESHOT)
	ev.Fflags = unix.NOTE_EXIT
	if _, err := unix.Kevent(kq, []unix.Kevent_t{ev}, nil, nil); err != nil {
		_ = unix.Close(kq)
		if errors.Is(err, unix.ESRCH) {
			return closedChan(), nil
		}
		return nil, fmt.Errorf("watch process %d: %w", pid, err)
	}
	if now, err := procStart(pid); err != nil || now != start {
		_ = unix.Close(kq)
		return closedChan(), nil // gone, or the id names another process now
	}
	var user unix.Kevent_t
	unix.SetKevent(&user, 0, unix.EVFILT_USER, unix.EV_ADD|unix.EV_CLEAR)
	if _, err := unix.Kevent(kq, []unix.Kevent_t{user}, nil, nil); err != nil {
		_ = unix.Close(kq)
		return nil, fmt.Errorf("watch process %d: %w", pid, err)
	}
	stop := onCancel(ctx, func() {
		var trigger unix.Kevent_t
		unix.SetKevent(&trigger, 0, unix.EVFILT_USER, 0)
		trigger.Fflags = unix.NOTE_TRIGGER
		_, _ = unix.Kevent(kq, []unix.Kevent_t{trigger}, nil, nil)
	})
	done := make(chan struct{})
	go func() {
		defer func() {
			stop()
			_ = unix.Close(kq)
		}()
		events := make([]unix.Kevent_t, 2)
		for {
			n, err := unix.Kevent(kq, nil, events, nil)
			if errors.Is(err, unix.EINTR) {
				continue
			}
			for _, ev := range events[:max(n, 0)] {
				if ev.Filter == unix.EVFILT_PROC {
					close(done)
					return
				}
			}
			// ctx ended, or the wait failed and the caller's slow checks decide: the channel stays open.
			return
		}
	}()
	return done, nil
}
