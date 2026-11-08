package app

import (
	"context"
	"io"
	"math"
	"path/filepath"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/contest"
	"github.com/NovaSeele/Seelegent/internal/hook"
	"github.com/NovaSeele/Seelegent/internal/httpapi"
	"github.com/NovaSeele/Seelegent/internal/logx"
	"github.com/NovaSeele/Seelegent/internal/meeting"
	"github.com/NovaSeele/Seelegent/internal/notes"
	"github.com/NovaSeele/Seelegent/internal/pool"
	"github.com/NovaSeele/Seelegent/internal/task"
)

// hooks.log rotation: 2 MiB, two old copies.
const (
	hooksLogMax  = 2 << 20
	hooksLogKeep = 2
)

// buildTeam wires the team services (milestone 14) after the task engine: review notes, meetings, contests and
// hooks. The members store is built before the task engine, which uses it.
func (a *App) buildTeam(now func() time.Time) error {
	a.Notes = notes.New(notes.Options{DB: a.DB, Tasks: a.Tasks, Root: a.Git.Root, Now: now,
		Changed: func(id string) { a.withAPI(func(s *httpapi.Server) { s.PublishNotes(id) }) }})
	a.Meetings = meeting.New(meeting.Options{DB: a.DB, Tasks: a.Tasks, Members: a.Members, Root: a.Git.Root,
		ConsultStats: a.Tasks.ConsultStats, Now: now, Logger: a.log,
		Changed: func(m api.MeetingSummary) { a.withAPI(func(s *httpapi.Server) { s.PublishMeeting(m) }) }})
	a.Contests = contest.New(contest.Options{DB: a.DB, Tasks: a.Tasks, Members: a.Members, Git: a.Git,
		Accounts: a.contestAccounts, Settings: a.Settings,
		SharedDirs: func(root string) []string {
			if a.Tickets == nil {
				return nil
			}
			return a.Tickets.SharedDirs(root)
		},
		Dir:          filepath.Join(a.Paths.Home, "contests"),
		WorktreeBase: filepath.Join(a.Paths.Home, "worktrees", "contests"),
		Now:          now, Logger: a.log,
		Changed: func(c api.ContestSummary) { a.withAPI(func(s *httpapi.Server) { s.PublishContest(c) }) }})
	hookLog := io.Discard
	if f, err := logx.OpenRotatingFile(filepath.Join(a.Paths.Logs, "hooks.log"), hooksLogMax, hooksLogKeep); err != nil {
		a.log.Warn("app: open hooks.log; hook runs are not logged", "err", err)
	} else {
		hookLog, a.hookLog = f, f
	}
	port, err := a.Port()
	if err != nil {
		port = a.Settings().Port
	}
	a.Hooks = hook.New(hook.Options{DB: a.DB, Tasks: a.Tasks, Port: port, Log: hookLog, Redact: a.redact,
		Now: now, Logger: a.log,
		Ran: func(r api.HookRun) { a.withAPI(func(s *httpapi.Server) { s.PublishHookRun(r) }) }})
	return nil
}

// startTeam resumes meetings and contests and starts the hooks. Failures are logged: the daemon runs without them.
func (a *App) startTeam(ctx, runCtx context.Context) {
	if err := a.Meetings.Start(ctx); err != nil {
		a.log.Warn("app: resume meetings", "err", err)
	}
	if err := a.Contests.Start(ctx); err != nil {
		a.log.Warn("app: resume contests", "err", err)
	}
	reattached := false
	if page, err := a.Tasks.List(ctx, task.Query{Statuses: []task.Status{task.StatusRunning}, Limit: 1}); err == nil {
		reattached = len(page.Items) > 0
	}
	a.Hooks.Start(reattached)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		a.watchAccountHooks(runCtx)
	}()
}

// closeTeam stops the team services (before the task engine).
func (a *App) closeTeam(ctx context.Context) {
	a.Hooks.Close(ctx)
	a.Contests.Close()
	a.Meetings.Close()
	if a.hookLog != nil {
		_ = a.hookLog.Close()
	}
}

// withAPI runs fn on the HTTP API once it runs (events before it are only visible through the lists).
func (a *App) withAPI(fn func(s *httpapi.Server)) {
	a.mu.Lock()
	srv := a.api
	a.mu.Unlock()
	if srv != nil {
		fn(srv)
	}
}

// contestEnded is task.Options.ContestEnded; the contests are built after the task engine.
func (a *App) contestEnded(ctx context.Context, id string) (status string, ended bool, err error) {
	if a.Contests == nil {
		return "", false, nil
	}
	return a.Contests.Ended(ctx, id)
}

// contestAccounts lists the accounts for a contest's quota plan: usable when enabled and in a state the pool runs
// work on (an error state keeps its last numbers and still runs, pool.Status.Usable) or about to (new), with the
// lowest fraction left over their quota windows.
func (a *App) contestAccounts(ctx context.Context) ([]contest.PoolAccount, error) {
	states, err := a.Pool.States(ctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := make([]contest.PoolAccount, 0, len(states))
	for i := range states {
		st := &states[i]
		pa := contest.PoolAccount{ID: st.Account.ID, Backend: st.Account.Backend, Provider: st.Account.Provider,
			Usable: st.Account.Enabled && (st.Status.Usable() || st.Status == pool.StatusNew), Busy: st.Busy}
		if st.HasQuota && st.Quota.Known() {
			left := 1.0
			for _, g := range st.Quota.Groups {
				for _, w := range g.Windows {
					left = math.Min(left, w.RemainingAt(now))
				}
			}
			if !st.Quota.ExhaustedUntil.IsZero() && st.Quota.ExhaustedUntil.After(now) {
				left = 0
			}
			pa.Remaining = &left
		}
		out = append(out, pa)
	}
	return out, nil
}

// watchAccountHooks fires account.needs_login and account.exhausted when an account turns so (not at start: only
// changes count).
func (a *App) watchAccountHooks(ctx context.Context) {
	type seen struct{ login, exhausted bool }
	last := map[string]seen{}
	first := true
	for {
		ch := a.Pool.Changed()
		if states, err := a.Pool.States(ctx); err == nil {
			now := time.Now()
			next := map[string]seen{}
			for i := range states {
				st := &states[i]
				cur := seen{login: st.Status == pool.StatusNeedsLogin,
					exhausted: st.HasQuota && st.Quota.ExhaustedUntil.After(now)}
				next[st.Account.ID] = cur
				old := last[st.Account.ID]
				if first {
					continue
				}
				data := map[string]any{"account": st.Account.ID, "backend": st.Account.Backend}
				if cur.login && !old.login {
					a.Hooks.Fire(api.HookAccountLogin, "", data)
				}
				if cur.exhausted && !old.exhausted {
					data["until"] = api.FormatTime(st.Quota.ExhaustedUntil)
					a.Hooks.Fire(api.HookAccountExhausted, "", data)
				}
			}
			last, first = next, false
		}
		select {
		case <-ctx.Done():
			return
		case <-ch:
		case <-time.After(time.Minute): // an exhaustion can also start without a pool notice
		}
	}
}
