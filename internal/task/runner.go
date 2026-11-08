package task

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/brief"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/launch"
	"github.com/NovaSeele/Seelegent/internal/memory"
	"github.com/NovaSeele/Seelegent/internal/pool"
	"github.com/NovaSeele/Seelegent/internal/usage"
)

// retryDelays are the waits before retrying a turn on the same account after a transient failure (old
// RETRY_DELAYS); after them the turn moves to another account.
func retryDelays() []time.Duration { return []time.Duration{15 * time.Second, 45 * time.Second} }

// noAccountRecheck is how often a task that no account can run looks again (account changes wake it sooner).
const noAccountRecheck = time.Minute

// extraAttempts is how many attempts a turn may make beyond one per account. Quota attempts do not count (see
// maxQuotaRests).
const extraAttempts = 3

// maxQuotaRests is how many attempts of one turn may end on a quota limit. Such an attempt is followed by a rest and a
// resume, or a handover: the turn waits for quota, it does not fail, so these attempts stay out of maxAttempts (a
// one-account task that rested four times in a turn used to end failed). This cap only stops a runaway loop, such as
// a CLI that reports a limit on every start: each limit rests the account (an hour by default when the CLI gives no
// reset time ahead), so 50 is about two days of such a loop on one account, while a real turn needs a handful (a
// 5-hour window resets at most about five times a day). A count rather than a time limit, because a turn has no
// deadline of its own and a weekly limit legitimately rests for days.
const maxQuotaRests = 50

// errStop tells the runner that the task was cancelled or taken over while it prepared an attempt.
var errStop = errors.New("stopped")

// errShutdown ends a run because the daemon stops; the attempt stays open for the next daemon to recover.
var errShutdown = errors.New("the daemon is stopping")

// run is the goroutine that works on one task: it runs the open turn attempt by attempt until the turn ends.
type run struct {
	id   string
	ctx  context.Context // ends on cancel, takeover or shutdown; never stops a worker by itself
	stop context.CancelFunc
	// stopCtx ends only on cancel or takeover, never at shutdown: a launch that is starting is aborted only then,
	// so a daemon shutdown never cancels a worker.
	stopCtx    context.Context
	stopLaunch context.CancelFunc
	done       chan struct{}

	mu      sync.Mutex
	stopped bool
	handle  *launch.Handle
	// starting is a launch whose worker is about to run (launch.Options.Started) or that recovery is about to
	// attach: its tree counts for the worker guard before handle is set.
	starting *launch.Handle
	account  string
	live     liveState
	// cwd is the task's folder, against which the loop check resolves the relative paths of edits; repo is its
	// repository root ("" outside git). The loop check hashes only files inside one of them.
	cwd, repo string
}

// setStarting records a launch whose worker is about to run, until setHandle or clearHandle.
func (r *run) setStarting(h *launch.Handle) {
	r.mu.Lock()
	if r.handle == nil {
		r.starting = h
	}
	r.mu.Unlock()
}

// tree returns the launch whose process tree belongs to this run's worker (nil when none).
func (r *run) tree() *launch.Handle {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.handle != nil {
		return r.handle
	}
	return r.starting
}

// setHandle records the worker being followed; a run that was already asked to stop cancels it at once.
func (r *run) setHandle(h *launch.Handle, accountID string, started time.Time) {
	r.mu.Lock()
	r.handle, r.starting, r.account = h, nil, accountID
	r.live = liveState{running: true, started: started, account: accountID}
	stopped := r.stopped
	r.mu.Unlock()
	if stopped {
		_ = h.Cancel()
	}
}

func (r *run) clearHandle() {
	r.mu.Lock()
	r.handle, r.starting, r.account, r.live = nil, nil, "", liveState{}
	r.mu.Unlock()
}

// requestStop ends the run's waits and asks the launcher to stop the worker tree (cancel and takeover). The run
// keeps following the worker until it has exited, so its slot is released only then.
func (r *run) requestStop() {
	r.mu.Lock()
	r.stopped = true
	h := r.handle
	r.mu.Unlock()
	r.stop()
	r.stopLaunch()
	if h != nil {
		_ = h.Cancel()
	}
}

