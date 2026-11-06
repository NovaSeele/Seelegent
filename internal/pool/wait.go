package pool

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Wait bounds (old tasks.js quota wait): a waiting task looks again at least every maxWaitStep and, when an
// account is due back, no earlier than minWaitStep before it; with no due time it looks every defaultWaitStep.
// Releases, cooldown changes and quota reads (Notify) wake it at once.
const (
	minWaitStep     = 5 * time.Second
	maxWaitStep     = 10 * time.Minute
	defaultWaitStep = 30 * time.Second
)

// Wait acquires an account for req, waiting while every matching account rests or is busy. onWait (may be nil) is
// called with the reason whenever the reason or the due time changes, so the caller can show the task as waiting.
// Wait returns at once with an error matching ErrNoAccount when no account can ever run the request, and when ctx
// ends with an error matching ctx.Err() and the last *Unavailable.
func (p *Pool) Wait(ctx context.Context, req Request, onWait func(*Unavailable)) (*Lease, Choice, error) {
	var last *Unavailable
	for {
		changed := p.changed() // before the attempt, so a release in between is not missed
		lease, choice, err := p.Acquire(ctx, req)
		if err == nil {
			return lease, choice, nil
		}
		var u *Unavailable
		if !errors.As(err, &u) || u.Reason == ReasonNoAccount {
			return nil, Choice{}, err
		}
		if onWait != nil && (last == nil || last.Reason != u.Reason || !last.Until.Equal(u.Until)) {
			onWait(u)
		}
		last = u
		t := p.o.Clock.NewTimer(waitStep(u, p.o.Clock.Now()))
		select {
		case <-ctx.Done():
			t.Stop()
			return nil, Choice{}, fmt.Errorf("wait for a %s account: %w: %w", req.Backend, ctx.Err(), u)
		case <-changed:
			t.Stop()
		case <-t.C():
		}
	}
}

// waitStep returns how long to sleep before looking again.
func waitStep(u *Unavailable, now time.Time) time.Duration {
	if u.Until.IsZero() {
		return defaultWaitStep
	}
	return min(max(u.Until.Sub(now), minWaitStep), maxWaitStep)
}
