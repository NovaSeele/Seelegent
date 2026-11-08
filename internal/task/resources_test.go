package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/gate"
	"github.com/NovaSeele/Seelegent/internal/launch"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// fakeMemory is a Resources whose reading the test sets; every set signals Changed.
type fakeMemory struct {
	mu   sync.Mutex
	m    platform.MemoryStatus
	wake chan struct{}
}

func newFakeMemory(phys int64) *fakeMemory {
	return &fakeMemory{m: platform.MemoryStatus{AvailPhysMB: phys, Known: true}, wake: make(chan struct{})}
}

func (f *fakeMemory) Sample() platform.MemoryStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m
}

func (f *fakeMemory) Changed() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.wake
}

func (f *fakeMemory) set(phys, commit int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.m = platform.MemoryStatus{AvailPhysMB: phys, AvailCommitMB: commit, Known: true}
	close(f.wake)
	f.wake = make(chan struct{})
}

// memorySettings are the thresholds of the engine tests: 2048 MB physical, commit off, watchdog at crit (0 = off).
func memorySettings(crit int) func(*config.Settings) {
	return func(s *config.Settings) {
		s.MinFreeMemoryMB, s.MinFreeCommitMB, s.CriticalFreeMemoryMB = 2048, 0, crit
	}
}

// lowLog records the watchdog's flips (Options.OnLowMemory).
type lowLog struct {
	mu    sync.Mutex
	flips []bool
}

func (l *lowLog) note(low bool, _ platform.MemoryStatus) {
	l.mu.Lock()
	l.flips = append(l.flips, low)
	l.mu.Unlock()
}

func (l *lowLog) get() []bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.flips)
}

// gateEvents returns the data of task id's gate events with the given phase.
func (h *harness) gateEvents(id, phase string) []api.GateEventData {
	h.t.Helper()
	var out []api.GateEventData
	for _, e := range eventsOf(h.events(id), api.EventGate) {
		var d api.GateEventData
		decode(h.t, e, &d)
		if d.Phase == phase {
			out = append(out, d)
		}
	}
	return out
}

// waitGateStarts waits until task id's gate has started n runs.
func (h *harness) waitGateStarts(id string, n int) {
	h.t.Helper()
	h.waitFor(fmt.Sprintf("%d gate runs of %s", n, id), func() bool {
		return len(h.gateEvents(id, api.GateEventStart)) >= n
	})
}