// note updates what is known about the worker from one of its events. replay marks an event read again after a
// daemon restart (the files it touched may have changed since).
func (r *run) note(ev event.Event, now time.Time, replay bool) {
	// An edit that reports only its file is told apart by the file's content (read outside the lock), so repeated
	// real edits of one file count as progress, not as a repeated call (old fix 492493d, bugs.md A1).
	pathOnly, mark := false, ""
	if ev.Kind == event.KindToolResult && ev.Tool != nil {
		if p := pathOnlyEdit(ev.Tool); p != "" {
			pathOnly = true
			if !replay {
				r.mu.Lock()
				cwd, repo := r.cwd, r.repo
				r.mu.Unlock()
				mark = fileMark(cwd, repo, p)
			}
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	l := &r.live
	l.lastEvent = now
	switch ev.Kind {
	case event.KindSession:
		l.sawInit = true
		if ev.Session != nil && ev.Session.ConversationID != "" {
			l.conversation = ev.Session.ConversationID
		}
	case event.KindToolCall:
		l.waiting = time.Time{}
		if ev.Tool != nil {
			l.tool, l.command, l.toolSince, l.step = ev.Tool.Name, ev.Tool.Command, now, ev.Tool.Name
			l.pending = stepKey(ev.Tool)
		}
	case event.KindToolResult:
		l.tool, l.command, l.toolSince = "", "", time.Time{}
		if ev.Tool != nil {
			if ev.Tool.Name != "" {
				l.step = ev.Tool.Name // agy reports a quick tool only when it is done (F9)
			}
			if pathOnly && mark == "" {
				// Content unknown: each such edit is its own step.
				l.edits++
				mark = fmt.Sprintf("#%d", l.edits)
			}
			l.steps = addStep(l.steps, ev.Tool, l.pending, mark)
			l.pending = ""
		}
	case event.KindFile:
		if ev.File != nil && ev.File.Op == event.FileEdit && len(l.edited) < maxLiveEdited &&
			!slices.Contains(l.edited, ev.File.Path) {
			l.edited = append(l.edited, ev.File.Path)
		}
	case event.KindMessage:
		l.waiting, l.step = time.Time{}, "responding"
	case event.KindBackgroundWait:
		l.waiting = now
	default:
	}
}

// live returns what is known about the worker of task id (zero when none is followed).
func (s *Service) live(id string) liveState {
	s.mu.Lock()
	r := s.runs[id]
	s.mu.Unlock()
	if r == nil {
		return liveState{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	l := r.live
	l.steps, l.edited = slices.Clone(l.steps), slices.Clone(l.edited)
	return l
}

// startRun starts the run of task id unless one is going.
func (s *Service) startRun(id string) { s.startRunWith(id, nil) }

// startRunWith starts the run of task id unless one is going; pre is the launch of a worker that recovery will
// attach (its tree counts for the worker guard at once), or nil.
func (s *Service) startRunWith(id string, pre *launch.Handle) {
	s.mu.Lock()
	if s.closed || s.runs[id] != nil {
		s.mu.Unlock()
		return
	}
	ctx, stop := context.WithCancel(s.root)
	stopCtx, stopLaunch := context.WithCancel(context.Background())
	r := &run{id: id, ctx: ctx, stop: stop, stopCtx: stopCtx, stopLaunch: stopLaunch, done: make(chan struct{}),
		starting: pre}
	s.runs[id] = r
	s.mu.Unlock()
	s.poke()
	ok := s.spawn("run", id, func() {
		defer func() {
			if v := recover(); v != nil {
				// We own the worker: stop it before the task is marked failed.
				r.mu.Lock()
				h := r.handle
				r.mu.Unlock()
				if h != nil {
					_ = h.Cancel()
				}
				s.endRun(r)
				panic(v)
			}
			s.endRun(r)
		}()
		s.runTask(r)
	})
	if !ok {
		s.endRun(r)
	}
}

// endRun forgets a finished run. A followup that arrived while the run was ending starts a new one.
func (s *Service) endRun(r *run) {
	s.mu.Lock()
	if s.runs[r.id] == r {
		delete(s.runs, r.id)
	}
	closed := s.closed
	s.mu.Unlock()
	r.stop()
	r.stopLaunch()
	close(r.done)
	if closed {
		return
	}
	if t, err := s.repo.Get(s.root, r.id); err == nil && Active(t.Status) && !t.Manual {
		if tu := t.LastTurn(); t.GatePhase != nil || (tu != nil && tu.Status == TurnRunning) {
			s.startRun(r.id)
		}
	}
}

// runTask works on the task until its open turn ended, it was cancelled or taken over, or the daemon stops.
func (s *Service) runTask(r *run) {
	for {
		t, err := s.repo.Get(s.root, r.id)
		if err != nil {
			if s.root.Err() == nil {
				s.log.Error("task: load", "task", r.id, "err", err)
			}
			return
		}
		if !Active(t.Status) || t.Manual {
			return
		}
		r.mu.Lock()
		r.cwd, r.repo = t.Cwd, t.RepoRoot
		r.mu.Unlock()
		if t.GatePhase != nil {
			switch {
			case t.CancelRequested:
				s.finishCancel(r.id)
				return
			case r.ctx.Err() != nil:
				return
			}
			more, err := s.gatePhase(r, t)
			if err != nil {
				if s.root.Err() != nil {
					return
				}
				s.fail(r.id, err.Error())
				return
			}
			if !more {
				return
			}
			continue
		}
		tu := t.LastTurn()
		if tu == nil || tu.Status != TurnRunning {
			s.fail(r.id, "the task has no open turn")
			return
		}
		var more bool
		switch a := tu.LastAttempt(); {
		case a != nil && a.Status == AttemptRunning:
			more, err = s.resumeAttempt(r, t, tu, a)
		case t.CancelRequested:
			s.finishCancel(r.id)
			return
		case r.ctx.Err() != nil:
			return
		default:
			more, err = s.newAttempt(r, t, tu)
		}
		if err != nil {
			if s.root.Err() != nil {
				return // shutdown: the next daemon picks the task up
			}
			s.fail(r.id, err.Error())
			return
		}
		if !more {
			return
		}
	}
}

// takeBaseline records which files under the task's folder already had uncommitted changes before its first
// worker ran, so the brief tells the worker not to revert them (old A10), and where the repository stood (HEAD and
// branch), so the Git view can show what the task committed. Both are taken once: followups keep them.
func (s *Service) takeBaseline(ctx context.Context, t *Task) {
	root, files, start := captureBaseline(ctx, s.o.Git, t.Cwd, s.now())
	if ctx.Err() != nil {
		return
	}
	_, err := s.mutate(ctx, t.ID, func(cur *Task) ([]NewEvent, error) {
		if cur.BaselineTaken {
			return nil, errNoChange
		}
		cur.BaselineTaken, cur.Baseline, cur.GitStart = true, files, start
		if cur.RepoRoot == "" {
			cur.RepoRoot = root
		}
		return nil, nil
	})
	if err != nil && ctx.Err() == nil {
		s.log.Warn("task: baseline", "task", t.ID, "err", err)
	}
}

// beforeFirstLaunch takes what the first launch of turn tu needs from the worktree as it is right before the worker
// starts: the task's baseline (its first turn) and the turn's start snapshot. It returns the task as stored then.
func (s *Service) beforeFirstLaunch(ctx context.Context, t *Task, tu *Turn) (*Task, error) {
	if !t.BaselineTaken && tu.N == 1 {
		s.takeBaseline(ctx, t)
		cur, err := s.repo.Get(ctx, t.ID)
		if err != nil {
			return nil, err
		}
		t = cur
	}
	if s.o.Snapshots != nil && t.RepoRoot != "" {
		if x := t.TurnN(tu.N); x != nil && x.Snap == nil && len(x.Attempts) == 0 {
			s.takeStartSnapshot(ctx, t, x)
		}
	}
	return s.repo.Get(ctx, t.ID)
}

// attemptResult is how a followed attempt ended.
type attemptResult struct {
	outcome backend.Outcome
	exit    launch.Exit
	// finish are the parser's closing events, logged together with the attempt's end.
	finish []event.Event
	// interrupted: nothing ran (the daemon stopped before the worker started).
	interrupted bool
}

// newAttempt picks an account, launches the worker for the open turn and follows it to the end. It reports
// whether the turn goes on with another attempt.
func (s *Service) newAttempt(r *run, t *Task, tu *Turn) (bool, error) {
	ctx := r.ctx
	if s.holdForMemory(r, t.ID) {
		// Low memory: waited without touching the pool; look at the task again before asking for an account.
		return true, nil
	}
	b, err := s.backend(t.Backend)
	if err != nil {
		return false, err
	}
	set := s.settings()
	var prov *account.Provider
	if t.Provider != "" {
		p, err := s.o.Accounts.GetProvider(ctx, t.Provider)
		if err != nil {
			return false, fmt.Errorf("provider %s: %w", t.Provider, err)
		}
		prov = &p
	}
	policy, err := backend.PolicyFor(t.Backend, set, prov)
	if err != nil {
		return false, err
	}
	model := tu.Model
	req := pool.Request{
		TaskID: t.ID, Backend: t.Backend, Group: b.QuotaGroup(model), Model: model, Provider: t.Provider,
		Prefer: slices.Clone(t.Prefer), Exclude: excluded(tu),
		Allowed: func(m string) bool { return policy.Allowed(m) == nil },
	}
	reason := "best free account"
	excludedBefore := len(req.Exclude) > 0
	pinned := false
	switch owner := t.AccountID; {
	case owner == "" || slices.Contains(req.Exclude, owner):
	case tu.Handback:
		// Only the owner's conversation saw what the user did in the console: wait for it rather than move.
		req.Account, reason = owner, "the hand-back summary needs the conversation's account"
		pinned = true
	default:
		h, err := s.o.Pool.Handover(ctx, pool.HandoverInput{Current: owner, Class: classOf(tu.LastAttempt()),
			Request: req})
		if err != nil {
			if ctx.Err() != nil {
				return true, nil
			}
			return false, err
		}
		reason = h.Why
		switch h.Action {
		case pool.ActionStay:
			req.Account = owner
		case pool.ActionMove:
			req.Prefer = append([]string{h.To.Account.ID}, req.Prefer...)
			req.Exclude = append(req.Exclude, owner)
		case pool.ActionWait:
		}
	}
	onWait := func(u *pool.Unavailable) { s.setWaiting(t.ID, u) }
	lease, choice, err := s.o.Pool.Wait(ctx, req, onWait)
	if pinned && errors.Is(err, pool.ErrNoAccount) && ctx.Err() == nil {
		// The conversation's account can no longer run at all (signed out, disabled, removed, or its models are
		// no longer allowed); resting or busy would have waited. Run the hand-back on another account rather than
		// wait forever; the next attempt tries the owner again first.
		req.Exclude = append(req.Exclude, req.Account)
		req.Account, reason = "", "the conversation's account cannot run: best free account"
		lease, choice, err = s.o.Pool.Wait(ctx, req, onWait)
	}
	if err != nil {
		if ctx.Err() != nil {
			return true, nil
		}
		if errors.Is(err, pool.ErrNoAccount) {
			if moved, ferr := s.memberFallback(ctx, t, tu); ferr != nil {
				return false, ferr
			} else if moved {
				return true, nil
			}
			if u := (*pool.Unavailable)(nil); errors.As(err, &u) && len(u.NoCredit) > 0 {
				// Only accounts without credit are left: no time brings them back, so fail now (never wait).
				return s.giveUp(t.ID, tu.N, u.Error())
			}
			if excludedBefore {
				return s.giveUp(t.ID, tu.N, "no other account can run this task: "+lastError(tu))
			}
			s.setWaiting(t.ID, &pool.Unavailable{Reason: pool.ReasonNoAccount, Backend: t.Backend, Group: req.Group})
			s.sleep(ctx, noAccountRecheck, s.o.Pool.Changed())
			return true, nil
		}
		return false, err
	}
	defer lease.Release()
	if s.holdStart(r, t.ID, lease.Release) {
		// Low memory: the account is free again for another task while this one waits (resources.go).
		return true, nil
	}
	if len(tu.Attempts) == 0 {
		// The turn's first launch, with the account in hand: the baseline and the start snapshot are taken now, not
		// when the turn was queued, so what others changed while it waited for an account is not this turn's work
		// (field report 4: a task that waited 2.5 h for quota diffed against its delegate-time snapshot). Retries
		// of the turn keep this snapshot.
		fresh, err := s.beforeFirstLaunch(ctx, t, tu)
		if err != nil {
			if ctx.Err() != nil {
				return true, nil
			}
			return false, err
		}
		t, tu = fresh, fresh.TurnN(tu.N)
		if tu == nil || len(tu.Attempts) > 0 {
			return true, nil // the task moved on meanwhile: look at it again
		}
	}

	acc := choice.Account
	attemptModel := choice.Model
	if attemptModel == "" {
		attemptModel = model
	}
	continuing := t.ConversationID != "" && acc.ID == t.AccountID
	prompt := s.prompt(ctx, t, tu, continuing, b.Capabilities().PlanShell)
	n := len(tu.Attempts) + 1
	dir := s.attemptDir(t.ID, tu.N, n)
	launchDir := filepath.Join(dir, "launch")
	label := usage.SafeLabel(acc.Label, acc.ID)
	_, err = s.mutate(s.background(), t.ID, func(t *Task) ([]NewEvent, error) {
		cur := t.LastTurn()
		if t.CancelRequested || t.Manual || cur == nil || cur.N != tu.N || len(cur.Attempts) != n-1 {
			return nil, errStop
		}
		if t.Status != StatusRunning {
			if err := t.SetStatus(StatusRunning); err != nil {
				return nil, err
			}
		}
		t.Waiting, t.WaitDetail, t.WaitingUntil, t.MemoryWait = "", "", time.Time{}, nil
		t.WaitBudget = nil
		a := &Attempt{N: n, AccountID: acc.ID, AccountLabel: label, Backend: t.Backend, Model: attemptModel,
			Continuing: continuing, StartedAt: s.now(), Status: AttemptRunning, LaunchDir: launchDir, Prompt: prompt}
		if continuing {
			a.ConversationID = t.ConversationID
		}
		cur.Attempts = append(cur.Attempts, a)
		cur.Model, t.Model = attemptModel, attemptModel
		var evs []NewEvent
		if t.AccountID != "" && t.AccountID != acc.ID {
			evs = append(evs, NewEvent{Type: api.EventHandoff, Turn: cur.N, Attempt: n,
				Data: api.HandoffData{From: t.AccountID, To: acc.ID, Reason: reason}})
		}
		route := api.RouteData{AccountID: acc.ID, AccountLabel: label, Model: attemptModel, Reason: reason,
			Continuing: continuing}
		if choice.RemainingKnown {
			rem := choice.Remaining
			route.Remaining = &rem
		}
		evs = append(evs, NewEvent{Type: api.EventRoute, Turn: cur.N, Attempt: n, Data: route},
			NewEvent{Type: api.EventWorkerPrompt, Turn: cur.N, Attempt: n,
				Data: api.WorkerPromptData{Length: len([]rune(prompt)), Preview: brief.Preview(prompt)}})
		return evs, nil
	})
	if errors.Is(err, errStop) {
		return true, nil
	}
	if err != nil {
		return false, err
	}

	res, err := s.launchAndFollow(r, t, tu.N, n, acc, b, prompt, attemptModel, continuing, dir, launchDir)
	if err != nil {
		return false, err
	}
	return s.finishAttempt(r, t.ID, tu.N, n, acc.ID, attemptModel, b, res)
}

// launchAndFollow starts the worker of an attempt and follows it until it exits. A launch that cannot start ends
// as a failed outcome (auth for an account without a usable key), never as an engine error.
func (s *Service) launchAndFollow(r *run, t *Task, turn, n int, acc account.Account, b backend.Backend,
	prompt, model string, continuing bool, dir, launchDir string) (attemptResult, error) {
	ctx := r.ctx
	failed := func(class event.Class, err error) (attemptResult, error) {
		switch {
		case s.root.Err() != nil:
			return attemptResult{}, errShutdown // the next daemon closes the attempt and starts another
		case r.stopCtx.Err() != nil:
			return attemptResult{outcome: backend.Outcome{Status: backend.OutcomeCanceled}, interrupted: true}, nil
		}
		return attemptResult{outcome: backend.Outcome{Status: backend.OutcomeFailed, Class: class,
			Error: err.Error()}, interrupted: true}, nil
	}
	set := s.settings()
	tu := t.TurnN(turn)
	base := s.o.BaseEnv()
	extra := ParallelVars(base, s.limits().TestParallelism)
	maps.Copy(extra, s.toolEnv(ctx, t.Cwd))
	allowGit := tu != nil && tu.AllowGit // per command (Turn.AllowGit)
	env, err := s.o.Accounts.Env(ctx, acc, b, account.EnvOptions{Base: base, Model: model,
		AllowGit: allowGit, Extra: extra})
	if err != nil {
		if errors.Is(err, account.ErrNoKey) || errors.Is(err, account.ErrProviderUnavailable) {
			return failed(event.ClassAuthRequired, err)
		}
		return failed(event.ClassFatal, fmt.Errorf("worker environment: %w", err))
	}
	var prov *account.Provider
	if acc.Provider != "" {
		p, err := s.o.Accounts.GetProvider(ctx, acc.Provider)
		if err != nil {
			class := event.ClassFatal
			if errors.Is(err, account.ErrNotFound) || errors.Is(err, account.ErrProviderUnavailable) {
				class = event.ClassAuthRequired
			}
			return failed(class, fmt.Errorf("provider %s: %w", acc.Provider, err))
		}
		prov = &p
	}
	if err := b.Prepare(env.Profile, prov); err != nil {
		return failed(event.ClassFatal, fmt.Errorf("prepare the profile: %w", err))
	}
	if s.o.GitShim != "" && set.GitGuard && !allowGit && backend.NeedsShim(b, env.Profile) {
		env.Vars = prependPath(env.Vars, s.o.GitShim)
	}
	timeout := time.Duration(set.TaskTimeoutMinutes) * time.Minute
	bt := backend.Turn{Prompt: prompt, Model: model, Plan: t.Mode == ModePlan, Timeout: timeout,
		Profile: env.Profile, AttemptDir: dir, Sandbox: set.Backends.Codex.Sandbox}
	if tu != nil {
		bt.Effort = tu.Effort
	}
	if continuing {
		bt.ConversationID = t.ConversationID
	}
	cmd, err := b.Command(bt)
	if err != nil {
		return failed(event.ClassFatal, err)
	}
	spec, err := backend.LaunchSpec(cmd, t.Cwd, dir, timeout)
	if err != nil {
		return failed(event.ClassFatal, err)
	}
	spec.Tree = WorkerTree(set)
	// Fail closed: a worker whose output could not be masked after a daemon restart is never started.
	if err := saveMask(dir, env.Secrets); err != nil {
		return failed(event.ClassFatal, fmt.Errorf("keep the mask list: %w", err))
	}
	h, err := launch.Start(r.stopCtx, launch.Options{Launcher: s.o.Launcher, Dir: launchDir, Spec: spec,
		Env: env.Vars, Started: r.setStarting})
	if err != nil {
		r.clearHandle()
		return failed(event.ClassFatal, fmt.Errorf("start the worker: %w", err))
	}
	r.setHandle(h, acc.ID, s.clk.Now())
	defer r.clearHandle()
	return s.follow(r, h, b, attemptMask{secrets: env.Secrets}, t.ID, turn, n, Offsets{Turn: turn, Attempt: n})
}

// closingEvents are the parser's closing events as the task's log keeps them, and the error texts they hold. A CLI
// that ended without a report gets a report Seelegent made up from its error, followed by the error event with the
// same text: the log keeps the report and drops the error event that repeats it (design review: one crash message
// stood three times in the history, with the attempt's end, which leaves out a text said here).
func closingEvents(finish []event.Event) (kept []event.Event, said map[string]bool) {
	said = map[string]bool{}
	synthesized := ""
	for _, ev := range finish {
		if ev.Kind == event.KindReport && ev.Report != nil && ev.Report.Synthesized && ev.Report.Error != "" {
			synthesized = ev.Report.Error
		}
	}
	for _, ev := range finish {
		switch {
		case ev.Kind == event.KindError && ev.Error != nil && synthesized != "" && ev.Error.Message == synthesized:
			continue
		case ev.Kind == event.KindError && ev.Error != nil:
			said[ev.Error.Message] = true
		case ev.Kind == event.KindReport && ev.Report != nil && ev.Report.Error != "":
			said[ev.Report.Error] = true
		}
		kept = append(kept, ev)
	}
	return kept, said
}

// classify maps an outcome to an attempt status and its error text.
func classify(o backend.Outcome, exit launch.Exit, timeoutMinutes int) (status, text string) {
	switch o.Status {
	case backend.OutcomeOK:
		return AttemptOK, ""
	case backend.OutcomeCanceled:
		return AttemptCancelled, ""
	case backend.OutcomeBackgroundWait:
		return AttemptBackgroundWait, o.Error // BackgroundWaitTimeout when the time limit ended the wait
	case backend.OutcomeFailed:
	}
	if exit.MemoryLimitHit {
		// The launcher saw the worker tree reach its Job Object cap (Spec.Tree.MemoryLimitMB).
		return AttemptError, "the worker hit the memory cap (workerMemoryLimitMB): raise the cap or test fewer " +
			"packages at once"
	}
	switch exit.Reason {
	case launch.ReasonTimeout:
		return AttemptError, fmt.Sprintf("the turn timed out after %d min", timeoutMinutes)
	case launch.ReasonLost:
		return AttemptTransient, "the worker's launcher stopped unexpectedly"
	case launch.ReasonExited, launch.ReasonCanceled, launch.ReasonStartFailed:
	}
	switch o.Class {
	case event.ClassQuotaExhausted:
		return AttemptQuota, o.Error
	case event.ClassAuthRequired:
		return AttemptAuth, o.Error
	case event.ClassCredits:
		return AttemptCredits, o.Error
	case event.ClassTransient:
		return AttemptTransient, o.Error
	case event.ClassFatal:
	}
	if o.Error == "" {
		return AttemptError, "the turn failed"
	}
	return AttemptError, o.Error
}

// finishAttempt records how an attempt ended and decides what the turn does next. It reports whether another
// attempt follows.
func (s *Service) finishAttempt(r *run, id string, turn, n int, accountID, model string, b backend.Backend,
	res attemptResult) (bool, error) {
	ctx := s.background()
	if err := removeMask(s.attemptDir(id, turn, n)); err != nil {
		s.log.Warn("task: remove the mask list", "task", id, "err", err)
	}
	set := s.settings()
	o := res.outcome
	status, errText := classify(o, res.exit, set.TaskTimeoutMinutes)
	group := b.QuotaGroup(model)
	var until time.Time
	switch status {
	case AttemptQuota:
		cd, err := s.o.Pool.MarkExhausted(ctx, accountID, pool.Exhaustion{Group: group, Model: model,
			ResetAt: o.ResetAt, Reason: errText})
		if err != nil {
			s.log.Warn("task: rest the account", "account", accountID, "err", err)
		}
		until = cd.Until
	case AttemptAuth:
		if err := s.o.Pool.MarkAuthFailed(ctx, accountID, errText); err != nil {
			s.log.Warn("task: mark the account signed out", "account", accountID, "err", err)
		}
	case AttemptCredits:
		// Not a quota rest: the account is left out until the user acts; the next pick moves on, or fails at once
		// when no other account can run the task (pool.Unavailable.NoCredit).
		if err := s.o.Pool.MarkNoCredit(ctx, accountID, errText); err != nil {
			s.log.Warn("task: rest the account without credit", "account", accountID, "err", err)
		}
	}
	maxAttempts := s.maxAttempts(ctx, b.Name())
	// preCheck runs the post-turn checks only when this attempt may end the turn: when its budget is used up.
	used, limit := n, maxAttempts
	if cur, err := s.repo.Get(ctx, id); err == nil {
		if tu := cur.TurnN(turn); tu != nil {
			counted, rests := attemptBudget(tu, n, status)
			used = counted
			if status == AttemptQuota {
				used, limit = rests, maxQuotaRests
			}
		}
	}
	check := s.preCheck(ctx, id, turn, status, &o, used, limit)
	now := s.now()
	var retry time.Duration
	more, gating := false, false
	t, err := s.mutate(ctx, id, func(t *Task) ([]NewEvent, error) {
		more, gating, retry = false, false, 0
		tu := t.TurnN(turn)
		if tu == nil || n > len(tu.Attempts) {
			return nil, fmt.Errorf("%w: attempt %d of turn %d", ErrNotFound, n, turn)
		}
		a := tu.Attempts[n-1]
		if a.Status != AttemptRunning {
			return nil, errNoChange
		}
		a.Status, a.EndedAt, a.Error, a.Outcome = status, now, errText, &o
		if !o.Usage.IsZero() {
			u := o.Usage
			a.Usage = &u
		}
		if o.ConversationID != "" {
			a.ConversationID = o.ConversationID
			t.AccountID, t.ConversationID = accountID, o.ConversationID
		}
		finish, said := closingEvents(res.finish)
		evs := make([]NewEvent, 0, len(finish)+4)
		for _, ev := range finish {
			evs = append(evs, NewEvent{Type: api.EventWorker, Turn: turn, Attempt: n, Data: ev})
		}
		endErr := errText
		if said[errText] {
			endErr = "" // the closing events say it already
		}
		evs = append(evs, NewEvent{Type: api.EventAttemptEnd, Turn: turn, Attempt: n,
			Data: api.AttemptEndData{Status: status, Error: endErr}})
		switch status {
		case AttemptQuota:
			evs = append(evs, NewEvent{Type: api.EventQuotaExhausted, Turn: turn, Attempt: n,
				Data: api.AccountProblemData{AccountID: accountID, Group: group, Until: api.FormatTime(until), Error: errText}})
		case AttemptAuth:
			evs = append(evs, NewEvent{Type: api.EventAuthFailed, Turn: turn, Attempt: n,
				Data: api.AccountProblemData{AccountID: accountID, Error: errText}})
		}
		if status == AttemptCancelled && !t.CancelRequested && !t.Manual {
			// Nobody here cancelled it (a cancel file left by an earlier daemon, a launcher stopped from outside):
			// the turn goes on with a new attempt.
			status, errText = AttemptTransient, "the worker was stopped from outside the engine"
			a.Status, a.Error = status, errText
			res.interrupted = true
		}
		switch {
		case status == AttemptCancelled || t.CancelRequested || t.Manual:
			evs = append(evs, s.endTurnChecked(t, tu, TurnCancelled, o, check))
			if !t.Manual {
				t.Status, t.CancelRequested = StatusCancelled, false
			}
		case status == AttemptOK:
			evs = append(evs, s.endTurnChecked(t, tu, TurnOK, o, check))
			t.Status, t.Error = StatusDone, ""
			if tu.Handback {
				tu.HandbackText = o.Response
				evs = append(evs, NewEvent{Type: api.EventHandback, Turn: turn,
					Data: api.HandbackData{Turn: turn, Response: o.Response}})
			}
		case status == AttemptBackgroundWait && tu.BackgroundContinues < 1:
			tu.BackgroundContinues++
			more = true
		case status == AttemptBackgroundWait:
			evs = append(evs, s.endTurnChecked(t, tu, TurnOK, o, check))
			t.Status, t.Error = StatusDone, ""
		case status == AttemptQuota || status == AttemptAuth || status == AttemptCredits:
			more = true
		case status == AttemptTransient:
			k := 0
			for _, x := range tu.Attempts {
				if x.Status == AttemptTransient && x.AccountID == accountID {
					k++
				}
			}
			if delays := retryDelays(); k <= len(delays) && !res.interrupted {
				retry = delays[k-1]
				evs = append(evs, NewEvent{Type: api.EventRetry, Turn: turn, Attempt: n,
					Data: api.RetryData{DelayMs: retry.Milliseconds(), Error: errText}})
			}
			more = true
		default:
			evs = append(evs, s.endTurnChecked(t, tu, TurnFailed, o, check))
			t.Status, t.Error = StatusFailed, errText
		}
		if more {
			msg := ""
			switch counted, rests := attemptBudget(tu, n, status); {
			case status == AttemptQuota && rests >= maxQuotaRests:
				msg = fmt.Sprintf("gave up after %d quota limits in one turn: %s", rests, errText)
			case status != AttemptQuota && counted >= maxAttempts:
				msg = fmt.Sprintf("gave up after %d attempts: %s", counted, errText)
			}
			if msg != "" {
				more, retry = false, 0
				evs = append(evs, s.endTurnChecked(t, tu, TurnFailed, o, check))
				t.Status, t.Error = StatusFailed, msg
			}
		}
		if tu.Status != TurnRunning {
			evs = append(evs, applyCheck(t, tu, check)...)
		}
		if t.Status == StatusDone && wantsGate(t, tu) {
			// The gate runs before the lead sees the turn: the task stays running in its gate phase.
			t.Status = StatusRunning
			t.GatePhase = &GatePhase{State: api.GatePhaseRunning, Turn: turn, Since: now}
			gating = true
		}
		return evs, nil
	})
	if err != nil {
		return false, err
	}
	s.recordUsage(ctx, t, turn, n, accountID, b.Name(), model, o.Usage)
	if (status == AttemptOK || status == AttemptBackgroundWait) && accountID != "" {
		// The account's key and provider work: an API-key account that was not tested counts as working now.
		if err := s.o.Pool.MarkSucceeded(ctx, accountID, attemptStart(t, turn, n)); err != nil {
			s.log.Warn("task: record the account's good run", "account", accountID, "err", err)
		}
	}
	if s.o.QuotaRefresh != nil && accountID != "" {
		// A run that failed on the account (quota, sign-in, transient or other error) always reads its quota
		// again; one that ended well, waits in the background or was cancelled only when the quota is stale.
		failed := status != AttemptOK && status != AttemptBackgroundWait && status != AttemptCancelled
		s.o.QuotaRefresh(accountID, failed)
	}
	if gating {
		return true, nil
	}
	if !more {
		s.lateCheck(ctx, id, turn) // when the turn ended without its checks (they could not run before)
		s.checkDeps(ctx)
		return false, nil
	}
	if retry > 0 {
		s.sleep(r.ctx, retry, nil)
	}
	return true, nil
}

// endTurn closes a turn that has no outcome to record (cancelled, given up) and returns its turn_end event; its
// post-turn checks run later (lateCheck).
func (s *Service) endTurn(t *Task, tu *Turn, status string) NewEvent {
	return s.endTurnChecked(t, tu, status, backend.Outcome{}, nil)
}

// endTurnChecked closes a turn with the outcome of its last attempt and returns its turn_end event. c holds the
// turn's post-turn checks (nil when they did not run): the turn's Files then
// come from its snapshots (checkedFiles), so the turn_end event, the report and the task view list the same files.
// A turn checked later (lateCheck) keeps the edit-tool list in its turn_end event; applyCheck updates its Files.
func (s *Service) endTurnChecked(t *Task, tu *Turn, status string, o backend.Outcome, c *turnCheck) NewEvent {
	tu.Status, tu.EndedAt = status, s.now()
	tu.Response = o.Response
	tu.Files = existingFiles(t.Cwd, displayPaths(t.Cwd, o.FilesEdited))
	if c != nil && tu.Snap != nil && !tu.Snap.Checked {
		tu.Files = checkedFiles(tu.Snap, c, tu.Files)
	}
	tu.Commands, tu.BlockedCommands, tu.DeniedActions = o.Commands, o.BlockedCommands, o.DeniedActions
	var total event.Usage
	for _, a := range tu.Attempts {
		if a.Usage != nil {
			total = total.Add(*a.Usage)
		}
	}
	tu.Usage = nil
	if !total.IsZero() {
		tu.Usage = &total
	}
	for _, a := range tu.Attempts {
		if a.Status == AttemptRunning {
			a.Status, a.EndedAt = AttemptCancelled, tu.EndedAt
		}
	}
	return NewEvent{Type: api.EventTurnEnd, Turn: tu.N, Data: api.TurnEndData{Status: status, Response: tu.Response,
		Files: api.NonNil(tu.Files), Usage: api.UsageOf(tu.Usage)}}
}

// giveUp ends the open turn as failed with message.
func (s *Service) giveUp(id string, turn int, message string) (bool, error) {
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		tu := t.TurnN(turn)
		if tu == nil || tu.Status != TurnRunning {
			return nil, errNoChange
		}
		ev := s.endTurn(t, tu, TurnFailed)
		t.Status, t.Error = StatusFailed, message
		t.Waiting, t.WaitDetail, t.WaitingUntil, t.MemoryWait = "", "", time.Time{}, nil
		t.WaitBudget = nil
		return []NewEvent{ev}, nil
	})
	if err == nil {
		s.lateCheck(s.background(), id, turn)
		s.checkDeps(s.background())
	}
	return false, err
}

// finishCancel ends a cancelled task that has no worker running.
func (s *Service) finishCancel(id string) {
	child := ""
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		child = ""
		if !t.CancelRequested && t.Status == StatusCancelled {
			return nil, errNoChange
		}
		var evs []NewEvent
		if tu := t.LastTurn(); tu != nil && tu.Status == TurnRunning {
			evs = append(evs, s.endTurn(t, tu, TurnCancelled))
		}
		if ph := t.GatePhase; ph != nil {
			child = ph.ChildID
			evs = append(evs, cancelGate(t, ph, s.now())...)
		}
		t.CancelRequested, t.Waiting, t.WaitDetail, t.WaitingUntil, t.MemoryWait = false, "", "", time.Time{}, nil
		t.WaitBudget = nil
		if t.Status != StatusCancelled {
			if err := t.SetStatus(StatusCancelled); err != nil {
				return nil, err
			}
		}
		return evs, nil
	})
	if err != nil {
		s.log.Error("task: finish cancel", "task", id, "err", err)
		return
	}
	if child != "" {
		if _, _, err := s.Cancel(s.background(), child, api.CancelRequest{}); err != nil {
			s.log.Warn("task: cancel the verify task", "task", child, "err", err)
		}
	}
	if t, err := s.repo.Get(s.background(), id); err == nil && t.LastTurn() != nil {
		s.lateCheck(s.background(), id, t.LastTurn().N)
	}
	s.checkDeps(s.background())
}

