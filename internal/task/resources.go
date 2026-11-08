package task

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/envx"
	"github.com/NovaSeele/Seelegent/internal/gate"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// Resources (M19, docs/task-engine.md "Resources"): the start check holds new worker turns and gate commands while
// free memory or free commit is below minFreeMemoryMB / minFreeCommitMB, and the watchdog stops the newest gate
// command when it falls below criticalFreeMemoryMB. Workers are never stopped. An unknown reading never holds
// anything; a commit of 0 MB is an unknown commit (everywhere but Windows).

// Resource timings.
const (
	// sampleTTL is how long the Sampler keeps a reading.
	sampleTTL = 2 * time.Second
	// memoryRecheck is how often a start held for memory looks again (Resources.Changed wakes it sooner).
	memoryRecheck = 10 * time.Second
	// admitSpacing spaces the starts while free memory is below twice its threshold, so the tasks that waited do
	// not all start at once when memory frees up.
	admitSpacing = 5 * time.Second
	// watchdogTick is how often the watchdog reads memory while tasks run (the Sampler's cache period).
	watchdogTick = 2 * time.Second
	// memoryDrift is how far a held task's reading may move before its record is saved again.
	memoryDrift = 64
)

// Sampler is the Resources of the daemon: platform.Memory cached for sampleTTL, so the start checks, the watchdog
// and GET /health may ask often. Changed is signaled when a new reading lands on the other side of a threshold of
// the settings (or Kick says the thresholds changed). It is safe for concurrent use.
type Sampler struct {
	read     func() platform.MemoryStatus
	clk      clock.Clock
	settings func() config.Settings

	mu   sync.Mutex
	last platform.MemoryStatus
	at   time.Time
	have bool
	wake chan struct{}
}

// NewSampler returns a sampler over read (platform.Memory in the daemon), with the thresholds of settings.
func NewSampler(read func() platform.MemoryStatus, clk clock.Clock, settings func() config.Settings) *Sampler {
	if clk == nil {
		clk = clock.Real()
	}
	return &Sampler{read: read, clk: clk, settings: settings, wake: make(chan struct{})}
}

// Sample returns the cached reading, or a new one when it is older than sampleTTL.
func (s *Sampler) Sample() platform.MemoryStatus {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.clk.Now()
	if s.have && now.Sub(s.at) < sampleTTL && !now.Before(s.at) {
		return s.last
	}
	m := s.read()
	if s.have && crossed(s.settings(), s.last, m) {
		s.signalLocked()
	}
	s.last, s.at, s.have = m, now, true
	return m
}

// Changed returns a channel that is closed at the next threshold crossing or Kick.
func (s *Sampler) Changed() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.wake
}

// Kick wakes the held starts (the thresholds changed) and drops the cached reading.
func (s *Sampler) Kick() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.have = false
	s.signalLocked()
}

func (s *Sampler) signalLocked() {
	close(s.wake)
	s.wake = make(chan struct{})
}

// crossed reports whether reading b is on another side than a of one of the thresholds of set.
func crossed(set config.Settings, a, b platform.MemoryStatus) bool {
	if a.Known != b.Known || commitKnown(a) != commitKnown(b) {
		return true
	}
	crit := int64(set.CriticalFreeMemoryMB)
	levels := []int64{int64(set.MinFreeMemoryMB), int64(set.MinFreeCommitMB), crit, 2 * crit,
		2 * int64(set.MinFreeMemoryMB), 2 * int64(set.MinFreeCommitMB)}
	for _, l := range levels {
		if l <= 0 {
			continue
		}
		if (a.AvailPhysMB < l) != (b.AvailPhysMB < l) {
			return true
		}
		if commitKnown(b) && (a.AvailCommitMB < l) != (b.AvailCommitMB < l) {
			return true
		}
	}
	return false
}

// commitKnown reports whether m has a commit reading (platform: 0 is unknown).
func commitKnown(m platform.MemoryStatus) bool { return m.Known && m.AvailCommitMB > 0 }

// WorkerTree are the limits of a worker tree from the settings (next launch).
func WorkerTree(set config.Settings) platform.TreeOptions {
	return platform.TreeOptions{Priority: platform.Priority(set.WorkerPriority), MemoryLimitMB: set.WorkerMemoryLimitMB,
		CPURatePercent: set.WorkerCPURatePercent}
}

// GateTree are the limits of a gate command, regression run command, automation precheck or hook (gatePriority).
func GateTree(set config.Settings) platform.TreeOptions {
	return platform.TreeOptions{Priority: platform.Priority(set.GatePriority)}
}