func TestMemoryWaitHoldsStart(t *testing.T) {
	mem := newFakeMemory(1000)
	h := newHarnessWith(t, func(h *harness) {
		h.res = mem
		h.set(memorySettings(0))
	})
	acc := h.agy("w", 1)
	h.queue.Push(t, okScript("conv-m", "Done."))
	sum := h.delegate(api.DelegateRequest{Title: "held"})
	h.waitFor("the memory wait", func() bool {
		tk := h.task(sum.ID)
		return tk.Status == StatusWaitingQuota && tk.Waiting == api.WaitingMemory
	})
	tk := h.task(sum.ID)
	if n := len(tk.LastTurn().Attempts); n != 0 {
		t.Fatalf("%d attempts launched while memory is low", n)
	}
	if n := h.pool.Busy(acc.ID); n != 0 {
		t.Fatalf("the held task keeps %d account slots", n)
	}
	if !strings.Contains(tk.WaitDetail, "1000 MB") || !strings.Contains(tk.WaitDetail, "2048 MB") {
		t.Errorf("wait detail %q", tk.WaitDetail)
	}
	got, err := h.s.Summary(t.Context(), sum.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Waiting != api.WaitingMemory || got.Status != StatusWaitingQuota || got.Attention != nil {
		t.Errorf("summary waiting=%q status=%s attention=%+v", got.Waiting, got.Status, got.Attention)
	}
	rep, err := h.s.Report(t.Context(), sum.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Waiting != api.WaitingMemory {
		t.Errorf("report waiting = %q", rep.Waiting)
	}
	waits := eventsOf(h.events(sum.ID), api.EventWaitingQuota)
	if len(waits) != 1 {
		t.Fatalf("%d waiting_quota events", len(waits))
	}
	var d api.WaitingQuotaData
	decode(t, waits[0], &d)
	if d != (api.WaitingQuotaData{Reason: api.WaitingMemory, AvailableMB: 1000, NeedMB: 2048}) {
		t.Errorf("waiting_quota = %+v", d)
	}

	mem.set(5000, 0)
	tk = h.waitStatus(sum.ID, StatusDone)
	if tk.Waiting != "" || tk.MemoryWait != nil || len(tk.LastTurn().Attempts) != 1 {
		t.Errorf("after the wait: waiting=%q memoryWait=%+v attempts=%d", tk.Waiting, tk.MemoryWait,
			len(tk.LastTurn().Attempts))
	}
	if got, _ := h.s.Summary(t.Context(), sum.ID); got.Waiting != "" {
		t.Errorf("summary waiting after the run = %q", got.Waiting)
	}
}

func TestResourcesWaitAttention(t *testing.T) {
	mem := newFakeMemory(1000)
	h := newHarnessWith(t, func(h *harness) {
		h.res = mem
		h.set(memorySettings(0))
	})
	h.agy("w", 1)
	h.queue.Push(t, okScript("conv-r", "Done."))
	sum := h.delegate(api.DelegateRequest{Title: "long wait"})
	h.waitFor("the memory wait", func() bool { return h.task(sum.ID).Waiting == api.WaitingMemory })
	if a := h.task(sum.ID).Attention; a != nil {
		t.Fatalf("attention before %s: %+v", ResourcesWaitAfter, a)
	}
	h.clk.Advance(ResourcesWaitAfter + time.Minute)
	h.waitFor("resources_wait", func() bool {
		a := h.task(sum.ID).Attention
		return a != nil && a.Reason == ReasonResourcesWait
	})
	tk := h.task(sum.ID)
	a := tk.Attention
	if a.State != api.AttentionInfo || a.Who != WhoUser || a.Params == nil || a.Params.AvailableMB != 1000 ||
		a.Params.NeedMB != 2048 || a.Params.Minutes < 10 || a.Detail != "free 1000 MB < threshold 2048 MB" {
		t.Fatalf("attention = %+v params = %+v", a, a.Params)
	}
	if needsLead(tk) || !tk.NeedsSince.IsZero() || flagKey(tk) != "" {
		t.Errorf("a memory wait must not need the lead (needsSince %v, flag %q)", tk.NeedsSince, flagKey(tk))
	}
	if tk.Status != StatusWaitingQuota || len(tk.LastTurn().Attempts) != 0 {
		t.Errorf("status %s, %d attempts", tk.Status, len(tk.LastTurn().Attempts))
	}
	mem.set(5000, 0)
	tk = h.waitStatus(sum.ID, StatusDone)
	if a := tk.Attention; a != nil && a.Reason == ReasonResourcesWait {
		t.Errorf("resources_wait not cleared: %+v", a)
	}
}

// TestGateSlotsFIFO: with gateConcurrency 1 the gate of a second task in the same repository is queued for the
// slot (phase queued, wait slot, position 1) and a regression run asking after it comes after it.
func TestGateSlotsFIFO(t *testing.T) {
	var slots *gate.Slots
	h := newHarnessWith(t, func(h *harness) {
		h.set(func(s *config.Settings) { s.GateConcurrency = 1 })
		slots = gate.NewSlots(func() int { return h.config().GateConcurrency })
		h.slots = slots
	})
	h.initRepo(map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	h.agy("w", 1)
	h.queue.Push(t, editScript("conv-a", filepath.Join(h.cwd, "a.txt"), "a2\n", "A."))
	first := h.delegate(api.DelegateRequest{Title: "A", GateCommands: []string{gateCmd(t, "--wait", "a.release")}})
	h.waitGateStarts(first.ID, 1)

	h.queue.Push(t, editScript("conv-b", filepath.Join(h.cwd, "b.txt"), "b2\n", "B."))
	bCmd := gateCmd(t, "b.txt", "b2")
	second := h.delegate(api.DelegateRequest{Title: "B", GateCommands: []string{bCmd}})
	h.waitFor("B queued for a slot", func() bool {
		ph := h.task(second.ID).GatePhase
		return ph != nil && ph.State == api.GatePhaseQueued && ph.Wait == api.GateWaitSlot
	})
	if q := h.gateEvents(second.ID, api.GateEventQueued); len(q) != 1 || q[0].Wait != api.GateWaitSlot ||
		q[0].Position != 1 || q[0].Limit != 1 {
		t.Fatalf("queued events = %+v", q)
	}
	if got, _ := h.s.Summary(t.Context(), second.ID); got.Gate == nil || got.Gate.Phase != api.GatePhaseQueued ||
		got.Gate.Wait != api.GateWaitSlot {
		t.Errorf("summary gate = %+v", got.Gate)
	}

	// A regression run command asks for a slot after B.
	pos := make(chan int, 8)
	got := make(chan error, 1)
	go func() {
		release, err := slots.Acquire(t.Context(), func(p int) { pos <- p })
		if release != nil {
			defer release()
		}
		if err != nil {
			got <- err
			return
		}
		for _, d := range h.gateEvents(second.ID, api.GateEventStep) {
			if d.Step != nil && d.Step.Cmd == bCmd && d.Step.State == api.StepGreen {
				got <- nil
				return
			}
		}
		got <- errors.New("the regression run got the slot before B's gate ran")
	}()
	if p := <-pos; p != 2 {
		t.Fatalf("regression run at position %d, want 2", p)
	}
	h.write("a.release", "go")
	h.waitStatus(first.ID, StatusDone)
	tk := h.waitStatus(second.ID, StatusDone)
	if g := tk.TurnN(1).Gate; g == nil || g.State != api.GateGreen {
		t.Fatalf("B's gate = %+v", g)
	}
	select {
	case err := <-got:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(waitLimit):
		t.Fatal("the regression run never got a slot")
	}
}

// TestGateQueuedMemoryReleasesSlot: a gate that waits for memory gives its slot back while it waits.
func TestGateQueuedMemoryReleasesSlot(t *testing.T) {
	mem := newFakeMemory(5000)
	var slots *gate.Slots
	h := newHarnessWith(t, func(h *harness) {
		h.res = mem
		h.set(memorySettings(0))
		h.set(func(s *config.Settings) { s.GateConcurrency = 1 })
		slots = gate.NewSlots(func() int { return h.config().GateConcurrency })
		h.slots = slots
	})
	h.initRepo(map[string]string{"a.txt": "a\n"})
	h.agy("w", 1)
	a := filepath.Join(h.cwd, "a.txt")
	goOn := filepath.Join(h.dir, "go-on")
	h.queue.Push(t, script(agyInit("conv-q"), "#!wait-file "+goOn, agyEdit(1, a), fakeWrite(a, "x\n"),
		agyResult("conv-q", "Done.")))
	sum := h.delegate(api.DelegateRequest{Title: "gate waits", GateCommands: []string{gateCmd(t, "a.txt", "x")}})
	h.runningWorker(sum.ID)
	mem.set(1000, 0)
	if err := writeText(goOn, "1"); err != nil {
		t.Fatal(err)
	}
	h.waitFor("the gate queued for memory", func() bool {
		ph := h.task(sum.ID).GatePhase
		return ph != nil && ph.State == api.GatePhaseQueued && ph.Wait == api.GateWaitMemory
	})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	release, err := slots.Acquire(ctx, func(int) { t.Error("the slot is held by a gate that waits for memory") })
	if err != nil {
		t.Fatal(err)
	}
	release()
	if q := h.gateEvents(sum.ID, api.GateEventQueued); len(q) != 1 || q[0].Wait != api.GateWaitMemory {
		t.Errorf("queued events = %+v", q)
	}
	if got, _ := h.s.Summary(t.Context(), sum.ID); got.Gate == nil || got.Gate.Wait != api.GateWaitMemory ||
		got.Waiting != "" {
		t.Errorf("summary gate = %+v waiting = %q", got.Gate, got.Waiting)
	}
	mem.set(5000, 0)
	tk := h.waitStatus(sum.ID, StatusDone)
	if g := tk.TurnN(1).Gate; g == nil || g.State != api.GateGreen {
		t.Fatalf("gate = %+v", g)
	}
}

// TestWatchdogStopsGate: below criticalFreeMemoryMB the watchdog stops the running gate command; the gate is
// queued until the resume level and runs again; the third stop of the phase ends it in error (low_memory). The stop
// count survives a daemon restart.
func TestWatchdogStopsGate(t *testing.T) {
	mem := newFakeMemory(5000)
	lows := &lowLog{}
	h := newHarnessWith(t, func(h *harness) {
		h.res, h.onLow = mem, lows.note
		h.set(memorySettings(1024))
	})
	h.initRepo(map[string]string{"a.txt": "a\n"})
	h.agy("w", 1)
	h.queue.Push(t, editScript("conv-w", filepath.Join(h.cwd, "a.txt"), "a2\n", "Done."))
	sum := h.delegate(api.DelegateRequest{Title: "heavy gate", GateCommands: []string{gateCmd(t, "--wait", "never")}})
	waitLows := func(want ...bool) {
		t.Helper()
		h.waitFor(fmt.Sprintf("low-memory flips %v", want), func() bool { return slices.Equal(lows.get(), want) })
	}
	waitStopped := func(stops int) {
		t.Helper()
		h.waitFor(fmt.Sprintf("stop %d queued for memory", stops), func() bool {
			ph := h.task(sum.ID).GatePhase
			return ph != nil && ph.Stops == stops && ph.State == api.GatePhaseQueued && ph.Wait == api.GateWaitMemory
		})
	}

	h.waitGateStarts(sum.ID, 1)
	mem.set(500, 0)
	waitStopped(1)
	waitLows(true)
	if !h.s.LowMemory() {
		t.Fatal("LowMemory is false below the critical line")
	}
	g := h.task(sum.ID).TurnN(1).Gate
	at := gate.StoppedAt(*g)
	if at < 0 || g.Steps[at].Stopped != api.StepStoppedLowMemory || g.Steps[at].Stops != 1 {
		t.Fatalf("stopped gate = %+v", g)
	}
	// Between the critical line and the resume level the hold stays and nothing is stopped again.
	mem.set(1500, 0)
	mem.set(900, 0)
	time.Sleep(200 * time.Millisecond)
	if !h.s.LowMemory() || len(h.gateEvents(sum.ID, api.GateEventStart)) != 1 || len(lows.get()) != 1 {
		t.Fatalf("hold lifted early: low=%v starts=%d flips=%v", h.s.LowMemory(),
			len(h.gateEvents(sum.ID, api.GateEventStart)), lows.get())
	}

	// A daemon restart keeps the count.
	h.stop()
	h.start()
	waitLows(true, true)
	if ph := h.task(sum.ID).GatePhase; ph == nil || ph.Stops != 1 {
		t.Fatalf("phase after the restart = %+v", ph)
	}

	for stop := 2; stop <= api.GateLowMemoryStopsMax; stop++ {
		mem.set(3000, 0)
		waitLows(append(lows.get()[:len(lows.get())-1:len(lows.get())-1], true, false)...)
		h.waitGateStarts(sum.ID, stop)
		mem.set(500, 0)
		if stop < api.GateLowMemoryStopsMax {
			waitStopped(stop)
		}
	}
	tk := h.waitStatus(sum.ID, StatusDone)
	g = tk.TurnN(1).Gate
	if g == nil || g.State != api.GateError || g.Reason != api.GateReasonLowMemory || tk.GatePhase != nil {
		t.Fatalf("gate = %+v phase = %+v", g, tk.GatePhase)
	}
	if at := gate.StoppedAt(*g); at < 0 || g.Steps[at].Stops != api.GateLowMemoryStopsMax {
		t.Errorf("stopped step = %+v", g.Steps)
	}
	if a := tk.Attention; a == nil || a.Reason != ReasonGateRed || !strings.Contains(a.Detail, "out of memory") {
		t.Errorf("attention = %+v", tk.Attention)
	}
	if n := len(tk.Turns); n != 1 {
		t.Errorf("%d turns: a low-memory gate must not open a fix turn", n)
	}
}

// TestWatchdogOneStopPerEpisode: with two gates running, only the newest is stopped, and nothing else is stopped
// until memory came back to the resume level and fell again.
func TestWatchdogOneStopPerEpisode(t *testing.T) {
	mem := newFakeMemory(5000)
	lows := &lowLog{}
	h := newHarnessWith(t, func(h *harness) {
		h.res, h.onLow = mem, lows.note
		h.set(memorySettings(1024))
	})
	h.initRepo(map[string]string{"a.txt": "a\n", "b.txt": "b\n"})
	h.agy("w", 1)
	h.queue.Push(t, editScript("conv-a", filepath.Join(h.cwd, "a.txt"), "a2\n", "A."))
	older := h.delegate(api.DelegateRequest{Title: "A", GateCommands: []string{gateCmd(t, "--wait", "a.release")}})
	h.waitGateStarts(older.ID, 1)
	h.queue.Push(t, editScript("conv-b", filepath.Join(h.cwd, "b.txt"), "b2\n", "B."))
	newer := h.delegate(api.DelegateRequest{Title: "B", GateCommands: []string{gateCmd(t, "--wait", "b.release")}})
	h.waitGateStarts(newer.ID, 1)

	mem.set(500, 0)
	h.waitFor("the newest gate stopped", func() bool {
		ph := h.task(newer.ID).GatePhase
		return ph != nil && ph.Stops == 1
	})
	mem.set(1500, 0)
	mem.set(800, 0)
	time.Sleep(200 * time.Millisecond)
	if ph := h.task(older.ID).GatePhase; ph == nil || ph.Stops != 0 || ph.State != api.GatePhaseRunning {
		t.Fatalf("the older gate was stopped too: %+v", ph)
	}
	if got := lows.get(); !slices.Equal(got, []bool{true}) {
		t.Fatalf("flips = %v", got)
	}
	mem.set(3000, 0)
	h.waitFor("the hold lifted", func() bool { return slices.Equal(lows.get(), []bool{true, false}) })
	h.waitGateStarts(newer.ID, 2)
	h.write("a.release", "go")
	h.write("b.release", "go")
	h.waitStatus(older.ID, StatusDone)
	tk := h.waitStatus(newer.ID, StatusDone)
	if g := tk.TurnN(1).Gate; g == nil || g.State != api.GateGreen {
		t.Fatalf("B's gate = %+v", g)
	}
	if g := h.task(older.ID).TurnN(1).Gate; g == nil || g.State != api.GateGreen || gate.StoppedAt(*g) >= 0 {
		t.Fatalf("A's gate = %+v", g)
	}
}

// TestLowMemoryHysteresis: LowMemory turns true below the critical line (physical, or commit when known), false
// only at the resume level, and the watchdog stops one gate per episode, the newest.
func TestLowMemoryHysteresis(t *testing.T) {
	mem := newFakeMemory(5000)
	set := config.Defaults()
	set.MinFreeMemoryMB, set.MinFreeCommitMB, set.CriticalFreeMemoryMB = 2048, 4096, 1024
	var flips []bool
	s := &Service{o: Options{Resources: mem, Settings: func() config.Settings { return set },
		OnLowMemory: func(low bool, _ platform.MemoryStatus) { flips = append(flips, low) }},
		runs: map[string]*run{"t": {}}, lowWake: make(chan struct{}), log: slog.New(slog.DiscardHandler)}
	ctxA, cancelA := context.WithCancelCause(t.Context())
	defer cancelA(nil)
	untrackA := s.TrackHeavy(cancelA)
	defer untrackA()
	ctxB, cancelB := context.WithCancelCause(t.Context())
	defer cancelB(nil)
	untrackB := s.TrackHeavy(cancelB)
	stopped := func(ctx context.Context) bool { return errors.Is(context.Cause(ctx), gate.ErrLowMemory) }
	step := func(phys, commit int64, want bool) {
		t.Helper()
		mem.set(phys, commit)
		s.checkMemory()
		if got := s.LowMemory(); got != want {
			t.Fatalf("phys %d commit %d: low = %v, want %v", phys, commit, got, want)
		}
	}
	step(5000, 0, false)
	step(900, 0, true)
	if !stopped(ctxB) || stopped(ctxA) {
		t.Fatalf("the newest gate is stopped, the older one not: A %v, B %v", context.Cause(ctxA), context.Cause(ctxB))
	}
	step(1500, 0, true)
	step(800, 0, true)
	if stopped(ctxA) {
		t.Fatal("a second gate was stopped in the same episode")
	}
	untrackB()
	step(2047, 0, true)
	step(2048, 0, false) // minFreeMemoryMB
	step(1500, 0, false) // above the critical line: no flicker
	step(1000, 0, true)  // a new episode
	if !stopped(ctxA) {
		t.Fatal("the remaining gate was not stopped in the next episode")
	}
	step(3000, 0, false)
	step(5000, 500, true) // commit below the critical line
	step(5000, 4095, true)
	step(5000, 4096, false) // minFreeCommitMB
	step(5000, 500, true)
	mem.mu.Lock()
	mem.m = platform.MemoryStatus{}
	mem.mu.Unlock()
	s.checkMemory()
	if s.LowMemory() {
		t.Fatal("an unknown reading must not hold")
	}
	step(500, 0, true)
	set.CriticalFreeMemoryMB = 0
	step(500, 0, false) // the watchdog is off

	// Without minFree thresholds the resume level is twice the critical line.
	set.MinFreeMemoryMB, set.MinFreeCommitMB, set.CriticalFreeMemoryMB = 0, 0, 1024
	step(1000, 0, true)
	step(2047, 0, true)
	step(2048, 0, false)
	want := []bool{true, false, true, false, true, false, true, false, true, false, true, false}
	if !slices.Equal(flips, want) {
		t.Errorf("flips = %v, want %v", flips, want)
	}
}

func TestSamplerCachesAndSignals(t *testing.T) {
	clk := clock.NewFake(harnessEpoch)
	set := config.Defaults()
	set.MinFreeMemoryMB, set.CriticalFreeMemoryMB = 2048, 1024
	reads := 0
	reading := platform.MemoryStatus{AvailPhysMB: 5000, Known: true}
	s := NewSampler(func() platform.MemoryStatus { reads++; return reading }, clk,
		func() config.Settings { return set })
	closed := func(ch <-chan struct{}) bool {
		select {
		case <-ch:
			return true
		default:
			return false
		}
	}
	s.Sample()
	ch := s.Changed()
	reading.AvailPhysMB = 1000
	if got := s.Sample(); got.AvailPhysMB != 5000 || reads != 1 {
		t.Fatalf("cached reading = %+v after %d reads", got, reads)
	}
	clk.Advance(sampleTTL)
	if got := s.Sample(); got.AvailPhysMB != 1000 || reads != 2 || !closed(ch) {
		t.Fatalf("reading %+v after %d reads, signaled %v", got, reads, closed(ch))
	}
	ch = s.Changed()
	reading.AvailPhysMB = 1010 // no threshold crossed
	clk.Advance(sampleTTL)
	s.Sample()
	if closed(ch) {
		t.Fatal("signaled without a crossing")
	}
	s.Kick()
	if !closed(ch) {
		t.Fatal("Kick did not signal")
	}
	s.Sample()
	if reads != 4 {
		t.Fatalf("Kick kept the cached reading (%d reads)", reads)
	}
}

func TestParallelVars(t *testing.T) {
	if got := ParallelVars([]string{"GOFLAGS=-mod=mod"}, 0); len(got) != 0 {
		t.Fatalf("testParallelism 0 = %v", got)
	}
	got := ParallelVars([]string{"GOFLAGS=-mod=mod", "CARGO_BUILD_JOBS=8"}, 4)
	want := map[string]string{"GOFLAGS": "-mod=mod -p=4", "PYTEST_XDIST_AUTO_NUM_WORKERS": "4"}
	if !mapsEqual(got, want) {
		t.Fatalf("vars = %v, want %v", got, want)
	}
	if got := ParallelVars([]string{"GOFLAGS=-p=2 -mod=mod"}, 4); got["GOFLAGS"] != "" {
		t.Errorf("a GOFLAGS that sets -p must stay as it is: %v", got)
	}
	if got := ParallelVars(nil, 3); !mapsEqual(got, map[string]string{"GOFLAGS": "-p=3", "CARGO_BUILD_JOBS": "3",
		"PYTEST_XDIST_AUTO_NUM_WORKERS": "3"}) {
		t.Errorf("vars = %v", got)
	}
}

func mapsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if w, ok := b[k]; !ok || w != v {
			return false
		}
	}
	return true
}

// TestGateEnvParallelism: gate commands get GOFLAGS with -p=N after the daemon's own flags, once, and keep the
// CARGO_BUILD_JOBS the daemon's environment sets.
func TestGateEnvParallelism(t *testing.T) {
	set := config.Defaults()
	set.TestParallelism = 4
	s := &Service{o: Options{Settings: func() config.Settings { return set },
		BaseEnv: func() []string { return []string{"PATH=/bin", "GOFLAGS=-mod=mod", "CARGO_BUILD_JOBS=8"} }}}
	env := s.gateEnv(t.Context(), &Task{ID: "t1"}, t.TempDir(), nil)
	count := func(name string) (n int, last string) {
		for _, kv := range env {
			if k, v, _ := strings.Cut(kv, "="); k == name {
				n, last = n+1, v
			}
		}
		return n, last
	}
	for name, want := range map[string]string{"GOFLAGS": "-mod=mod -p=4", "CARGO_BUILD_JOBS": "8",
		"PYTEST_XDIST_AUTO_NUM_WORKERS": "4", "CI": "1"} {
		if n, v := count(name); n != 1 || v != want {
			t.Errorf("%s: %d entries, last %q, want one %q (env %v)", name, n, v, want, env)
		}
	}
	set.TestParallelism = 0
	env = s.gateEnv(t.Context(), &Task{ID: "t1"}, t.TempDir(), nil)
	if n, v := count("GOFLAGS"); n != 1 || v != "-mod=mod" {
		t.Errorf("testParallelism 0 changed GOFLAGS: %v", env)
	}
	if n, _ := count("PYTEST_XDIST_AUTO_NUM_WORKERS"); n != 0 {
		t.Errorf("testParallelism 0 set pytest workers: %v", env)
	}
}

// TestWorkerTreeInLaunchJSON: the worker tree limits of the settings reach the attempt's launch.json.
func TestWorkerTreeInLaunchJSON(t *testing.T) {
	h := newHarness(t)
	h.set(func(s *config.Settings) {
		s.WorkerPriority, s.WorkerMemoryLimitMB, s.WorkerCPURatePercent = config.PriorityLow, 4096, 50
	})
	h.agy("w", 1)
	h.queue.Push(t, okScript("conv-t", "Done."))
	sum := h.delegate(api.DelegateRequest{Title: "tree", Mode: "plan"})
	tk := h.waitStatus(sum.ID, StatusDone)
	data, err := os.ReadFile(launch.FilesIn(tk.LastTurn().Attempts[0].LaunchDir).Spec)
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Tree platform.TreeOptions `json:"tree"`
	}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}
	want := platform.TreeOptions{Priority: platform.PriorityLow, MemoryLimitMB: 4096, CPURatePercent: 50}
	if spec.Tree != want {
		t.Fatalf("tree = %+v, want %+v", spec.Tree, want)
	}
	if got := GateTree(h.config()); got != (platform.TreeOptions{Priority: platform.PriorityLow}) {
		t.Errorf("gate tree = %+v", got)
	}
}