// setWaiting shows a task as waiting for an account.
func (s *Service) setWaiting(id string, u *pool.Unavailable) {
	u = s.keyProviders(u)
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		if t.CancelRequested || t.Manual || !Active(t.Status) {
			return nil, errNoChange
		}
		detail := u.Error()
		if t.Status == StatusWaitingQuota && t.Waiting == string(u.Reason) && t.WaitingUntil.Equal(u.Until) &&
			t.WaitDetail == detail {
			return nil, errNoChange
		}
		if err := t.SetStatus(StatusWaitingQuota); err != nil {
			return nil, err
		}
		// MemoryWait stays: a pool wait that ends back in a memory wait keeps its start (resources_wait).
		t.Waiting, t.WaitDetail, t.WaitingUntil = string(u.Reason), detail, u.Until.UTC()
		t.WaitBudget = nil
		if u.Reason == pool.ReasonBudget {
			t.WaitBudget = slices.Clone(u.BudgetReached)
		}
		turn := 0
		if tu := t.LastTurn(); tu != nil {
			turn = tu.N
		}
		return []NewEvent{{Type: api.EventWaitingQuota, Turn: turn,
			Data: api.WaitingQuotaData{Until: api.FormatTime(u.Until), Reason: string(u.Reason), Detail: detail}}}, nil
	})
	if err != nil && s.root.Err() == nil {
		s.log.Warn("task: set waiting", "task", id, "err", err)
	}
}