// ParallelVars are the variables that carry testParallelism into the environment base (worker turns, gate
// commands, regression runs): GOFLAGS gains -p=N (kept as it is when it already sets -p), CARGO_BUILD_JOBS and
// PYTEST_XDIST_AUTO_NUM_WORKERS are N unless base sets them. Empty when n is 0.
func ParallelVars(base []string, n int) map[string]string {
	out := map[string]string{}
	if n <= 0 {
		return out
	}
	p := strconv.Itoa(n)
	flags, _ := envx.Lookup(base, "GOFLAGS")
	if !slices.ContainsFunc(strings.Fields(flags), func(f string) bool {
		return f == "-p" || strings.HasPrefix(f, "-p=") || f == "--p" || strings.HasPrefix(f, "--p=")
	}) {
		out["GOFLAGS"] = strings.TrimSpace(flags + " -p=" + p)
	}
	for _, name := range []string{"CARGO_BUILD_JOBS", "PYTEST_XDIST_AUTO_NUM_WORKERS"} {
		if _, ok := envx.Lookup(base, name); !ok {
			out[name] = p
		}
	}
	return out
}

// memHold is why a start waits for memory: the reading avail is below need (of commit when commit is set).
type memHold struct {
	avail, need int64
	commit      bool
}

// detail is the hold in English (WaitDetail).
func (h memHold) detail() string {
	what := "memory"
	if h.commit {
		what = "commit"
	}
	return fmt.Sprintf("free %s %d MB is below the %d MB threshold: new work waits", what, h.avail, h.need)
}

// resumeLevels are the readings at which the watchdog stops holding (lead decision 2): minFreeMemoryMB and
// minFreeCommitMB when they are on, else twice criticalFreeMemoryMB.
func resumeLevels(set config.Settings) (phys, commit int64) {
	crit := int64(set.CriticalFreeMemoryMB)
	phys, commit = 2*crit, 2*crit
	if set.MinFreeMemoryMB > 0 {
		phys = int64(set.MinFreeMemoryMB)
	}
	if set.MinFreeCommitMB > 0 {
		commit = int64(set.MinFreeCommitMB)
	}
	return phys, commit
}

// critical reports whether m is below criticalFreeMemoryMB (physical, or commit when it is known).
func critical(set config.Settings, m platform.MemoryStatus) bool {
	crit := int64(set.CriticalFreeMemoryMB)
	return crit > 0 && m.Known && (m.AvailPhysMB < crit || (commitKnown(m) && m.AvailCommitMB < crit))
}

// resumed reports whether m is back at the resume levels.
func resumed(set config.Settings, m platform.MemoryStatus) bool {
	phys, commit := resumeLevels(set)
	return m.AvailPhysMB >= phys && (!commitKnown(m) || m.AvailCommitMB >= commit)
}

// memoryHold returns why a new start (worker turn or gate run) must wait now, or nil.
func (s *Service) memoryHold(set config.Settings) *memHold {
	if s.o.Resources == nil {
		return nil
	}
	m := s.o.Resources.Sample()
	if !m.Known {
		return nil
	}
	if s.LowMemory() {
		// The watchdog holds everything until the resume level; a reading already there passes (the watchdog lifts
		// the flag at its next look).
		phys, commit := resumeLevels(set)
		if m.AvailPhysMB < phys {
			return &memHold{avail: m.AvailPhysMB, need: phys}
		}
		if commitKnown(m) && m.AvailCommitMB < commit {
			return &memHold{avail: m.AvailCommitMB, need: commit, commit: true}
		}
	}
	if n := int64(set.MinFreeMemoryMB); n > 0 && m.AvailPhysMB < n {
		return &memHold{avail: m.AvailPhysMB, need: n}
	}
	if n := int64(set.MinFreeCommitMB); n > 0 && commitKnown(m) && m.AvailCommitMB < n {
		return &memHold{avail: m.AvailCommitMB, need: n, commit: true}
	}
	return nil
}

// admitDelay spaces worker starts while free memory is below twice its threshold: it returns 0 and counts the
// start when one may go now, else how long to wait before trying again.
func (s *Service) admitDelay(set config.Settings) time.Duration {
	if s.o.Resources == nil {
		return 0
	}
	m := s.o.Resources.Sample()
	near := m.Known && ((set.MinFreeMemoryMB > 0 && m.AvailPhysMB < 2*int64(set.MinFreeMemoryMB)) ||
		(set.MinFreeCommitMB > 0 && commitKnown(m) && m.AvailCommitMB < 2*int64(set.MinFreeCommitMB)))
	if !near {
		return 0
	}
	now := s.clk.Now()
	s.resMu.Lock()
	defer s.resMu.Unlock()
	if !s.lastAdmit.IsZero() {
		if next := s.lastAdmit.Add(admitSpacing); now.Before(next) {
			return next.Sub(now)
		}
	}
	s.lastAdmit = now
	return 0
}

