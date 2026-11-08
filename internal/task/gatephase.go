package task

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/gate"
	"github.com/NovaSeele/Seelegent/internal/proof"
)

// The gate phase (docs/gates.md): when a turn of a gated task ends well, the task stays running with a GatePhase
// while its run goroutine runs the gate. A red gate starts an automatic fix turn (by gate) while the budget lasts;
// a green one may go to the verifier (verifier.go). The phase survives a daemon restart: the next daemon runs the
// gate again.

// Gate phase limits.
const (
	// turnFilesEnvMax is the longest SEELEGENT_TURN_FILES value given to gate commands.
	turnFilesEnvMax = 16000
	// gateHeadMax caps the HEAD version read for the JSON check.
	gateHeadMax = 4 << 20
)

// wantsGate reports whether turn tu of t, which just ended well, gets a gate phase.
func wantsGate(t *Task, tu *Turn) bool {
	return gated(t) && t.Proof != nil && tu.Status == TurnOK && !tu.Handback
}

// instructionStart returns the index of the turn that holds the lead's (or user's) latest instruction at or before
// turn n: the gate covers that turn and the fix turns after it.
func instructionStart(t *Task, n int) int {
	start := 0
	for i, tu := range t.Turns {
		if tu.N > n {
			break
		}
		if tu.By == ByLead || tu.By == ByUser || tu.By == "" {
			start = i
		}
	}
	return start
}

// gateInput collects the changed files (relative to the repository root) of the turns since the instruction up to
// turn n, and the red hygiene findings still open: a finding of an earlier turn stays until a later turn changes
// its file again.
func gateInput(t *Task, n int) (files []string, hygiene []api.Finding, checked bool) {
	open := map[string]api.Finding{}
	var order []string
	seen := map[string]bool{}
	for _, tu := range t.Turns[instructionStart(t, n):] {
		if tu.N > n {
			break
		}
		changed := tu.Changed
		if tu.Snap == nil || !tu.Snap.Checked {
			changed = turnFiles(t, tu, liveState{})
		} else {
			checked = true
		}
		for _, f := range changed {
			delete(open, f)
			if !seen[f] {
				seen[f] = true
				files = append(files, f)
			}
		}
		for _, h := range tu.Hygiene {
			if h.Severity == api.SeverityRed {
				if _, ok := open[h.Path]; !ok {
					order = append(order, h.Path)
				}
				open[h.Path] = h
			}
		}
	}
	for _, p := range order {
		if h, ok := open[p]; ok {
			hygiene = append(hygiene, h)
			delete(open, p)
		}
	}
	return files, hygiene, checked
}

// others returns the other active tasks of t's repository, with their files and lanes (for the fix prompt and
// foreign failures).
func (s *Service) others(ctx context.Context, t *Task) []gate.Other {
	if t.RepoRoot == "" {
		return nil
	}
	tasks, err := s.repoTasks(ctx, t.RepoRoot)
	if err != nil {
		s.log.Warn("task: other tasks of the repository", "task", t.ID, "err", err)
		return nil
	}
	now := s.clk.Now()
	var out []gate.Other
	for _, o := range tasks {
		if o.ID == t.ID || related(t, o) || !Active(o.Status) {
			continue
		}
		g := gate.Other{ID: o.ID, Title: o.Title}
		if o.Proof != nil && o.Proof.Lane != nil {
			g.Lane = o.Proof.Lane.Paths
		}
		for _, f := range s.touched(o, now) {
			g.Files = append(g.Files, f.path)
		}
		out = append(out, g)
	}
	return out
}

// othersWorking reports whether another task of t's repository has a worker (or a takeover console) at work.
func (s *Service) othersWorking(ctx context.Context, t *Task) bool {
	if t.RepoRoot == "" {
		return false
	}
	s.forgetOverlaps()
	tasks, err := s.repoTasks(ctx, t.RepoRoot)
	if err != nil {
		return false
	}
	for _, o := range tasks {
		// Only a worker at work changes files: a queued task or one in its own gate phase does not.
		if o.ID != t.ID && !related(t, o) && o.Status == StatusRunning && o.GatePhase == nil {
			return true
		}
	}
	return false
}

