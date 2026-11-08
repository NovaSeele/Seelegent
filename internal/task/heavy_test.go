package task

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/gate"
	"github.com/NovaSeele/Seelegent/internal/platform"
	"github.com/NovaSeele/Seelegent/internal/pool"
)

// TestAdmitHeavyWaitsForMemory: a regression run command (AdmitHeavy) waits for memory like a gate and gives its
// slot back meanwhile.
func TestAdmitHeavyWaitsForMemory(t *testing.T) {
	mem := newFakeMemory(1000)
	var slots *gate.Slots
	h := newHarnessWith(t, func(h *harness) {
		h.res = mem
		h.set(memorySettings(0))
		h.set(func(s *config.Settings) { s.GateConcurrency = config.Int(1) })
		slots = gate.NewSlots(func() int { return *h.config().GateConcurrency })
		h.slots = slots
	})
	admitted := make(chan func(), 1)
	go func() {
		release, err := h.s.AdmitHeavy(t.Context())
		if err != nil {
			t.Error(err)
			return
		}
		admitted <- release
	}()
	// The slot is free for others while it waits for memory.
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	release, err := slots.Acquire(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	release()
	time.Sleep(100 * time.Millisecond)
	select {
	case <-admitted:
		t.Fatal("admitted while memory is low")
	default:
	}
	mem.set(5000, 0)
	select {
	case release := <-admitted:
		release()
	case <-time.After(waitLimit):
		t.Fatal("not admitted once memory came back")
	}

	// A cancelled wait says so.
	mem.set(1000, 0)
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	if _, err := h.s.AdmitHeavy(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("AdmitHeavy on a cancelled context = %v", err)
	}
}

// TestWatchdogStopsTrackedCommand: with no task running, the watchdog still watches a tracked heavy command (a
// regression run command) and stops it below the critical line.
func TestWatchdogStopsTrackedCommand(t *testing.T) {
	mem := newFakeMemory(5000)
	h := newHarnessWith(t, func(h *harness) {
		h.res = mem
		h.set(memorySettings(1024))
	})
	ctx, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	untrack := h.s.TrackHeavy(cancel)
	defer untrack()
	mem.set(500, 0)
	select {
	case <-ctx.Done():
	case <-time.After(waitLimit):
		t.Fatal("the tracked command was not stopped")
	}
	if !errors.Is(context.Cause(ctx), gate.ErrLowMemory) {
		t.Fatalf("cause = %v", context.Cause(ctx))
	}
	untrack()
	mem.set(3000, 0)
	h.waitFor("the hold lifted", func() bool { return !h.s.LowMemory() })
}

// TestMemoryHoldLeavesPoolAlone: a task held for memory waits before the pool: no lease, no pool signal, however
// often the reading changes.
func TestMemoryHoldLeavesPoolAlone(t *testing.T) {
	mem := newFakeMemory(1000)
	h := newHarnessWith(t, func(h *harness) {
		h.res = mem
		h.set(memorySettings(0))
	})
	acc := h.agy("w", 1)
	h.queue.Push(t, okScript("conv-p", "Done."))
	changed := h.pool.Changed()
	sum := h.delegate(api.DelegateRequest{Title: "held"})
	h.waitFor("the memory wait", func() bool { return h.task(sum.ID).Waiting == api.WaitingMemory })
	for range 5 {
		mem.set(1100, 0)
		h.clk.Advance(memoryRecheck)
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case <-changed:
		t.Fatal("the pool was signaled while the task waited for memory")
	default:
	}
	if n := h.pool.Busy(acc.ID); n != 0 {
		t.Fatalf("busy = %d", n)
	}
	if n := len(eventsOf(h.events(sum.ID), api.EventWaitingQuota)); n != 1 {
		t.Errorf("%d waiting_quota events", n)
	}
	mem.set(5000, 0)
	h.waitStatus(sum.ID, StatusDone)
}

// TestPoolWaitKeepsMemoryWait: a pool wait between two memory waits keeps the memory wait's start and logs no new
// memory event.
func TestPoolWaitKeepsMemoryWait(t *testing.T) {
	mem := newFakeMemory(1000)
	h := newHarnessWith(t, func(h *harness) {
		h.res = mem
		h.set(memorySettings(0))
	})
	h.agy("w", 1)
	h.queue.Push(t, okScript("conv-k", "Done."))
	sum := h.delegate(api.DelegateRequest{Title: "held"})
	h.waitFor("the memory wait", func() bool { return h.task(sum.ID).MemoryWait != nil })
	since := h.task(sum.ID).MemoryWait.Since
	h.clk.Advance(5 * time.Minute)
	h.s.setWaiting(sum.ID, &pool.Unavailable{Reason: pool.ReasonBusy, Backend: "agy"})
	if tk := h.task(sum.ID); tk.MemoryWait == nil || !tk.MemoryWait.Since.Equal(since) {
		t.Fatalf("pool wait dropped the memory wait: %+v", tk.MemoryWait)
	}
	h.s.setMemoryWaiting(sum.ID, memHold{avail: 1000, need: 2048})
	tk := h.task(sum.ID)
	if tk.Waiting != api.WaitingMemory || tk.MemoryWait == nil || !tk.MemoryWait.Since.Equal(since) {
		t.Fatalf("back in the memory wait: waiting %q, %+v", tk.Waiting, tk.MemoryWait)
	}
	n := 0
	for _, e := range eventsOf(h.events(sum.ID), api.EventWaitingQuota) {
		var d api.WaitingQuotaData
		decode(t, e, &d)
		if d.Reason == api.WaitingMemory {
			n++
		}
	}
	if n != 1 {
		t.Errorf("%d memory waiting_quota events, want 1", n)
	}
	mem.set(5000, 0)
	if tk := h.waitStatus(sum.ID, StatusDone); tk.MemoryWait != nil {
		t.Errorf("memory wait kept after the launch: %+v", tk.MemoryWait)
	}
}

// settledPending is the fake clock's pending timers once they stop changing.
func settledPending(h *harness) int {
	last, same := -1, 0
	for same < 4 {
		time.Sleep(50 * time.Millisecond)
		if n := h.clk.Pending(); n == last {
			same++
		} else {
			last, same = n, 0
		}
	}
	return last
}

// TestIdleWatchdogArmsNoTimer: an idle daemon's watchdog sleeps without a timer and wakes when a heavy command
// starts.
func TestIdleWatchdogArmsNoTimer(t *testing.T) {
	plain := newHarness(t)
	base := settledPending(plain)
	mem := newFakeMemory(5000)
	h := newHarnessWith(t, func(h *harness) {
		h.res = mem
		h.set(memorySettings(1024))
	})
	if got := settledPending(h); got != base {
		t.Fatalf("idle: %d pending timers, %d without the watchdog", got, base)
	}
	h.clk.Advance(10 * watchdogTick)
	if got := settledPending(h); got != base {
		t.Fatalf("idle after advancing: %d pending timers, want %d", got, base)
	}
	_, cancel := context.WithCancelCause(t.Context())
	defer cancel(nil)
	untrack := h.s.TrackHeavy(cancel)
	if !h.clk.WaitTimers(base+1, waitLimit) {
		t.Fatal("the watchdog did not wake for a heavy command")
	}
	untrack()
	h.clk.Advance(watchdogTick)
	if got := settledPending(h); got != base {
		t.Fatalf("idle again: %d pending timers, want %d", got, base)
	}
}

func TestQueueMoveLogged(t *testing.T) {
	for _, c := range []struct {
		was, pos int
		want     bool
	}{
		{10, 9, false}, {10, 6, false}, {10, 5, true}, {5, 4, false}, {5, 3, true}, {3, 2, true}, {2, 1, true},
		{1, 1, false}, {4, 4, false}, {40, 20, true}, {40, 21, false},
	} {
		if got := queueMoveLogged(c.was, c.pos); got != c.want {
			t.Errorf("queueMoveLogged(%d, %d) = %v, want %v", c.was, c.pos, got, c.want)
		}
	}
}

// TestGateQueueLogsFewMoves: a gate at the back of a long slot queue logs a queued event when it starts to wait
// and only for the moves queueMoveLogged keeps, not for every slot release.
func TestGateQueueLogsFewMoves(t *testing.T) {
	var slots *gate.Slots
	h := newHarnessWith(t, func(h *harness) {
		h.set(func(s *config.Settings) { s.GateConcurrency = config.Int(1) })
		slots = gate.NewSlots(func() int { return *h.config().GateConcurrency })
		h.slots = slots
	})
	h.initRepo(map[string]string{"a.txt": "a\n"})
	h.agy("w", 1)
	hold, err := slots.Acquire(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	const ahead = 9
	acquired := make(chan int, ahead)
	releases := make([]chan struct{}, ahead)
	for i := range ahead {
		releases[i] = make(chan struct{})
		queued := make(chan struct{})
		go func() {
			var once bool
			rel, err := slots.Acquire(t.Context(), func(int) {
				if !once {
					once = true
					close(queued)
				}
			})
			if err != nil {
				return
			}
			acquired <- i
			<-releases[i]
			rel()
		}()
		<-queued
	}
	h.queue.Push(t, editScript("conv-q", filepath.Join(h.cwd, "a.txt"), "a2\n", "Done."))
	sum := h.delegate(api.DelegateRequest{Title: "last in line", GateCommands: []string{gateCmd(t, "a.txt", "a2")}})
	h.waitFor("the gate at the back of the queue", func() bool {
		q := h.gateEvents(sum.ID, api.GateEventQueued)
		return len(q) == 1 && q[0].Position == ahead+1
	})
	hold()
	for i := range ahead {
		if got := <-acquired; got != i {
			t.Fatalf("waiter %d got the slot before %d", got, i)
		}
		close(releases[i])
	}
	h.waitStatus(sum.ID, StatusDone)
	var positions []int
	for _, d := range h.gateEvents(sum.ID, api.GateEventQueued) {
		positions = append(positions, d.Position)
	}
	if len(positions) > 5 || positions[0] != ahead+1 {
		t.Fatalf("queued positions logged = %v, want at most 5 starting at %d", positions, ahead+1)
	}
	for i := 1; i < len(positions); i++ {
		if !queueMoveLogged(positions[i-1], positions[i]) {
			t.Errorf("logged a move %d -> %d (all: %v)", positions[i-1], positions[i], positions)
		}
	}
}

// TestAutoThresholdHoldsStill: with the thresholds on auto, the need comes from the machine profile, never from a
// reading: readings that move (their totals included) log one waiting_quota event with one need.
func TestAutoThresholdHoldsStill(t *testing.T) {
	mem := newFakeMemory(1000)
	h := newHarnessWith(t, func(h *harness) {
		h.res = mem
		h.machine = func() config.Machine { return config.NewMachine(16*1024, 0, 8, "linux") } // auto 2048
	})
	h.agy("w", 1)
	h.queue.Push(t, okScript("conv-a", "Done."))
	sum := h.delegate(api.DelegateRequest{Title: "auto"})
	h.waitFor("the memory wait", func() bool { return h.task(sum.ID).MemoryWait != nil })
	for i, total := range []int64{15000, 17000, 4000, 64000} {
		mem.mu.Lock()
		mem.m = platform.MemoryStatus{AvailPhysMB: 1000 + int64(i)*100, TotalPhysMB: total, Known: true}
		close(mem.wake)
		mem.wake = make(chan struct{})
		mem.mu.Unlock()
		h.clk.Advance(memoryRecheck)
		time.Sleep(30 * time.Millisecond)
	}
	if mw := h.task(sum.ID).MemoryWait; mw == nil || mw.NeedMB != 2048 {
		t.Fatalf("memory wait = %+v", mw)
	}
	var needs []int64
	for _, e := range eventsOf(h.events(sum.ID), api.EventWaitingQuota) {
		var d api.WaitingQuotaData
		decode(t, e, &d)
		needs = append(needs, d.NeedMB)
	}
	if len(needs) != 1 || needs[0] != 2048 {
		t.Fatalf("waiting_quota needs = %v", needs)
	}
	mem.set(5000, 0)
	h.waitStatus(sum.ID, StatusDone)
}
