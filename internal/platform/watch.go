package platform

import (
	"context"
	"errors"
	"fmt"
)

// WatchProcess returns a channel that is closed once process pid, which started at start (ProcessStartTime), has
// ended; it is closed at once when pid no longer names that process (it ended and the id may have been reused).
// The process does not have to be a child of this one: a daemon that restarted watches the launchers an earlier
// daemon started. The watch holds one goroutine blocked in the system's wait (no polling: it wakes only when the
// process ends or ctx does) and ends with ctx (the channel then stays open).
//
// It fails with an error wrapping errors.ErrUnsupported where the system cannot wait for a process that is not a
// child (Linux before 5.3, BSDs) and when start is 0 (unknown: a reused id could not be told apart). Other errors
// are possible too (a sandbox refusing pidfd_open with EPERM, a process that cannot be opened): callers treat
// every error as "poll for the end instead".
func WatchProcess(ctx context.Context, pid int, start uint64) (<-chan struct{}, error) {
	if pid <= 0 || start == 0 {
		return nil, fmt.Errorf("watch process %d: no start time: %w", pid, errors.ErrUnsupported)
	}
	return watchProcess(ctx, pid, start)
}

// closedChan returns a closed channel: the watched process has already ended.
func closedChan() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}

// onCancel calls signal once ctx ends, to wake a goroutine blocked in a system wait. stop undoes it, or waits for
// signal to return when it already ran, so that the caller may then release what signal uses.
func onCancel(ctx context.Context, signal func()) (stop func()) {
	fired := make(chan struct{})
	stopSignal := context.AfterFunc(ctx, func() {
		defer close(fired)
		signal()
	})
	return func() {
		if !stopSignal() {
			<-fired
		}
	}
}