// gateEnv is the environment of gate commands: the daemon's, plus CI=1 and what the gate is about. The gate's own
// variables are never inherited: a SEELEGENT_TURN_FILES from the daemon's environment would name other files when
// this list is too long to pass (as the old version did). Commands run in root get the same Rust toolchain pin as
// the worker (Toolchains): only set when the daemon's environment does not pin one itself.
//
// testParallelism adds GOFLAGS=-p=N (after the GOFLAGS the daemon has), CARGO_BUILD_JOBS=N and
// PYTEST_XDIST_AUTO_NUM_WORKERS=N (unless the daemon's environment sets them): ParallelVars.
func (s *Service) gateEnv(ctx context.Context, t *Task, root string, files []string) []string {
	base := s.o.BaseEnv()
	extra := ParallelVars(base, s.limits().TestParallelism)
	maps.Copy(extra, s.toolEnv(ctx, root))
	own := []string{"CI", "SEELEGENT_GATE", api.EnvGateTask, "SEELEGENT_TURN_FILES"}
	for k := range extra {
		own = append(own, k)
	}
	var env []string
	for _, kv := range base {
		name, _, _ := strings.Cut(kv, "=")
		if !slices.ContainsFunc(own, func(n string) bool { return strings.EqualFold(n, name) }) {
			env = append(env, kv)
		}
	}
	for _, k := range slices.Sorted(maps.Keys(extra)) {
		env = append(env, k+"="+extra[k])
	}
	env = append(env, "CI=1", "SEELEGENT_GATE=1", api.EnvGateTask+"="+t.ID)
	if v := strings.Join(files, "\n"); len(v) <= turnFilesEnvMax {
		env = append(env, "SEELEGENT_TURN_FILES="+v)
	}
	return env
}

// gatePhase runs the gate phase of t. It reports whether the run goes on (a fix turn, the verifier).
func (s *Service) gatePhase(r *run, t *Task) (bool, error) {
	ph := t.GatePhase
	if ph.State == api.GatePhaseVerifying {
		return s.awaitVerifier(r, t)
	}
	s.lateCheck(r.ctx, t.ID, ph.Turn)
	t, err := s.repo.Get(r.ctx, t.ID)
	if err != nil {
		return false, err
	}
	tu := t.TurnN(ph.Turn)
	if tu == nil || t.Proof == nil {
		return false, s.endGate(t.ID, ph.Turn, nil, nil)
	}
	set := s.settings()
	files, hygiene, checked := gateInput(t, ph.Turn)
	p := t.Proof
	laneMode := set.Lanes
	var lane []string
	if p.Lane != nil {
		lane = p.Lane.Paths
	}
	in := gate.Input{Files: files, LaneMode: laneMode, Lane: lane, Hygiene: hygiene,
		Hygienic: set.Hygiene && checked, Gates: set.Gates, Disabled: p.GateOff, AutoChecks: set.GateAutoChecks,
		Doctrine: p.Doctrine, DoctrineError: p.DoctrineError, Commands: p.Commands}
	if laneMode == config.LanesOff {
		in.Lane = nil
	}
	steps := gate.Plan(in)
	if len(steps) == 0 {
		return s.afterGreen(t, ph.Turn, nil, files)
	}
	root := claimRoot(t)
	runner := gate.Runner{Root: root, Env: s.gateEnv(r.ctx, t, root, files),
		Timeout:   time.Duration(max(set.GateTimeoutSec, 1)) * time.Second,
		TailBytes: max(set.GateOutputTailBytes, 1),
		LogPrefix: filepath.Join(s.o.TasksDir, t.ID, fmt.Sprintf("gate-t%d-", ph.Turn)),
		Checker:   gate.Checker{Env: s.o.BaseEnv(), Head: s.headBlob(t)},
		Tree:      GateTree(set)}
	// A gate the watchdog kept stopping ends at once: no settling, no fix turn (the worker cannot fix it).
	outOfMemory := func(res api.GateResult) (bool, error) {
		res.EndedAt = api.FormatTime(s.now())
		return false, s.endGate(t.ID, ph.Turn, &res, s.barChanges(t))
	}
	res, ok := s.runGate(r, t.ID, ph.Turn, runner, steps)
	if !ok {
		return s.gateStopped(t.ID)
	}
	if res.Reason == api.GateReasonLowMemory {
		return outOfMemory(res)
	}
	if failedGate(res) && s.othersWorking(r.ctx, t) {
		// Another worker may be half way through a change the gate trips over: wait for it, then run again.
		if !s.settle(r, t) {
			return s.gateStopped(t.ID)
		}
		if res, ok = s.runGate(r, t.ID, ph.Turn, runner, steps); !ok {
			return s.gateStopped(t.ID)
		}
		if res.Reason == api.GateReasonLowMemory {
			return outOfMemory(res)
		}
		res.Settled = true
	}
	// Findings an earlier fix turn could not clear stop the gate before its other steps: run those too, so what
	// comes next depends on whether anything else fails.
	stuck := stuckStyle(t, ph.Turn, res)
	if p.AutoFixes < p.Budget && onlyFailures(res, stuck) && len(res.NotRun) > 0 {
		rest, ok := s.runGate(r, t.ID, ph.Turn, runner, pastHygiene(steps))
		if !ok {
			return s.gateStopped(t.ID)
		}
		if rest.Reason == api.GateReasonLowMemory {
			return outOfMemory(rest)
		}
		res = withHygieneOf(rest, res)
	}
	others := s.others(r.ctx, t)
	for i := range res.Steps {
		st := &res.Steps[i]
		// The step says it itself (causedBy): the views, the report and the attention detail name the tasks.
		if by := gate.ForeignCause(*st, files, others); len(by) > 0 {
			st.Foreign, st.CausedBy = true, by
		}
	}
	// A foreign step keeps the gate red: the lead sees it and decides.
	res.State = gate.Overall(res.Steps)
	res.EndedAt = api.FormatTime(s.now())
	s.recordLedger(t, tu, res)
	barChanged := s.barChanges(t)
	if !failedGate(res) {
		return s.afterGreen(t, ph.Turn, &res, files, barChanged...)
	}
	// Only the task's own failures are worth a fix turn.
	if p.AutoFixes < p.Budget && gate.Failing(res) != nil {
		if !onlyFailures(res, stuck) {
			// Other failures are worth another turn; the repeated findings are left out of its prompt and stay red.
			msg := gate.FixPrompt(withoutFindings(res, stuck), p.AutoFixes+1, p.Budget, others)
			return s.fixTurn(t.ID, ph.Turn, res, barChanged, msg, ByGate)
		}
		res.Warnings = append(res.Warnings, "the automatic fix turn left the same line ending and BOM findings: "+
			"no more fix turns, the lead decides")
	}
	return false, s.endGate(t.ID, ph.Turn, &res, barChanged)
}

