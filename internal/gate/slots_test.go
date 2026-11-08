package gate

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const slotWait = 5 * time.Second

// waiter is one Acquire running in the background, with the positions it was told.
type waiter struct {
	mu      sync.Mutex
	pos     []int
	waiting chan struct{} // closed at the first onWait
	got     chan func()
	err     chan error
}

func startWaiter(ctx context.Context, s *Slots) *waiter {
	w := &waiter{waiting: make(chan struct{}), got: make(chan func(), 1), err: make(chan error, 1)}
	var once sync.Once
	go func() {
		release, err := s.Acquire(ctx, func(p int) {
			w.mu.Lock()
			w.pos = append(w.pos, p)
			w.mu.Unlock()
			once.Do(func() { close(w.waiting) })
		})
		if err != nil {
			w.err <- err
			return
		}
		w.got <- release
	}()
	return w
}

func (w *waiter) mustWait(t *testing.T) {
	t.Helper()
	select {
	case <-w.waiting:
	case r := <-w.got:
		r()
		t.Fatal("got a slot, want to wait")
	case <-time.After(slotWait):
		t.Fatal("never started waiting")
	}
}

func (w *waiter) mustGet(t *testing.T) func() {
	t.Helper()
	select {
	case r := <-w.got:
		return r
	case err := <-w.err:
		t.Fatalf("Acquire: %v", err)
	case <-time.After(slotWait):
		t.Fatal("no slot")
	}
	return nil
}

func (w *waiter) mustNotGet(t *testing.T) {
	t.Helper()
	select {
	case r := <-w.got:
		r()
		t.Fatal("got a slot too early")
	case <-time.After(50 * time.Millisecond):
	}
}

func (w *waiter) positions() []int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]int(nil), w.pos...)
}

func acquireNow(t *testing.T, s *Slots) func() {
	t.Helper()
	release, err := s.Acquire(t.Context(), func(int) { t.Error("waited for a free slot") })
	if err != nil {
		t.Fatal(err)
	}
	return release
}

func TestSlotsUnlimited(t *testing.T) {
	for _, s := range []*Slots{NewSlots(nil), NewSlots(func() int { return 0 }), NewSlots(func() int { return -3 })} {
		var releases []func()
		for range 50 {
			releases = append(releases, acquireNow(t, s))
		}
		for _, r := range releases {
			r()
		}
		if s.Limit() != 0 {
			t.Fatalf("limit = %d", s.Limit())
		}
	}
}

func TestSlotsFIFOAndPositions(t *testing.T) {
	s := NewSlots(func() int { return 2 })
	r1, r2 := acquireNow(t, s), acquireNow(t, s)
	w3 := startWaiter(t.Context(), s)
	w3.mustWait(t)
	w4 := startWaiter(t.Context(), s)
	w4.mustWait(t)

	r1()
	r1() // a second release is harmless
	r3 := w3.mustGet(t)
	w4.mustNotGet(t)
	r2()
	r4 := w4.mustGet(t)
	if got := w3.positions(); len(got) != 1 || got[0] != 1 {
		t.Errorf("first waiter positions = %v, want [1]", got)
	}
	if got := w4.positions(); len(got) < 2 || got[0] != 2 || got[len(got)-1] != 1 {
		t.Errorf("second waiter positions = %v, want 2 then 1", got)
	}
	r3()
	r4()
	// Every slot is free again: two start at once.
	acquireNow(t, s)()
	a, b := acquireNow(t, s), acquireNow(t, s)
	a()
	b()
}

func TestSlotsCancelLeavesQueue(t *testing.T) {
	s := NewSlots(func() int { return 1 })
	r1 := acquireNow(t, s)
	ctx, cancel := context.WithCancel(t.Context())
	w2 := startWaiter(ctx, s)
	w2.mustWait(t)
	w3 := startWaiter(t.Context(), s)
	w3.mustWait(t)
	cancel()
	select {
	case err := <-w2.err:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v", err)
		}
	case <-time.After(slotWait):
		t.Fatal("cancelled Acquire did not return")
	}
	deadline := time.Now().Add(slotWait)
	for got := w3.positions(); got[len(got)-1] != 1; got = w3.positions() {
		if time.Now().After(deadline) {
			t.Fatalf("positions after the cancel = %v, want to end at 1", got)
		}
		time.Sleep(5 * time.Millisecond)
	}
	r1()
	w3.mustGet(t)()
	acquireNow(t, s)() // the cancelled waiter holds nothing

	done, cancelDone := context.WithCancel(t.Context())
	cancelDone()
	if _, err := s.Acquire(done, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("Acquire on an ended context: %v", err)
	}
}

func TestSlotsNotifyAfterLimitGrows(t *testing.T) {
	var limit atomic.Int32
	limit.Store(1)
	s := NewSlots(func() int { return int(limit.Load()) })
	r1 := acquireNow(t, s)
	w2 := startWaiter(t.Context(), s)
	w2.mustWait(t)
	w3 := startWaiter(t.Context(), s)
	w3.mustWait(t)
	limit.Store(3)
	if s.Limit() != 3 {
		t.Fatalf("limit = %d", s.Limit())
	}
	s.Notify()
	r2, r3 := w2.mustGet(t), w3.mustGet(t)

	// Lowering the limit stops nothing; new commands wait until enough have ended.
	limit.Store(1)
	s.Notify()
	w4 := startWaiter(t.Context(), s)
	w4.mustWait(t)
	r1()
	r2()
	w4.mustNotGet(t)
	r3()
	w4.mustGet(t)()
}