// memWake are the signals a start held for memory wakes on: a threshold crossing and the watchdog's flips.
type memWake struct{ res, low <-chan struct{} }

// memoryWake returns the wake signals; take them before reading, so a change in between is not missed.
func (s *Service) memoryWake() memWake {
	var w memWake
	if s.o.Resources != nil {
		w.res = s.o.Resources.Changed()
	}
	s.resMu.Lock()
	w.low = s.lowWake
	s.resMu.Unlock()
	return w
}

// waitMemory waits memoryRecheck, until a wake signal, or until ctx ends.
func (s *Service) waitMemory(ctx context.Context, w memWake) {
	t := s.clk.NewTimer(memoryRecheck)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C():
	case <-w.res:
	case <-w.low:
	}
}

// holdForMemory is the start check of a worker turn before it asks the pool for an account: while memory is low
// it shows the task waiting for memory and waits, without touching the pool, until memory is back or the run
// stops. It reports whether it waited (the caller looks at the task again).
func (s *Service) holdForMemory(r *run, id string) bool {
	if s.o.Resources == nil {
		return false
	}
	waited := false
	for r.ctx.Err() == nil {
		wake := s.memoryWake()
		h := s.memoryHold(s.settings())
		if h == nil {
			break
		}
		waited = true
		s.setMemoryWaiting(id, *h)
		s.waitMemory(r.ctx, wake)
	}
	return waited
}

// holdStart is the start check of a worker turn after its lease (release gives the lease back), the guard against
// memory that fell while the turn waited for an account: while memory is low it releases the lease, shows the task
// waiting for memory and waits; while memory is near its threshold it spaces the starts. It reports whether the
// start was held (the caller tries the attempt again).
func (s *Service) holdStart(r *run, id string, release func()) bool {
	if s.o.Resources == nil {
		return false
	}
	set := s.settings()
	wake := s.memoryWake()
	if h := s.memoryHold(set); h != nil {
		release()
		s.setMemoryWaiting(id, *h)
		s.waitMemory(r.ctx, wake)
		return true
	}
	if d := s.admitDelay(set); d > 0 {
		release()
		s.sleep(r.ctx, d, nil)
		return true
	}
	return false
}

// MemoryWait is how long a task has waited for memory and what the last reading found (resources_wait).
type MemoryWait struct {
	Since       time.Time `json:"since"`
	AvailableMB int64     `json:"availableMB"`
	NeedMB      int64     `json:"needMB"`
}

// setMemoryWaiting shows a task as waiting for memory (status waiting_quota, Waiting memory). The waiting_quota
// event is logged when the wait starts and when its threshold changes; the reading is saved again only when it
// moved by memoryDrift. A memory wait that a pool wait interrupted keeps its start and logs no new event
// (MemoryWait survives the pool wait; only a launch or the end of the run clears it), so resources_wait counts the
// whole time.
func (s *Service) setMemoryWaiting(id string, h memHold) {
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		if t.CancelRequested || t.Manual || !Active(t.Status) {
			return nil, errNoChange
		}
		mw := t.MemoryWait
		waiting := t.Status == StatusWaitingQuota && t.Waiting == api.WaitingMemory && mw != nil
		if waiting && mw.NeedMB == h.need && abs64(mw.AvailableMB-h.avail) < memoryDrift && t.WaitDetail == h.detail() {
			return nil, errNoChange
		}
		if t.Status != StatusWaitingQuota {
			if err := t.SetStatus(StatusWaitingQuota); err != nil {
				return nil, err
			}
		}
		since := s.now()
		if mw != nil {
			since = mw.Since
		}
		t.Waiting, t.WaitDetail, t.WaitingUntil = api.WaitingMemory, h.detail(), time.Time{}
		t.MemoryWait = &MemoryWait{Since: since, AvailableMB: h.avail, NeedMB: h.need}
		if mw != nil && mw.NeedMB == h.need {
			return nil, nil
		}
		turn := 0
		if tu := t.LastTurn(); tu != nil {
			turn = tu.N
		}
		return []NewEvent{{Type: api.EventWaitingQuota, Turn: turn, Data: api.WaitingQuotaData{
			Reason: api.WaitingMemory, AvailableMB: h.avail, NeedMB: h.need}}}, nil
	})
	if err != nil && s.root.Err() == nil {
		s.log.Warn("task: set waiting for memory", "task", id, "err", err)
	}
}

func abs64(n int64) int64 {
	if n < 0 {
		return -n
	}
	return n
}

// waitingOf is TaskSummary.Waiting and TaskReport.Waiting: only the memory wait is shown (lead decision 3); the
// pool's reasons stay internal.
func waitingOf(t *Task) string {
	if t.Status == StatusWaitingQuota && t.Waiting == api.WaitingMemory {
		return api.WaitingMemory
	}
	return ""
}

