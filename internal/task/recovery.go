package task

import (
	"context"
	"errors"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/launch"
)

// recoverTasks picks up what an earlier daemon left (old §8): every active task gets a run, which first resumes an
// attempt that was running (resumeAttempt); takeover consoles are watched again; dependents are re-evaluated.
// Nothing is killed: a worker whose launch cannot be attached is left alone, and no process id is ever signaled.
func (s *Service) recoverTasks(ctx context.Context) error {
	ids, err := s.repo.IDs(ctx, StatusQueued, StatusRunning, StatusWaitingQuota)
	if err != nil {
		return err
	}
	for _, id := range ids {
		t, err := s.repo.Get(ctx, id)
		if err != nil {
			return err
		}
		if t.Manual {
			if t.Console != nil {
				s.watchConsole(t.ID, t.Console, nil)
			}
			continue
		}
		s.startRun(id)
	}
	s.checkDeps(ctx)
	return nil
}

// resumeAttempt continues an attempt that was running when the daemon stopped. A live worker is attached again and
// counted on its account exactly once (pool.Reattach, old B3); a finished one is replayed from its files; an
// attempt whose worker never started is closed as interrupted so the turn goes on with a new attempt, which gets the
// full brief when it lands on another account (old B7).
func (s *Service) resumeAttempt(r *run, t *Task, tu *Turn, a *Attempt) (bool, error) {
	ctx := s.background()
	b, err := s.backend(t.Backend)
	if err != nil {
		return false, err
	}
	var h *launch.Handle
	if a.LaunchDir != "" {
		h, err = launch.Attach(a.LaunchDir)
		if err != nil && !errors.Is(err, launch.ErrNotLaunch) {
			s.log.Warn("task: attach", "task", t.ID, "dir", a.LaunchDir, "err", err)
		}
	}
	if h == nil {
		return s.finishAttempt(r, t.ID, tu.N, a.N, a.AccountID, a.Model, b, attemptResult{
			outcome: backend.Outcome{Status: backend.OutcomeFailed, Class: event.ClassTransient,
				Error: "interrupted: the daemon stopped before the worker started"},
			interrupted: true})
	}
	_, ended, err := h.Exited()
	if err != nil {
		return false, err
	}
	typ := api.EventResumed
	if !ended {
		lease := s.o.Pool.Reattach(a.AccountID, t.ID)
		defer lease.Release()
		typ = api.EventReattach
	}
	if err := s.appendEvents(ctx, t.ID, []NewEvent{{Type: typ, Turn: tu.N, Attempt: a.N,
		Data: api.RecoveryData{AccountID: a.AccountID, Offset: a.StreamOffset}}}, nil); err != nil {
		return false, err
	}
	var secrets []string
	if acc, err := s.o.Accounts.Get(ctx, a.AccountID); err == nil {
		if env, err := s.o.Accounts.Env(ctx, acc, b, account.EnvOptions{Base: []string{}, Model: a.Model}); err == nil {
			secrets = env.Secrets
		}
	}
	r.setHandle(h, a.AccountID, a.StartedAt)
	defer r.clearHandle()
	if t.CancelRequested {
		_ = h.Cancel()
	}
	res, err := s.follow(r, h, b, secrets, t.ID, tu.N, a.N, Offsets{Stream: a.StreamOffset, Stderr: a.StderrOffset})
	if err != nil {
		return false, err
	}
	return s.finishAttempt(r, t.ID, tu.N, a.N, a.AccountID, a.Model, b, res)
}
