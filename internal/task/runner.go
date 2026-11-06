package task

import (
	"context"
	"errors"
	"fmt"
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

// extraAttempts is how many attempts a turn may make beyond one per account.
const extraAttempts = 3

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
	account string
	live    liveState
}

// setHandle records the worker being followed; a run that was already asked to stop cancels it at once.
func (r *run) setHandle(h *launch.Handle, accountID string, started time.Time) {
	r.mu.Lock()
	r.handle, r.account = h, accountID
	r.live = liveState{running: true, started: started, account: accountID}
	stopped := r.stopped
	r.mu.Unlock()
	if stopped {
		_ = h.Cancel()
	}
}

func (r *run) clearHandle() {
	r.mu.Lock()
	r.handle, r.account, r.live = nil, "", liveState{}
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

// note updates what is known about the worker from one of its events.
func (r *run) note(ev event.Event, now time.Time) {
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
		}
	case event.KindToolResult:
		l.tool, l.command, l.toolSince = "", "", time.Time{}
	case event.KindMessage:
		l.waiting, l.step = time.Time{}, "writing"
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
	return r.live
}

// startRun starts the run of task id unless one is going.
func (s *Service) startRun(id string) {
	s.mu.Lock()
	if s.closed || s.runs[id] != nil {
		s.mu.Unlock()
		return
	}
	ctx, stop := context.WithCancel(s.root)
	stopCtx, stopLaunch := context.WithCancel(context.Background())
	r := &run{id: id, ctx: ctx, stop: stop, stopCtx: stopCtx, stopLaunch: stopLaunch, done: make(chan struct{})}
	s.runs[id] = r
	s.mu.Unlock()
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
		if tu := t.LastTurn(); tu != nil && tu.Status == TurnRunning {
			s.startRun(r.id)
		}
	}
}

