package gate

import (
	"context"
	"sync"
)

// Slots limits the gate commands that run at the same time (the setting gateConcurrency). Commands get a slot in
// the order they asked for one (FIFO); the engine's gates and the proof regression runs share one Slots. It is safe
// for concurrent use.
type Slots struct {
	// limit returns the current number of slots; 0 or less is unlimited. It is called with the lock held, so it
	// must be cheap and must not call back into the Slots.
	limit func() int

	mu      sync.Mutex
	running int
	queue   []*slotWaiter
}

// slotWaiter is one Acquire waiting in the queue.
type slotWaiter struct {
	// ready is closed (with the lock held) when the waiter got its slot.
	ready chan struct{}
	// moved tells the waiter that its place in the queue may have changed.
	moved chan struct{}
}

// NewSlots returns Slots whose size is limit(), read again at every Acquire, release and Notify; a nil limit is
// unlimited.
func NewSlots(limit func() int) *Slots {
	if limit == nil {
		limit = func() int { return 0 }
	}
	return &Slots{limit: limit}
}

// Acquire waits for a free slot and returns the function that gives it back (calling it more than once is
// harmless). While it waits, onWait (optional) gets the caller's place in the queue, from 1, on the caller's
// goroutine: first when it starts waiting, then every time the place changes. A slot that is free at once calls
// onWait never. When ctx ends first, Acquire leaves the queue and returns ctx's error.
func (s *Slots) Acquire(ctx context.Context, onWait func(pos int)) (release func(), err error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	if len(s.queue) == 0 && s.freeLocked() {
		s.running++
		s.mu.Unlock()
		return s.releaser(), nil
	}
	w := &slotWaiter{ready: make(chan struct{}), moved: make(chan struct{}, 1)}
	s.queue = append(s.queue, w)
	pos := len(s.queue)
	s.mu.Unlock()

	last := 0
	report := func(p int) {
		if onWait != nil && p > 0 && p != last {
			last = p
			onWait(p)
		}
	}
	report(pos)
	for {
		select {
		case <-w.ready:
			return s.releaser(), nil
		case <-w.moved:
			s.mu.Lock()
			p := s.positionLocked(w)
			s.mu.Unlock()
			report(p)
		case <-ctx.Done():
			s.mu.Lock()
			select {
			case <-w.ready: // the slot came at the same moment: give it back
				s.mu.Unlock()
				s.releaser()()
				return nil, ctx.Err()
			default:
			}
			s.removeLocked(w)
			s.admitLocked(true)
			s.mu.Unlock()
			return nil, ctx.Err()
		}
	}
}

// Notify admits waiting commands after the limit grew (a settings change). Lowering the limit never stops a running
// command: new ones wait until enough have ended.
func (s *Slots) Notify() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.admitLocked(false)
}

// Limit returns the current number of slots (0 = unlimited).
func (s *Slots) Limit() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return max(s.limit(), 0)
}

// releaser returns the function that gives one slot back, once.
func (s *Slots) releaser() func() {
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			s.running--
			s.admitLocked(false)
		})
	}
}

// freeLocked reports whether one more command may run now.
func (s *Slots) freeLocked() bool {
	l := s.limit()
	return l <= 0 || s.running < l
}

// admitLocked hands free slots to the head of the queue, in order, and tells the ones left behind that they moved
// when anything left the queue (or when changed says it already did).
func (s *Slots) admitLocked(changed bool) {
	for len(s.queue) > 0 && s.freeLocked() {
		w := s.queue[0]
		s.queue[0] = nil
		s.queue = s.queue[1:]
		s.running++
		close(w.ready)
		changed = true
	}
	if !changed {
		return
	}
	for _, w := range s.queue {
		select {
		case w.moved <- struct{}{}:
		default: // already told
		}
	}
}

// positionLocked returns w's place in the queue from 1, or 0 once it left it.
func (s *Slots) positionLocked(w *slotWaiter) int {
	for i, q := range s.queue {
		if q == w {
			return i + 1
		}
	}
	return 0
}

// removeLocked takes w out of the queue.
func (s *Slots) removeLocked(w *slotWaiter) {
	for i, q := range s.queue {
		if q == w {
			s.queue = append(s.queue[:i], s.queue[i+1:]...)
			return
		}
	}
}