// keyProviders returns u with its free API-key providers checked (field report F14): a disabled or unreadable
// provider is dropped, since delegate refuses it, and one that maps no tier to a model needs an explicit model. The
// task never moves to them by itself: keys cost money, the lead decides.
func (s *Service) keyProviders(u *pool.Unavailable) *pool.Unavailable {
	if len(u.KeyProviders) == 0 {
		return u
	}
	c := *u
	c.KeyProviders = nil
	for _, kp := range u.KeyProviders {
		p, err := s.o.Accounts.GetProvider(s.background(), kp.ID)
		if err != nil || !p.Enabled {
			continue
		}
		kp.NeedsModel = p.Tiers == (config.TierModels{})
		c.KeyProviders = append(c.KeyProviders, kp)
	}
	return &c
}

// sleep waits d, until wake is closed (nil: never), or until ctx ends.
func (s *Service) sleep(ctx context.Context, d time.Duration, wake <-chan struct{}) {
	t := s.clk.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C():
	case <-wake:
	}
}

// attemptBudget returns how much of its budgets turn tu has used once attempt n ends as status: counted is the
// number of its attempts that count toward maxAttempts (all but quota attempts), rests the number of quota attempts.
func attemptBudget(tu *Turn, n int, status string) (counted, rests int) {
	for i, a := range tu.Attempts {
		st := a.Status
		if i == n-1 {
			st = status
		}
		if st == AttemptQuota {
			rests++
		} else {
			counted++
		}
	}
	return counted, rests
}

