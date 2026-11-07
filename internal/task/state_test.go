package task

import (
	"errors"
	"testing"
)

func TestTransitionTable(t *testing.T) {
	// want lists every allowed move; everything else (except staying) must be refused.
	want := map[Status][]Status{
		StatusWaitingDeps:  {StatusQueued, StatusBlockedDeps, StatusCancelled},
		StatusBlockedDeps:  {StatusQueued, StatusWaitingDeps, StatusCancelled},
		StatusQueued:       {StatusRunning, StatusWaitingQuota, StatusFailed, StatusCancelled},
		StatusRunning:      {StatusDone, StatusFailed, StatusCancelled, StatusWaitingQuota},
		StatusWaitingQuota: {StatusRunning, StatusQueued, StatusFailed, StatusCancelled},
		StatusDone:         {StatusQueued, StatusRunning},
		StatusFailed:       {StatusQueued, StatusRunning},
		StatusCancelled:    {StatusQueued, StatusRunning},
	}
	for _, from := range Statuses() {
		allowed := map[Status]bool{from: true}
		for _, to := range want[from] {
			allowed[to] = true
		}
		for _, to := range Statuses() {
			if got := CanTransition(from, to); got != allowed[to] {
				t.Errorf("CanTransition(%s, %s) = %v, want %v", from, to, got, allowed[to])
			}
		}
		if CanTransition(from, "bogus") {
			t.Errorf("%s -> bogus allowed", from)
		}
	}
	if Next("bogus") != nil {
		t.Error("unknown status has moves")
	}
}

func TestSetStatus(t *testing.T) {
	tk := &Task{ID: "t1", Status: StatusQueued}
	if err := tk.SetStatus(StatusRunning); err != nil || tk.Status != StatusRunning {
		t.Fatalf("SetStatus = %v, status %s", err, tk.Status)
	}
	if err := tk.SetStatus(StatusWaitingDeps); !errors.Is(err, ErrBadState) || tk.Status != StatusRunning {
		t.Fatalf("SetStatus(waiting_deps) = %v, status %s", err, tk.Status)
	}
}

func TestStatusClasses(t *testing.T) {
	cases := []struct {
		s                         Status
		active, pending, finished bool
	}{
		{StatusWaitingDeps, false, true, false},
		{StatusBlockedDeps, false, false, false},
		{StatusQueued, true, true, false},
		{StatusRunning, true, true, false},
		{StatusWaitingQuota, true, true, false},
		{StatusDone, false, false, true},
		{StatusFailed, false, false, true},
		{StatusCancelled, false, false, true},
	}
	for _, c := range cases {
		if Active(c.s) != c.active || Pending(c.s) != c.pending || Finished(c.s) != c.finished {
			t.Errorf("%s: active %v pending %v finished %v", c.s, Active(c.s), Pending(c.s), Finished(c.s))
		}
		if !ValidStatus(c.s) {
			t.Errorf("%s not valid", c.s)
		}
	}
}