// stuckStyle returns the line ending and BOM findings of res when turn n is an automatic fix turn whose gate still
// has exactly those (same files, same kinds) of the gate that started it, or nil: another fix turn would not clear
// them either (M18: two fix turns could not, one worker even converted a file to CRLF trying).
func stuckStyle(t *Task, n int, res api.GateResult) map[[2]string]bool {
	now := styleFindings(res)
	if len(now) == 0 {
		return nil
	}
	for _, tu := range t.Turns {
		if tu.Gate != nil && tu.Gate.AutoFix == n {
			if maps.Equal(styleFindings(*tu.Gate), now) {
				return now
			}
			return nil
		}
	}
	return nil
}

// onlyFailures reports whether the findings of stuck (not empty) are all the task's own failures in res: every
// other own step passed and the hygiene step has no other red finding.
func onlyFailures(res api.GateResult, stuck map[[2]string]bool) bool {
	if len(stuck) == 0 {
		return false
	}
	for i := range res.Steps {
		st := &res.Steps[i]
		if !stepFailed(st.State) || st.Foreign {
			continue
		}
		if st.Kind != api.StepHygiene {
			return false
		}
		for _, f := range st.Findings {
			if f.Severity == api.SeverityRed && !stuck[[2]string{f.Kind, f.Path}] {
				return false
			}
		}
	}
	return true
}

// withoutFindings returns a copy of res for a fix prompt without the findings of stuck; a hygiene step left with no
// red finding counts as passed there, so the prompt works on the next failure.
func withoutFindings(res api.GateResult, stuck map[[2]string]bool) api.GateResult {
	if len(stuck) == 0 {
		return res
	}
	res.Steps = slices.Clone(res.Steps)
	for i := range res.Steps {
		st := &res.Steps[i]
		if st.Kind != api.StepHygiene {
			continue
		}
		st.Findings = slices.DeleteFunc(slices.Clone(st.Findings), func(f api.Finding) bool {
			return stuck[[2]string{f.Kind, f.Path}]
		})
		if redFindings(st.Findings) == 0 && stepFailed(st.State) {
			st.State = api.StepGreen
		}
	}
	return res
}

// stepFailed reports a red or error step.
func stepFailed(state string) bool { return state == api.StepRed || state == api.StepError }