func TestMemoryLimitHitFailsTheTurn(t *testing.T) {
	status, text := classify(backend.Outcome{Status: backend.OutcomeFailed, Error: "exit 1"},
		launch.Exit{Reason: launch.ReasonExited, MemoryLimitHit: true}, 30)
	if status != AttemptError || !strings.Contains(text, "memory cap") || !strings.Contains(text, "workerMemoryLimitMB") {
		t.Fatalf("classify = %s %q", status, text)
	}
}

// TestWaitingOnlyForMemory: the summary and the report show Waiting only for a memory wait (lead decision 3).
func TestWaitingOnlyForMemory(t *testing.T) {
	s := &Service{runs: map[string]*run{}}
	for waiting, want := range map[string]string{"busy": "", "resting": "", "no_account": "",
		api.WaitingMemory: api.WaitingMemory} {
		tk := &Task{ID: "t1", Status: StatusWaitingQuota, Waiting: waiting}
		if got := s.summary(tk).Waiting; got != want {
			t.Errorf("summary waiting for %q = %q, want %q", waiting, got, want)
		}
		if got := waitingOf(tk); got != want {
			t.Errorf("report waiting for %q = %q, want %q", waiting, got, want)
		}
	}
	if got := waitingOf(&Task{Status: StatusRunning, Waiting: api.WaitingMemory}); got != "" {
		t.Errorf("a running task shows waiting %q", got)
	}
}

// TestAttentionLeavesResourceWaitsAlone: a short memory wait and a queued gate raise no flag.
func TestAttentionLeavesResourceWaitsAlone(t *testing.T) {
	now := harnessEpoch.Add(time.Hour)
	set := config.Defaults()
	held := &Task{Kind: KindNormal, Status: StatusWaitingQuota, Waiting: api.WaitingMemory,
		MemoryWait: &MemoryWait{Since: now.Add(-ResourcesWaitAfter + time.Second), AvailableMB: 900, NeedMB: 2048},
		Turns:      []*Turn{{N: 1, Status: TurnRunning}}}
	if a := computeAttention(held, liveState{}, accountHealth{}, now, set); a != nil {
		t.Errorf("short memory wait: %+v", a)
	}
	queued := &Task{Kind: KindNormal, Status: StatusRunning, GatePhase: &GatePhase{State: api.GatePhaseQueued,
		Wait: api.GateWaitSlot, Turn: 1, Since: now.Add(-time.Hour)}, Turns: []*Turn{{N: 1, Status: TurnOK}}}
	if a := computeAttention(queued, liveState{}, accountHealth{}, now, set); a != nil {
		t.Errorf("queued gate: %+v", a)
	}
}