// maxAttempts is how many attempts one turn may make, quota attempts aside: one per account of the backend plus
// extraAttempts.
func (s *Service) maxAttempts(ctx context.Context, backendName string) int {
	n := 0
	if accs, err := s.o.Accounts.List(ctx); err == nil {
		for i := range accs {
			if accs[i].Backend == backendName {
				n++
			}
		}
	}
	return max(n, 1) + extraAttempts
}

func (s *Service) recordUsage(ctx context.Context, t *Task, turn, n int, accountID, backendName, model string,
	u event.Usage) {
	if s.o.Usage == nil || u.IsZero() || t == nil {
		return
	}
	var started time.Time
	if tu := t.TurnN(turn); tu != nil && n <= len(tu.Attempts) {
		started = tu.Attempts[n-1].StartedAt
	}
	if err := s.o.Usage.RecordAttempt(ctx, usage.Attempt{TaskID: t.ID, Turn: turn, Attempt: n, AccountID: accountID,
		Backend: backendName, Model: model, ProjectKey: t.ProjectKey, ProjectName: t.ProjectName,
		StartedAt: started, EndedAt: s.now(), Usage: u}); err != nil {
		s.log.Warn("task: record usage", "task", t.ID, "err", err)
	}
}

// attemptStart returns when attempt n of the turn started (zero when the task does not have it).
func attemptStart(t *Task, turn, n int) time.Time {
	if t == nil {
		return time.Time{}
	}
	if tu := t.TurnN(turn); tu != nil && n >= 1 && n <= len(tu.Attempts) {
		return tu.Attempts[n-1].StartedAt
	}
	return time.Time{}
}