// styleFindings returns the line ending and BOM findings of a gate's hygiene steps, as kind and path.
func styleFindings(res api.GateResult) map[[2]string]bool {
	out := map[[2]string]bool{}
	for i := range res.Steps {
		if res.Steps[i].Kind != api.StepHygiene {
			continue
		}
		for _, f := range res.Steps[i].Findings {
			switch f.Kind {
			case api.FindingEOLChanged, api.FindingBOMChanged, api.FindingBOMNewFile:
				out[[2]string{f.Kind, f.Path}] = true
			}
		}
	}
	return out
}

func failedGate(res api.GateResult) bool {
	return res.State == api.GateRed || res.State == api.GateError
}

func laneStep(res api.GateResult) *api.GateStep {
	for i := range res.Steps {
		if res.Steps[i].Kind == api.StepLane {
			return &res.Steps[i]
		}
	}
	return nil
}

// headBlob reads the HEAD version of a repository file for the JSON check.
func (s *Service) headBlob(t *Task) func(ctx context.Context, rel string) ([]byte, bool) {
	if s.o.Repos == nil || t.RepoRoot == "" {
		return nil
	}
	return func(ctx context.Context, rel string) ([]byte, bool) {
		data, ok, err := s.o.Repos.Blob(ctx, t.RepoRoot, "HEAD", rel, gateHeadMax, false)
		return data, ok && err == nil
	}
}

// runGate runs the steps once, recording the progress on the turn and in gate events. ok is false when the run
// was cut short (cancel, takeover, shutdown).
//
// Before it runs, the gate takes a gate slot (gateConcurrency) and then checks memory; while it waits for either
// the phase is queued (gateAdmit). A run the memory watchdog stopped (gate.StoppedAt) is counted on the phase and
// run again as a whole once memory is back; after api.GateLowMemoryStopsMax stops the result is an error with
// api.GateReasonLowMemory, which the phase ends with.
func (s *Service) runGate(r *run, id string, turn int, runner gate.Runner, steps []api.GateStep) (api.GateResult, bool) {
	for {
		release, ok := s.gateAdmit(r, id, turn)
		if !ok {
			return api.GateResult{}, false
		}
		res, ok := s.runGateOnce(r, id, turn, runner, steps)
		release()
		at := gate.StoppedAt(res)
		if !ok || at < 0 {
			return res, ok
		}
		stops := s.countGateStop(id, turn, &res, at)
		if stops >= api.GateLowMemoryStopsMax {
			res.State, res.Reason = api.GateError, api.GateReasonLowMemory
			res.Warnings = append(res.Warnings, fmt.Sprintf("the machine kept running out of memory: the gate was "+
				"stopped %d times", stops))
			return res, true
		}
		if r.ctx.Err() != nil {
			return res, false
		}
	}
}

// gateAdmit waits for a gate slot, then for memory, showing the phase queued meanwhile. A gate never holds its slot
// while it waits for memory: it gives the slot back and queues again. It returns the slot's release, or false
// when the run was stopped meanwhile.
func (s *Service) gateAdmit(r *run, id string, turn int) (func(), bool) {
	release, err := s.admitHeavy(r.ctx, true, func(pos, limit int) {
		s.gateQueued(id, turn, api.GateWaitSlot, pos, limit)
	}, func() { s.gateQueued(id, turn, api.GateWaitMemory, 0, 0) })
	if err != nil {
		return nil, false
	}
	s.holdSlot(id, 1)
	return func() {
		s.holdSlot(id, -1)
		release()
	}, true
}

// holdSlot counts a gate slot taken (+1) or given back (-1) by the gate of task id (HoldsGateSlot).
func (s *Service) holdSlot(id string, delta int) {
	s.resMu.Lock()
	defer s.resMu.Unlock()
	if s.slotHolders == nil {
		s.slotHolders = map[string]int{}
	}
	if s.slotHolders[id] += delta; s.slotHolders[id] <= 0 {
		delete(s.slotHolders, id)
	}
}

// HoldsGateSlot reports whether the gate of task id holds a gate slot now.
func (s *Service) HoldsGateSlot(id string) bool {
	s.resMu.Lock()
	defer s.resMu.Unlock()
	return s.slotHolders[id] > 0
}