// LowMemory reports whether the watchdog holds every new start (Health.LowMemory).
func (s *Service) LowMemory() bool {
	s.resMu.Lock()
	defer s.resMu.Unlock()
	return s.low
}

// gateRun is a gate run in flight, which the watchdog may stop.
type gateRun struct{ cancel context.CancelCauseFunc }

// TrackHeavy records a heavy command in flight, a gate run or a regression run command (newest last), which the
// memory watchdog may stop by calling cancel with gate.ErrLowMemory. It returns its untrack.
func (s *Service) TrackHeavy(cancel context.CancelCauseFunc) func() {
	g := &gateRun{cancel: cancel}
	s.resMu.Lock()
	s.gateRuns = append(s.gateRuns, g)
	s.resMu.Unlock()
	s.poke()
	return func() {
		s.resMu.Lock()
		s.gateRuns = slices.DeleteFunc(s.gateRuns, func(x *gateRun) bool { return x == g })
		s.resMu.Unlock()
	}
}

// poke wakes an idle watchdog: a run or a heavy command started.
func (s *Service) poke() {
	s.resMu.Lock()
	if s.activity != nil {
		close(s.activity)
	}
	s.activity = make(chan struct{})
	s.resMu.Unlock()
}

// idle reports whether the watchdog has nothing to watch: no run, no heavy command in flight and no hold. It
// returns the activity signal taken before it looked.
func (s *Service) idle() (idle bool, activity <-chan struct{}) {
	s.resMu.Lock()
	act, busy := s.activity, len(s.gateRuns) > 0 || s.low
	s.resMu.Unlock()
	if busy {
		return false, act
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.runs) == 0, act
}

// watchMemory is the low-memory watchdog: it reads memory every watchdogTick while a run or a heavy command is in
// flight or the hold is on (and at every threshold crossing), and applies checkMemory. Idle, it arms no timer and
// sleeps until a run or a heavy command starts.
func (s *Service) watchMemory() {
	for {
		if idle, act := s.idle(); idle {
			select {
			case <-s.root.Done():
				return
			case <-act:
			}
			continue
		}
		wake := s.o.Resources.Changed()
		s.checkMemory()
		t := s.clk.NewTimer(watchdogTick)
		select {
		case <-s.root.Done():
			t.Stop()
			return
		case <-t.C():
		case <-wake:
			t.Stop()
		}
	}
}

// checkMemory applies the watchdog rules to a fresh reading (lead decisions 1 and 2):
//
//   - Below criticalFreeMemoryMB (physical, or commit when known), LowMemory turns true: every new start waits
//     for the resume level, and the newest heavy command in flight (gate run or regression run command) is
//     stopped with gate.ErrLowMemory.
//   - One stop per episode: no other gate is stopped until LowMemory has turned false (the resume level) and
//     memory fell below the critical line again. A gate admitted just before the flag turned is stopped too, as
//     long as nothing was stopped yet in that episode.
//   - LowMemory turns false only at the resume level, so it does not flicker between the two lines.
//   - criticalFreeMemoryMB 0 (off) and an unknown reading turn it false.
//
// With nothing running and no hold, the watchdog reads nothing at all.
func (s *Service) checkMemory() {
	if idle, _ := s.idle(); idle {
		return
	}
	set := s.settings()
	var m platform.MemoryStatus
	if set.CriticalFreeMemoryMB > 0 {
		m = s.o.Resources.Sample()
	}
	s.resMu.Lock()
	flipped := false
	switch {
	case set.CriticalFreeMemoryMB <= 0 || !m.Known:
		flipped = s.low
		s.low = false
	case !s.low && critical(set, m):
		s.low, s.stopped, flipped = true, false, true
	case s.low && resumed(set, m):
		s.low, flipped = false, true
	}
	var victim *gateRun
	if s.low && !s.stopped && critical(set, m) && len(s.gateRuns) > 0 {
		victim, s.stopped = s.gateRuns[len(s.gateRuns)-1], true
	}
	low := s.low
	if flipped {
		close(s.lowWake)
		s.lowWake = make(chan struct{})
	}
	s.resMu.Unlock()
	if victim != nil {
		s.log.Warn("task: machine low on memory: stopping the newest gate or regression command", "availPhysMB", m.AvailPhysMB,
			"availCommitMB", m.AvailCommitMB, "criticalMB", set.CriticalFreeMemoryMB)
		victim.cancel(gate.ErrLowMemory)
	}
	if flipped {
		s.log.Info("task: low-memory hold", "on", low, "availPhysMB", m.AvailPhysMB, "availCommitMB", m.AvailCommitMB)
		if s.o.OnLowMemory != nil {
			s.o.OnLowMemory(low, m)
		}
	}
}