// runTask works on the task until its open turn ended, it was cancelled or taken over, or the daemon stops.
func (s *Service) runTask(r *run) {
	baselineTried := false
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
		case !t.BaselineTaken && !baselineTried && tu.N == 1 && len(tu.Attempts) == 0:
			baselineTried = true
			s.takeBaseline(r.ctx, t)
			continue
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
// worker ran, so the brief tells the worker not to revert them (old A10).
func (s *Service) takeBaseline(ctx context.Context, t *Task) {
	root, files := captureBaseline(ctx, t.Cwd)
	if ctx.Err() != nil {
		return
	}
	_, err := s.mutate(ctx, t.ID, func(cur *Task) ([]NewEvent, error) {
		if cur.BaselineTaken {
			return nil, errNoChange
		}
		cur.BaselineTaken, cur.Baseline = true, files
		if cur.RepoRoot == "" {
			cur.RepoRoot = root
		}
		return nil, nil
	})
	if err != nil && ctx.Err() == nil {
		s.log.Warn("task: baseline", "task", t.ID, "err", err)
	}
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
	if owner := t.AccountID; owner != "" && !slices.Contains(req.Exclude, owner) {
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
	lease, choice, err := s.o.Pool.Wait(ctx, req, func(u *pool.Unavailable) { s.setWaiting(t.ID, u) })
	if err != nil {
		if ctx.Err() != nil {
			return true, nil
		}
		if errors.Is(err, pool.ErrNoAccount) {
			if len(req.Exclude) > 0 {
				return s.giveUp(t.ID, tu.N, "no other account can run this task: "+lastError(tu))
			}
			s.setWaiting(t.ID, &pool.Unavailable{Reason: pool.ReasonNoAccount, Backend: t.Backend, Group: req.Group})
			s.sleep(ctx, noAccountRecheck, s.o.Pool.Changed())
			return true, nil
		}
		return false, err
	}
	defer lease.Release()

	acc := choice.Account
	attemptModel := choice.Model
	if attemptModel == "" {
		attemptModel = model
	}
	continuing := t.ConversationID != "" && acc.ID == t.AccountID
	prompt := s.prompt(ctx, t, tu, continuing)
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
		t.Waiting, t.WaitDetail, t.WaitingUntil = "", "", time.Time{}
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
	env, err := s.o.Accounts.Env(ctx, acc, b, account.EnvOptions{Base: s.o.BaseEnv(), Model: model,
		AllowGit: t.AllowGit})
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
	h, err := launch.Start(r.stopCtx, launch.Options{Launcher: s.o.Launcher, Dir: launchDir, Spec: spec,
		Env: env.Vars})
	if err != nil {
		return failed(event.ClassFatal, fmt.Errorf("start the worker: %w", err))
	}
	r.setHandle(h, acc.ID, s.clk.Now())
	defer r.clearHandle()
	return s.follow(r, h, b, env.Secrets, t.ID, turn, n, Offsets{Turn: turn, Attempt: n})
}

// classify maps an outcome to an attempt status and its error text.
func classify(o backend.Outcome, exit launch.Exit, timeoutMinutes int) (status, text string) {
	switch o.Status {
	case backend.OutcomeOK:
		return AttemptOK, ""
	case backend.OutcomeCanceled:
		return AttemptCancelled, ""
	case backend.OutcomeBackgroundWait:
		return AttemptBackgroundWait, ""
	case backend.OutcomeFailed:
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
	set := s.settings()
	o := res.outcome
	status, errText := classify(o, res.exit, set.TaskTimeoutMinutes)
	group := b.QuotaGroup(model)
	var until time.Time
	switch status {
	case AttemptQuota:
		cd, err := s.o.Pool.MarkExhausted(ctx, accountID, pool.Exhaustion{Group: group, Model: model, Reason: errText})
		if err != nil {
			s.log.Warn("task: rest the account", "account", accountID, "err", err)
		}
		until = cd.Until
	case AttemptAuth:
		if err := s.o.Pool.MarkAuthFailed(ctx, accountID, errText); err != nil {
			s.log.Warn("task: mark the account signed out", "account", accountID, "err", err)
		}
	}
	maxAttempts := s.maxAttempts(ctx, b.Name())
	now := s.now()
	var retry time.Duration
	more := false
	t, err := s.mutate(ctx, id, func(t *Task) ([]NewEvent, error) {
		more, retry = false, 0
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
		evs := make([]NewEvent, 0, len(res.finish)+4)
		for _, ev := range res.finish {
			evs = append(evs, NewEvent{Type: api.EventWorker, Turn: turn, Attempt: n, Data: ev})
		}
		evs = append(evs, NewEvent{Type: api.EventAttemptEnd, Turn: turn, Attempt: n,
			Data: api.AttemptEndData{Status: status, Error: errText}})
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
			evs = append(evs, s.endTurn(t, tu, TurnCancelled, o))
			if !t.Manual {
				t.Status, t.CancelRequested = StatusCancelled, false
			}
		case status == AttemptOK:
			evs = append(evs, s.endTurn(t, tu, TurnOK, o))
			t.Status, t.Error = StatusDone, ""
			if tu.Handback {
				tu.HandbackOf = o.Response
				evs = append(evs, NewEvent{Type: api.EventHandback, Turn: turn,
					Data: api.HandbackData{Turn: turn, Response: o.Response}})
			}
		case status == AttemptBackgroundWait && tu.BackgroundContinues < 1:
			tu.BackgroundContinues++
			more = true
		case status == AttemptBackgroundWait:
			evs = append(evs, s.endTurn(t, tu, TurnOK, o))
			t.Status, t.Error = StatusDone, ""
		case status == AttemptQuota || status == AttemptAuth:
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
			evs = append(evs, s.endTurn(t, tu, TurnFailed, o))
			t.Status, t.Error = StatusFailed, errText
		}
		if more && len(tu.Attempts) >= maxAttempts {
			more, retry = false, 0
			msg := fmt.Sprintf("gave up after %d attempts: %s", len(tu.Attempts), errText)
			evs = append(evs, s.endTurn(t, tu, TurnFailed, o))
			t.Status, t.Error = StatusFailed, msg
		}
		return evs, nil
	})
	if err != nil {
		return false, err
	}
	s.recordUsage(ctx, t, turn, n, accountID, b.Name(), model, o.Usage)
	if s.o.QuotaRefresh != nil && accountID != "" {
		s.o.QuotaRefresh(accountID)
	}
	if !more {
		s.checkDeps(ctx)
		return false, nil
	}
	if retry > 0 {
		s.sleep(r.ctx, retry, nil)
	}
	return true, nil
}

// endTurn closes a turn with the outcome of its last attempt and returns its turn_end event.
func (s *Service) endTurn(t *Task, tu *Turn, status string, o backend.Outcome) NewEvent {
	tu.Status, tu.EndedAt = status, s.now()
	tu.Response = o.Response
	tu.Files = displayPaths(t.Cwd, o.FilesEdited)
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
		ev := s.endTurn(t, tu, TurnFailed, backend.Outcome{})
		t.Status, t.Error = StatusFailed, message
		t.Waiting, t.WaitDetail, t.WaitingUntil = "", "", time.Time{}
		return []NewEvent{ev}, nil
	})
	if err == nil {
		s.checkDeps(s.background())
	}
	return false, err
}