// AdmitHeavy admits a heavy command outside a task gate (a regression run command): it waits for a gate slot
// (gateConcurrency, in order with the task gates), then for memory like a gate, giving the slot back while it waits
// for memory. It returns the slot's release, or ctx's error when ctx ended first.
//
// gateTask is the task whose gate command asked for the run (SEELEGENT_TASK in a gate's environment; "" from
// anywhere else). While that task's gate holds a slot the command shares it: it takes no second slot, so a gate
// command that runs a regression through the daemon cannot wait for a slot its own gate holds (with
// gateConcurrency N, N such gates would wait for each other until gateTimeoutSec). It still waits for memory. An
// id the daemon does not know, or a gate that holds no slot, waits for a slot as usual.
func (s *Service) AdmitHeavy(ctx context.Context, gateTask string) (func(), error) {
	return s.admitHeavy(ctx, gateTask == "" || !s.HoldsGateSlot(gateTask), nil, nil)
}

// admitHeavy is AdmitHeavy with the queue reports: onSlot with the place in line while it waits for a slot,
// onMemory when it waits for memory (each may be nil). Without slot it waits for memory only.
func (s *Service) admitHeavy(ctx context.Context, slot bool, onSlot func(pos, limit int), onMemory func()) (func(),
	error) {
	for {
		release := func() {}
		if sl := s.o.GateSlots; sl != nil && slot {
			var onWait func(int)
			if onSlot != nil {
				onWait = func(pos int) { onSlot(pos, sl.Limit()) }
			}
			rel, err := sl.Acquire(ctx, onWait)
			if rel != nil {
				release = rel
			}
			if err == nil {
				err = ctx.Err()
			}
			if err != nil {
				release()
				return nil, err
			}
		}
		wake := s.memoryWake()
		if s.memoryHold(s.limits()) == nil {
			return release, nil
		}
		release()
		if onMemory != nil {
			onMemory()
		}
		s.waitMemory(ctx, wake)
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

// queueNear is the slot queue position from which every move of a queued gate is logged.
const queueNear = 3

// queueMoveLogged reports whether a queued gate that moved in the slot queue from position was to pos logs a new
// queued event: near the front (queueNear or less) every move, further back only when the position halved. With k
// gates queued, each slot release would otherwise log k-1 events (k² over the queue); this keeps it to about
// log2(k)+queueNear per gate. GatePhase.Position is the position last logged.
func queueMoveLogged(was, pos int) bool {
	return pos != was && (pos <= queueNear || pos <= was/2)
}

// gateQueued shows the gate phase of turn queued, waiting for wait, with a queued gate event: when the wait starts,
// when what it waits for changes (slot, memory), and when its slot queue position moved enough (queueMoveLogged).
// A smaller move changes nothing stored.
func (s *Service) gateQueued(id string, turn int, wait string, pos, limit int) {
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		ph := t.GatePhase
		if ph == nil || ph.Turn != turn || t.CancelRequested {
			return nil, errNoChange
		}
		if ph.State == api.GatePhaseQueued && ph.Wait == wait &&
			(wait == api.GateWaitMemory || !queueMoveLogged(ph.Position, pos)) {
			return nil, errNoChange
		}
		ph.State, ph.Wait, ph.Position = api.GatePhaseQueued, wait, pos
		return []NewEvent{{Type: api.EventGate, Turn: turn, Data: api.GateEventData{Phase: api.GateEventQueued,
			Wait: wait, Position: pos, Limit: limit}}}, nil
	})
	if err != nil && s.root.Err() == nil {
		s.log.Warn("task: queue the gate", "task", id, "err", err)
	}
}

// countGateStop counts a watchdog stop of the gate phase of turn (GatePhase.Stops survives a restart), copies the
// count onto the stopped step at and stores the stopped run on the turn. It returns the count.
func (s *Service) countGateStop(id string, turn int, res *api.GateResult, at int) int {
	stops := 0
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		ph := t.GatePhase
		if ph == nil || ph.Turn != turn {
			return nil, errNoChange
		}
		ph.Stops++
		stops = ph.Stops
		res.Steps[at].Stops = stops
		if tu := t.TurnN(turn); tu != nil {
			cp := *res
			cp.Steps = slices.Clone(res.Steps)
			tu.Gate = &cp
		}
		st := res.Steps[at]
		return []NewEvent{{Type: api.EventGate, Turn: turn, Data: api.GateEventData{Phase: api.GateEventStep,
			Index: at, Step: &st}}}, nil
	})
	if err != nil && s.root.Err() == nil {
		s.log.Warn("task: count the gate's memory stop", "task", id, "err", err)
	}
	if stops == 0 {
		// The phase is gone (cancelled meanwhile): the caller stops anyway.
		stops = 1
	}
	return stops
}