func (s *Service) attemptDir(id string, turn, n int) string {
	return filepath.Join(s.o.TasksDir, id, fmt.Sprintf("t%d-a%d", turn, n))
}

// excluded lists the accounts this turn must not use again: those that needed a sign-in, and those that failed
// transiently more often than the retries allow.
func excluded(tu *Turn) []string {
	var out []string
	transient := map[string]int{}
	for _, a := range tu.Attempts {
		switch a.Status {
		case AttemptAuth:
			if !slices.Contains(out, a.AccountID) {
				out = append(out, a.AccountID)
			}
		case AttemptTransient:
			transient[a.AccountID]++
		}
	}
	for id, k := range transient {
		if k > len(retryDelays()) && !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	slices.Sort(out)
	return out
}

// classOf is the failure class of an attempt for Pool.Handover.
func classOf(a *Attempt) event.Class {
	if a == nil {
		return ""
	}
	switch a.Status {
	case AttemptQuota:
		return event.ClassQuotaExhausted
	case AttemptAuth:
		return event.ClassAuthRequired
	case AttemptCredits:
		return event.ClassCredits
	case AttemptTransient:
		return event.ClassTransient
	}
	return ""
}

func lastError(tu *Turn) string {
	for i := len(tu.Attempts) - 1; i >= 0; i-- {
		if e := tu.Attempts[i].Error; e != "" {
			return e
		}
	}
	return "every account failed"
}

// prompt builds what the worker gets: the short continuation of its own conversation, or the full brief for a
// fresh conversation (with the handoff section, so a worker on another account never loses the task, old B7).
// planShell is Capabilities.PlanShell of the backend this attempt runs on: a fresh plan conversation is told how
// that backend reads files (brief.PlanRule), whatever backend the task started on.
func (s *Service) prompt(ctx context.Context, t *Task, tu *Turn, continuing, planShell bool) string {
	if Hidden(t.Kind) {
		// A hidden run's first turn carries its whole brief (role, rules, format); later turns (meeting rounds) lean
		// on the conversation. A fresh conversation on a later turn (another account) gets the first brief again.
		// Either fresh conversation starts with the plan rule of the attempt's backend.
		if continuing {
			return tu.Message
		}
		msg := tu.Message
		if tu.N > 1 && len(t.Turns) > 0 && t.Turns[0].Message != "" {
			msg = "## Your brief\n(Sent when this work began; you continue it in a new conversation.)\n\n" +
				t.Turns[0].Message + "\n\n## This turn\n\n" + tu.Message
		}
		if t.Mode != ModePlan {
			return msg
		}
		return brief.ReadOnlyRun(msg, planShell)
	}
	spec := s.briefSpec(t)
	if continuing {
		request := brief.Continue(tu.Message, spec, tu.TaskIDsPassed)
		if tu.ProofChanged {
			if sec := brief.GateSection(s.briefGate(t)); sec != "" {
				request = sec + "\n\n" + request
			}
		}
		if last := tu.LastAttempt(); last != nil {
			switch last.Status {
			case AttemptTransient:
				// A failed attempt that never reported its conversation may not have delivered the request: send it
				// again. One that did gets the retry note, with the request in case it was cut off.
				if last.Outcome == nil || last.Outcome.ConversationID == "" {
					return request
				}
				return brief.RetryRequest(request)
			case AttemptBackgroundWait:
				return brief.BackgroundWait
			}
		}
		return request
	}
	in := brief.Input{Cwd: t.Cwd, Plan: t.Mode == ModePlan, PlanShell: planShell, Spec: spec, Deps: t.DepsContext,
		Interrupted: len(tu.Attempts) > 0, Message: tu.Message, Baseline: displayPaths(t.Cwd, t.Baseline),
		TestParallelism: s.limits().TestParallelism, DataDir: s.o.DataDir}
	in.Lane, in.Gate = s.briefGate(t)
	if tu.Handback {
		in.Message = brief.HandbackElsewhere // a new conversation: the console's context is not available
	}
	if t.Session != nil {
		in.Lead = brief.LeadName(t.Session.Lead)
	}
	if t.Profile != nil && s.o.Roles != nil {
		if r, err := s.o.Roles.Get(ctx, t.Profile.ID, t.projectDir()); err == nil {
			in.Role = s.o.Roles.Brief(r, t.Cwd)
		} else {
			s.log.Warn("task: role", "task", t.ID, "role", t.Profile.ID, "err", err)
		}
	}
	if s.o.Memory != nil {
		if n, err := s.o.Memory.Get(ctx, t.projectDir()); err == nil {
			in.Memory = n.Text
		} else {
			s.log.Warn("task: project memory", "task", t.ID, "err", err)
		}
		if h, err := s.o.Memory.History(ctx, t.projectDir(), memory.HistoryShown); err == nil {
			in.History = h
		} else {
			s.log.Warn("task: review history", "task", t.ID, "err", err)
		}
	}
	in.Constitution = readPrefix(filepath.Join(t.Cwd, ".specify", "memory", "constitution.md"), 4*brief.ConstitutionChars)
	for _, x := range t.Turns {
		if x.N < tu.N {
			in.Earlier = append(in.Earlier, brief.Turn{N: x.N, Message: x.Message, Response: x.Response,
				Files: briefFiles(x.Files)})
		}
	}
	return brief.Worker(in)
}

// readPrefix returns up to max bytes from the start of a file ("" when it cannot be read): the brief keeps only the
// first few thousand characters, so a huge file is never read whole.
func readPrefix(path string, maxBytes int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	data, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return ""
	}
	return strings.ToValidUTF8(string(data), "")
}