// finishCancel ends a cancelled task that has no worker running.
func (s *Service) finishCancel(id string) {
	_, err := s.mutate(s.background(), id, func(t *Task) ([]NewEvent, error) {
		if !t.CancelRequested && t.Status == StatusCancelled {
			return nil, errNoChange
		}
		var evs []NewEvent
		if tu := t.LastTurn(); tu != nil && tu.Status == TurnRunning {
			evs = append(evs, s.endTurn(t, tu, TurnCancelled, backend.Outcome{}))
		}
		t.CancelRequested, t.Waiting, t.WaitDetail, t.WaitingUntil = false, "", "", time.Time{}
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
	s.checkDeps(s.background())
}

// setWaiting shows a task as waiting for an account.
func (s *Service) setWaiting(id string, u *pool.Unavailable) {
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
		t.Waiting, t.WaitDetail, t.WaitingUntil = string(u.Reason), detail, u.Until.UTC()
		turn := 0
		if tu := t.LastTurn(); tu != nil {
			turn = tu.N
		}
		return []NewEvent{{Type: api.EventWaitingQuota, Turn: turn,
			Data: api.WaitingQuotaData{Until: api.FormatTime(u.Until)}}}, nil
	})
	if err != nil && s.root.Err() == nil {
		s.log.Warn("task: set waiting", "task", id, "err", err)
	}
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

// maxAttempts is how many attempts one turn may make: one per account of the backend plus extraAttempts.
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
func (s *Service) prompt(ctx context.Context, t *Task, tu *Turn, continuing bool) string {
	spec := s.briefSpec(t)
	if continuing {
		request := brief.Continue(tu.Message, spec, tu.TaskIDsPassed)
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
	in := brief.Input{Cwd: t.Cwd, Plan: t.Mode == ModePlan, Spec: spec, Deps: t.DepsContext,
		Interrupted: len(tu.Attempts) > 0, Message: tu.Message, Baseline: displayPaths(t.Cwd, t.Baseline)}
	if t.Session != nil {
		in.Lead = brief.LeadName(t.Session.HostID)
	}
	if t.Profile != nil && s.o.Roles != nil {
		if r, err := s.o.Roles.Get(ctx, t.Profile.ID, t.Cwd); err == nil {
			in.Role = s.o.Roles.Brief(r, t.Cwd)
		} else {
			s.log.Warn("task: role", "task", t.ID, "role", t.Profile.ID, "err", err)
		}
	}
	if s.o.Memory != nil {
		if n, err := s.o.Memory.Get(ctx, t.Cwd); err == nil {
			in.Memory = n.Text
		} else {
			s.log.Warn("task: project memory", "task", t.ID, "err", err)
		}
		if h, err := s.o.Memory.History(ctx, t.Cwd, memory.HistoryShown); err == nil {
			in.History = h
		} else {
			s.log.Warn("task: review history", "task", t.ID, "err", err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(t.Cwd, ".specify", "memory", "constitution.md")); err == nil {
		in.Constitution = string(data)
	}
	for _, x := range t.Turns {
		if x.N < tu.N {
			in.Earlier = append(in.Earlier, brief.Turn{N: x.N, Message: x.Message, Response: x.Response, Files: x.Files})
		}
	}
	return brief.Worker(in)
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