// runGateOnce runs the steps once in a context the memory watchdog may stop (gate.ErrLowMemory).
func (s *Service) runGateOnce(r *run, id string, turn int, runner gate.Runner, steps []api.GateStep) (
	api.GateResult, bool) {
	ctx, cancel := context.WithCancelCause(r.ctx)
	defer cancel(nil)
	untrack := s.TrackHeavy(cancel)
	defer untrack()
	started := api.FormatTime(s.now())
	labels := make([]string, len(steps))
	plan := make([]api.GatePlanStep, len(steps))
	for i := range steps {
		st := &steps[i]
		labels[i] = st.Label
		plan[i] = api.GatePlanStep{Kind: st.Kind, Label: st.Label, Cmd: st.Cmd, Dir: st.Dir,
			TaskIDs: slices.Clone(st.TaskIDs)}
	}
	live := api.GateResult{State: api.GateRunning, Steps: slices.Clone(steps), NotRun: []string{}, Warnings: []string{},
		StartedAt: started}
	s.saveGate(id, turn, live, NewEvent{Type: api.EventGate, Turn: turn,
		Data: api.GateEventData{Phase: api.GateEventStart, Steps: labels, Plan: plan}})
	res, ok := runner.Run(ctx, steps, func(i int, st api.GateStep) {
		live.Steps[i] = st
		s.saveGate(id, turn, live, NewEvent{Type: api.EventGate, Turn: turn,
			Data: api.GateEventData{Phase: api.GateEventStep, Index: i, Step: &st}})
	})
	res.StartedAt = started
	return res, ok
}

// saveGate stores the running gate on the turn with its event.
func (s *Service) saveGate(id string, turn int, res api.GateResult, ev NewEvent) {
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		tu := t.TurnN(turn)
		if tu == nil || t.GatePhase == nil || t.GatePhase.Turn != turn {
			return nil, errNoChange
		}
		cp := res
		cp.Steps = slices.Clone(res.Steps)
		tu.Gate = &cp
		if t.GatePhase.State != api.GatePhaseRunning {
			t.GatePhase.State, t.GatePhase.Wait, t.GatePhase.Position = api.GatePhaseRunning, "", 0
		}
		return []NewEvent{ev}, nil
	})
	if err != nil && !errors.Is(err, context.Canceled) && s.root.Err() == nil {
		s.log.Warn("task: save the gate", "task", id, "err", err)
	}
}

// settle waits until the other tasks of the repository stop working, gateSettleMinutes at most. It reports false
// when the run was stopped meanwhile.
func (s *Service) settle(r *run, t *Task) bool {
	set := s.settings()
	_, err := s.mutate(r.ctx, t.ID, func(cur *Task) ([]NewEvent, error) {
		if cur.GatePhase == nil {
			return nil, errNoChange
		}
		cur.GatePhase.State = api.GatePhaseSettling
		return []NewEvent{{Type: api.EventGate, Turn: cur.GatePhase.Turn,
			Data: api.GateEventData{Phase: api.GateEventSettling}}}, nil
	})
	if err != nil && r.ctx.Err() == nil {
		s.log.Warn("task: settle the gate", "task", t.ID, "err", err)
	}
	poll := time.Duration(max(set.GateSettlePollMs, 100)) * time.Millisecond
	deadline := s.now().Add(time.Duration(max(set.GateSettleMinutes, 0)) * time.Minute)
	for s.now().Before(deadline) && s.othersWorking(r.ctx, t) {
		s.sleep(r.ctx, poll, nil)
		if r.ctx.Err() != nil {
			return false
		}
	}
	return r.ctx.Err() == nil
}

// gateStopped handles a gate cut short: a cancel ends the task, anything else (shutdown, takeover) leaves the
// phase for the next run.
func (s *Service) gateStopped(id string) (bool, error) {
	t, err := s.repo.Get(s.background(), id)
	if err != nil {
		return false, err
	}
	if t.CancelRequested {
		s.finishCancel(id)
	}
	return false, nil
}

