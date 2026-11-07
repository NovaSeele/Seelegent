package task

import (
	"errors"
	"fmt"
	"slices"
)

// Errors of the task engine. They wrap a message; test with errors.Is.
var (
	// ErrNotFound: no task (turn, attempt) with that id.
	ErrNotFound = errors.New("not found")
	// ErrInvalid: a request field is missing or wrong.
	ErrInvalid = errors.New("invalid")
	// ErrBadState: the task is not in a state that allows the action (a followup while the task runs, ...).
	ErrBadState = errors.New("bad state")
	// ErrUnavailable: something the action needs is missing (no terminal for a takeover console, the engine is
	// shutting down).
	ErrUnavailable = errors.New("unavailable")
	// ErrConflict: the request collides with other tasks (a lane overlapping their claims, LaneConflictError).
	ErrConflict = errors.New("conflict")
)

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

func badStatef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrBadState, fmt.Sprintf(format, args...))
}

// Next returns the statuses a task may move to from status from. Staying in the same status is always allowed and
// not listed.
//
//	waiting_deps  -> queued (prerequisites met or forced), blocked_deps, cancelled
//	blocked_deps  -> queued (forced), waiting_deps (a prerequisite was reopened), cancelled
//	queued        -> running (an account was acquired), waiting_quota, failed, cancelled
//	running       -> done, failed, cancelled, waiting_quota (no account left for the next attempt)
//	waiting_quota -> running, queued, failed, cancelled
//	done, failed, cancelled -> queued (followup), running (takeover)
func Next(from Status) []Status {
	switch from {
	case StatusWaitingDeps:
		return []Status{StatusQueued, StatusBlockedDeps, StatusCancelled}
	case StatusBlockedDeps:
		return []Status{StatusQueued, StatusWaitingDeps, StatusCancelled}
	case StatusQueued:
		return []Status{StatusRunning, StatusWaitingQuota, StatusFailed, StatusCancelled}
	case StatusRunning:
		return []Status{StatusDone, StatusFailed, StatusCancelled, StatusWaitingQuota}
	case StatusWaitingQuota:
		return []Status{StatusRunning, StatusQueued, StatusFailed, StatusCancelled}
	case StatusDone, StatusFailed, StatusCancelled:
		return []Status{StatusQueued, StatusRunning}
	}
	return nil
}

// CanTransition reports whether a task may move from status from to status to.
func CanTransition(from, to Status) bool {
	if !ValidStatus(to) {
		return false
	}
	return from == to || slices.Contains(Next(from), to)
}

// SetStatus moves the task to status to, or fails with ErrBadState when the table does not allow it.
func (t *Task) SetStatus(to Status) error {
	if !CanTransition(t.Status, to) {
		return badStatef("task %s cannot go from %s to %s", t.ID, t.Status, to)
	}
	t.Status = to
	return nil
}