// briefSpec is the task's Spec Kit feature as the brief shows it.
func (s *Service) briefSpec(t *Task) *brief.Spec {
	if t.Spec == nil {
		return nil
	}
	sp := &brief.Spec{Dir: t.Spec.Dir, Outside: t.SpecOutside, TaskIDs: t.Spec.TaskIDs}
	if t.SpecOutside {
		for cur := filepath.Clean(t.Spec.Dir); ; {
			candidate := filepath.Join(cur, ".agents", "skills", "speckit-implement", "SKILL.md")
			if st, err := os.Stat(candidate); err == nil && st.Mode().IsRegular() {
				sp.ImplementSkill = filepath.ToSlash(candidate)
				break
			}
			parent := filepath.Dir(cur)
			if parent == cur {
				break
			}
			cur = parent
		}
	}
	return sp
}

// briefFileCap is how many of an earlier turn's files a brief lists.
const briefFileCap = 50

// briefFiles caps an earlier turn's files for the brief: a turn that generated a whole folder lists its first
// files and the count of the rest.
func briefFiles(files []string) []string {
	if len(files) <= briefFileCap {
		return files
	}
	out := slices.Clone(files[:briefFileCap])
	return append(out, fmt.Sprintf("and %d more", len(files)-briefFileCap))
}

