//go:build windows

package platform

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

// watchProcess waits on a process handle (SYNCHRONIZE) and an event set when ctx ends: the handle keeps the
// process object, so its id cannot name another process while it is open, and the start time read through it
// tells a reused id apart.
func watchProcess(ctx context.Context, pid int, start uint64) (<-chan struct{}, error) {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false,
		uint32(pid)) //nolint:gosec // pid > 0 checked by WatchProcess
	if err != nil {
		if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
			return closedChan(), nil // no such process
		}
		return nil, fmt.Errorf("watch process %d: %w", pid, err)
	}
	var created, exited, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(h, &created, &exited, &kernel, &user); err != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("watch process %d: %w", pid, err)
	}
	if uint64(created.HighDateTime)<<32|uint64(created.LowDateTime) != start {
		_ = windows.CloseHandle(h)
		return closedChan(), nil // the id names another process now
	}
	cancelled, err := windows.CreateEvent(nil, 1, 0, nil)
	if err != nil {
		_ = windows.CloseHandle(h)
		return nil, fmt.Errorf("watch process %d: %w", pid, err)
	}
	stop := onCancel(ctx, func() { _ = windows.SetEvent(cancelled) })
	done := make(chan struct{})
	go func() {
		defer func() {
			stop()
			_ = windows.CloseHandle(cancelled)
			_ = windows.CloseHandle(h)
		}()
		ev, err := windows.WaitForMultipleObjects([]windows.Handle{h, cancelled}, false, windows.INFINITE)
		if err == nil && ev == windows.WAIT_OBJECT_0 {
			close(done)
		}
		// Otherwise ctx ended, or the wait failed and the caller's slow checks decide: the channel stays open.
	}()
	return done, nil
}