// recordLedger writes the result of the gate's spec-task commands to the proof ledger.
func (s *Service) recordLedger(t *Task, tu *Turn, res api.GateResult) {
	p := t.Proof
	if s.o.Ledger == nil || p.FeatureKey == "" || p.Project == "" {
		return
	}
	byTask := map[string][]*api.GateStep{}
	var order []string
	for i := range res.Steps {
		st := &res.Steps[i]
		if st.Kind != api.StepTask {
			continue
		}
		for _, id := range st.TaskIDs {
			if _, ok := byTask[id]; !ok {
				order = append(order, id)
			}
			byTask[id] = append(byTask[id], st)
		}
	}
	head := ""
	if tu.Snap != nil {
		head = tu.Snap.Head
	}
	var results []proof.Result
	for _, id := range order {
		steps := byTask[id]
		state := api.GateGreen
		ran := false
		for _, st := range steps {
			switch st.State {
			case api.StepRed:
				if state == api.GateGreen {
					state = api.GateRed
				}
				ran = true
			case api.StepError:
				state, ran = api.GateError, true
			case api.StepGreen:
				ran = true
			default: // not run: the result says nothing about this task
				if state == api.GateGreen {
					ran = false
				}
			}
		}
		if !ran || (state == api.GateGreen && slices.ContainsFunc(steps, func(st *api.GateStep) bool {
			return st.State != api.StepGreen
		})) {
			continue
		}
		var cmds []proof.EntryCmd
		for _, st := range steps {
			cmds = append(cmds, proof.EntryCmd{Cmd: st.Cmd, Exit: st.Exit, Ms: st.Ms, State: st.State})
		}
		results = append(results, proof.Result{TaskID: id, State: state, Source: proof.SourceTask, Ref: t.ID,
			GitHead: head, Cmds: cmds})
	}
	ctx, cancel := context.WithTimeout(s.background(), 30*time.Second)
	defer cancel()
	if err := s.o.Ledger.Record(ctx, p.Project, proof.Feature{Key: p.FeatureKey, Display: p.Feature}, results); err != nil {
		s.log.Warn("task: record the gate in the ledger", "task", t.ID, "err", err)
	}
}

// barChanges compares the task's frozen bar with the current files and returns what changed.
func (s *Service) barChanges(t *Task) []string {
	p := t.Proof
	if p == nil || p.Bar == nil {
		return nil
	}
	lines := map[string]string{}
	if p.FeatureDir != "" && t.Spec != nil {
		if data, _, err := fsutil.ReadRegular(filepath.Join(p.FeatureDir, "tasks.md"), 4<<20); err == nil {
			tasks := proof.ParseTasks(string(data))
			for _, id := range t.Spec.TaskIDs {
				if tl, ok := tasks.Find(id); ok {
					lines[id] = tl.Line
				}
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil
		}
	}
	var cmds []string
	for _, c := range p.Commands {
		cmds = append(cmds, c.Cmd)
	}
	cur := proof.SnapshotBar(t.RepoRoot, p.FeatureDir, lines, cmds)
	return p.Bar.Diff(cur)
}

// afterGreen ends a gate that passed (or had nothing to run): the verifier when the task asks for one and the
// turns changed files, else the task is done.
func (s *Service) afterGreen(t *Task, turn int, res *api.GateResult, files []string, barChanged ...string) (
	bool, error) {
	if t.Proof != nil && t.Proof.Verify && len(files) > 0 {
		return s.startVerifier(t, turn, res, files, barChanged)
	}
	return false, s.endGate(t.ID, turn, res, barChanged)
}

// endGate stores the gate's result and ends the phase: the task is done.
func (s *Service) endGate(id string, turn int, res *api.GateResult, barChanged []string) error {
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		if t.GatePhase == nil || t.GatePhase.Turn != turn {
			return nil, errNoChange
		}
		var evs []NewEvent
		tu := t.TurnN(turn)
		if tu != nil {
			tu.Gate = res
			if res != nil {
				evs = append(evs, gateResultEvent(turn, res, barChanged))
				if st := laneStep(*res); st != nil && len(st.Files) > 0 {
					evs = append(evs, NewEvent{Type: api.EventLane, Turn: turn, Data: api.LaneEventData{
						Kind: api.LaneViolation, Paths: lanePaths(t), Files: st.Files}})
				}
			}
		}
		if t.Proof != nil && len(barChanged) > 0 {
			t.Proof.BarChanged = mergeSorted(t.Proof.BarChanged, barChanged)
		}
		t.GatePhase = nil
		if !t.CancelRequested {
			t.Status, t.Error = StatusDone, ""
		}
		return evs, nil
	})
	if err != nil && !errors.Is(err, errNoChange) {
		return err
	}
	s.checkDeps(s.background())
	return nil
}