// existingFiles drops the display paths (relative to cwd, or absolute) that no longer exist, and sorts the rest
// without duplicates.
func existingFiles(cwd string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		abs := filepath.FromSlash(p)
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(cwd, abs)
		}
		if _, err := os.Lstat(abs); err == nil {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// displayPaths makes paths relative to cwd with '/' separators; paths outside it stay absolute.
func displayPaths(cwd string, paths []string) []string {
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if filepath.IsAbs(p) {
			if rel, ok := relInside(cwd, p); ok {
				p = rel
			}
		}
		out = append(out, filepath.ToSlash(p))
	}
	return out
}

// relInside returns p relative to base when p lies inside base, comparing canonical paths (8.3 short names,
// junctions and letter case on Windows).
func relInside(base, p string) (string, bool) {
	if c, err := fsutil.Canonical(base); err == nil {
		base = c
	}
	if c, err := fsutil.Canonical(p); err == nil {
		p = c
	}
	rel, err := filepath.Rel(base, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", false
	}
	return rel, true
}

// prependPath puts dir first on the PATH of env (the variable is matched without case, as Windows spells it Path).
func prependPath(env []string, dir string) []string {
	out := append([]string(nil), env...)
	for i, kv := range out {
		if eq := strings.IndexByte(kv, '='); eq > 0 && strings.EqualFold(kv[:eq], "PATH") {
			if kv[eq+1:] == "" {
				out[i] = kv[:eq+1] + dir
			} else {
				out[i] = kv[:eq+1] + dir + string(os.PathListSeparator) + kv[eq+1:]
			}
			return out
		}
	}
	return append(out, "PATH="+dir)
}
