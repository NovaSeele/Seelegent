// Package clock is the time source of the services that schedule work (quota refresh, pool waits, usage
// snapshots). Production code uses Real; tests use Fake to move time by hand, so timing logic is tested without
// sleeping.
package clock

import (
	"sort"
	"sync"
	"time"
)

// Clock tells the time and makes timers.
type Clock interface {
	// Now returns the current time.
	Now() time.Time
	// NewTimer returns a timer that fires once after d (at once when d <= 0).
	NewTimer(d time.Duration) Timer
}

// Timer is a one-shot timer.
type Timer interface {
	// C delivers the time when the timer fires.
	C() <-chan time.Time
	// Stop prevents the timer from firing; it reports whether the call stopped it.
	Stop() bool
}

// Real returns the system clock.
func Real() Clock { return realClock{} }

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

type realTimer struct{ t *time.Timer }

func (r realTimer) C() <-chan time.Time { return r.t.C }
func (r realTimer) Stop() bool          { return r.t.Stop() }

// Fake is a clock that only moves when told to. It is safe for concurrent use.
type Fake struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
	added  chan struct{}
}

// NewFake returns a fake clock set to now.
func NewFake(now time.Time) *Fake {
	return &Fake{now: now, added: make(chan struct{})}
}

// Now implements Clock.
func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

// NewTimer implements Clock. A timer with d <= 0 fires at once.
func (f *Fake) NewTimer(d time.Duration) Timer {
	f.mu.Lock()
	defer f.mu.Unlock()
	t := &fakeTimer{f: f, at: f.now.Add(d), c: make(chan time.Time, 1)}
	if d <= 0 {
		t.c <- f.now
		t.fired = true
		return t
	}
	f.timers = append(f.timers, t)
	close(f.added)
	f.added = make(chan struct{})
	return t
}

// Advance moves the clock forward by d and fires the timers that are due, earliest first.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	sort.SliceStable(f.timers, func(i, j int) bool { return f.timers[i].at.Before(f.timers[j].at) })
	kept := f.timers[:0]
	for _, t := range f.timers {
		if !t.at.After(f.now) {
			t.fired = true
			t.c <- f.now
			continue
		}
		kept = append(kept, t)
	}
	f.timers = kept
}

// Pending returns the number of timers that have not fired or been stopped.
func (f *Fake) Pending() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.timers)
}

// WaitTimers blocks until at least n timers are pending or timeout passes (real time); it reports whether they
// are. Tests use it to let a goroutine reach its timer before they advance the clock.
func (f *Fake) WaitTimers(n int, timeout time.Duration) bool {
	deadline := time.After(timeout)
	for {
		f.mu.Lock()
		count, added := len(f.timers), f.added
		f.mu.Unlock()
		if count >= n {
			return true
		}
		select {
		case <-added:
		case <-deadline:
			return false
		}
	}
}

type fakeTimer struct {
	f     *Fake
	at    time.Time
	c     chan time.Time
	fired bool
}

func (t *fakeTimer) C() <-chan time.Time { return t.c }

func (t *fakeTimer) Stop() bool {
	t.f.mu.Lock()
	defer t.f.mu.Unlock()
	if t.fired {
		return false
	}
	for i, x := range t.f.timers {
		if x == t {
			t.f.timers = append(t.f.timers[:i], t.f.timers[i+1:]...)
			t.fired = true
			return true
		}
	}
	return false
}