// cancelGate ends the gate phase of a cancelled task: a gate still running is marked skipped, a pending verdict
// becomes an error.
func cancelGate(t *Task, ph *GatePhase, now time.Time) []NewEvent {
	t.GatePhase = nil
	tu := t.TurnN(ph.Turn)
	if tu == nil {
		return nil
	}
	var evs []NewEvent
	if g := tu.Gate; g != nil && g.State == api.GateRunning {
		cp := *g
		cp.Steps = slices.Clone(g.Steps)
		for i := range cp.Steps {
			if st := cp.Steps[i].State; st == api.StepPending || st == api.StepRunning {
				cp.Steps[i].State = api.StepNotRun
				cp.NotRun = append(cp.NotRun, cp.Steps[i].Label)
			}
		}
		cp.State, cp.EndedAt = api.GateSkipped, api.FormatTime(now)
		tu.Gate = &cp
		evs = append(evs, gateResultEvent(ph.Turn, &cp, nil))
	}
	if v := tu.Verifier; v != nil && v.Verdict == api.GateRunning {
		cp := *v
		cp.Verdict, cp.Detail = api.VerdictError, "cancelled"
		tu.Verifier = &cp
		evs = append(evs, NewEvent{Type: api.EventVerify, Turn: ph.Turn, Data: api.VerifyEventData{Phase: "result",
			ChildID: v.ChildID, Result: &cp}})
	}
	return evs
}

func gateResultEvent(turn int, res *api.GateResult, barChanged []string) NewEvent {
	cp := *res
	return NewEvent{Type: api.EventGate, Turn: turn, Data: api.GateEventData{Phase: api.GateEventResult, Result: &cp,
		BarChanged: barChanged}}
}

func lanePaths(t *Task) []string {
	if t.Proof == nil || t.Proof.Lane == nil {
		return []string{}
	}
	return t.Proof.Lane.Paths
}

func mergeSorted(a, b []string) []string {
	set := map[string]bool{}
	for _, x := range a {
		set[x] = true
	}
	for _, x := range b {
		set[x] = true
	}
	return slices.Sorted(maps.Keys(set))
}

// fixTurn stores the failed gate (or rejected review) of turn and opens an automatic fix turn by `by` with msg.
func (s *Service) fixTurn(id string, turn int, res api.GateResult, barChanged []string, msg, by string) (bool, error) {
	more := false
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		more = false
		if t.GatePhase == nil || t.GatePhase.Turn != turn || t.CancelRequested {
			return nil, errNoChange
		}
		tu := t.TurnN(turn)
		if tu == nil {
			return nil, errNoChange
		}
		next := &Turn{N: len(t.Turns) + 1, By: by, Message: msg, Status: TurnRunning, Model: t.Model,
			Tier: t.Tier, Effort: t.Effort, StartedAt: s.now()}
		var evs []NewEvent
		if by == ByGate {
			res.AutoFix = next.N
			tu.Gate = &res
			evs = append(evs, gateResultEvent(turn, &res, barChanged))
		}
		t.Proof.AutoFixes++
		if len(barChanged) > 0 {
			t.Proof.BarChanged = mergeSorted(t.Proof.BarChanged, barChanged)
		}
		t.Turns = append(t.Turns, next)
		t.GatePhase = nil
		more = true
		return append(evs, NewEvent{Type: api.EventLeadMessage, Turn: next.N,
			Data: api.LeadMessageData{Message: msg, By: by, Model: t.Model, Effort: t.Effort, Tier: t.Tier}}), nil
	})
	if err != nil && !errors.Is(err, errNoChange) {
		return false, err
	}
	return more, nil
}

// pastHygiene returns the plan steps with the hygiene step passed, so a run reaches the steps it stopped.
func pastHygiene(steps []api.GateStep) []api.GateStep {
	out := slices.Clone(steps)
	for i := range out {
		if out[i].Kind == api.StepHygiene {
			out[i].State, out[i].Findings, out[i].Tail = api.StepGreen, nil, ""
		}
	}
	return out
}

// withHygieneOf returns rest, a run past the hygiene step (pastHygiene), with the hygiene step of first put back.
func withHygieneOf(rest, first api.GateResult) api.GateResult {
	for i := range rest.Steps {
		if rest.Steps[i].Kind == api.StepHygiene && i < len(first.Steps) && first.Steps[i].Kind == api.StepHygiene {
			rest.Steps[i] = first.Steps[i]
		}
	}
	rest.State, rest.Settled = gate.Overall(rest.Steps), first.Settled
	return rest
}
