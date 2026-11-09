package task

import (
	"testing"
	"time"
)

// overlapState is the contention cache's size and whether its sweeper runs.
func overlapState(h *harness) (int, bool) {
	h.s.overlapMu.Lock()
	defer h.s.overlapMu.Unlock()
	return len(h.s.overlaps), h.s.overlapSweep
}

// TestOverlapCacheEmptiesWhenIdle: a cached contention view is dropped when its TTL ends, with no further call,
// and the sweeper then ends (no timer left).
func TestOverlapCacheEmptiesWhenIdle(t *testing.T) {
	h := newHarness(t)
	base := settledPending(h)
	if _, err := h.s.repoTasks(t.Context(), h.cwd); err != nil {
		t.Fatal(err)
	}
	if n, sweeping := overlapState(h); n != 1 || !sweeping {
		t.Fatalf("after a view: %d cached, sweeping %v", n, sweeping)
	}
	if !h.clk.WaitTimers(base+1, waitLimit) {
		t.Fatal("the sweeper armed no timer")
	}
	h.clk.Advance(overlapTTL - time.Millisecond)
	if n, _ := overlapState(h); n != 1 {
		t.Fatalf("%d cached before the TTL ended, want 1", n)
	}
	h.clk.Advance(time.Millisecond)
	h.waitFor("the expired view to be dropped", func() bool {
		n, sweeping := overlapState(h)
		return n == 0 && !sweeping
	})
	if got := settledPending(h); got != base {
		t.Fatalf("%d pending timers after the sweep, want %d", got, base)
	}
	// A new view starts a new sweeper.
	if _, err := h.s.repoTasks(t.Context(), h.cwd); err != nil {
		t.Fatal(err)
	}
	if n, sweeping := overlapState(h); n != 1 || !sweeping {
		t.Fatalf("after a second view: %d cached, sweeping %v", n, sweeping)
	}
}
