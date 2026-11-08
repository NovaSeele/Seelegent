package task

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/brief"
)

// depsState says where a dependent stands given its prerequisites: ready, waiting or blocked, and why (in English
// for the lead, and as params for a blocked one).
func depsState(after []string, mode string, get func(id string) (*Task, error)) (state, reason string,
	block *api.AttentionParams, err error) {
	var waitingOn []string
	for _, id := range after {
		d, err := get(id)
		if errors.Is(err, ErrNotFound) {
			return api.DepsBlocked, fmt.Sprintf("prerequisite %s not found", id),
				&api.AttentionParams{Kind: api.AttentionDepMissing, Task: id}, nil
		}
		if err != nil {
			return "", "", nil, err
		}
		verdict := ""
		if d.Review != nil {
			verdict = d.Review.Verdict
		}
		switch {
		case verdict == api.VerdictAccepted && d.Status == StatusDone,
			mode == AfterDone && d.Status == StatusDone && verdict != api.VerdictRejected:
			continue
		case verdict == api.VerdictRejected:
			return api.DepsBlocked, fmt.Sprintf("prerequisite %s (%s) was rejected", id, d.Title),
				&api.AttentionParams{Kind: api.AttentionDepRejected, Task: id, Title: d.Title}, nil
		case d.Status == StatusFailed || d.Status == StatusCancelled:
			kind := api.AttentionDepFailed
			if d.Status == StatusCancelled {
				kind = api.AttentionDepCancelled
			}
			return api.DepsBlocked, fmt.Sprintf("prerequisite %s (%s) was %s", id, d.Title, d.Status),
				&api.AttentionParams{Kind: kind, Task: id, Title: d.Title}, nil
		}
		waitingOn = append(waitingOn, id)
	}
	if len(waitingOn) > 0 {
		return api.DepsWaiting, "waiting for " + strings.Join(waitingOn, ", "), nil, nil
	}
	return api.DepsReady, "", nil, nil
}

// checkDeps re-evaluates every waiting or blocked dependent: it starts those whose prerequisites are met, blocks
// those with a failed, cancelled or rejected prerequisite, and lets a blocked one wait again when the prerequisite
// was reopened. Runs are serialized, so two changes never start a dependent twice.
func (s *Service) checkDeps(ctx context.Context) {
	s.depsMu.Lock()
	defer s.depsMu.Unlock()
	ids, err := s.repo.IDs(ctx, StatusWaitingDeps, StatusBlockedDeps)
	if err != nil {
		s.log.Warn("task: check dependencies", "err", err)
		return
	}
	for _, id := range ids {
		start := false
		_, err := s.mutate(ctx, id, func(t *Task) ([]NewEvent, error) {
			start = false
			if t.Status != StatusWaitingDeps && t.Status != StatusBlockedDeps {
				return nil, errNoChange
			}
			state, reason, block, err := depsState(t.After, t.AfterMode, func(id string) (*Task, error) {
				return s.repo.Get(ctx, id)
			})
			if err != nil {
				return nil, err
			}
			switch state {
			case api.DepsReady:
				evs, err := s.startFromDeps(ctx, t, false)
				start = err == nil
				return evs, err
			case api.DepsBlocked:
				if t.Status == StatusBlockedDeps && t.DepsReason == reason && t.DepsBlock != nil {
					return nil, errNoChange
				}
				t.Status, t.DepsReason, t.DepsBlock = StatusBlockedDeps, reason, block
			default:
				if t.Status == StatusWaitingDeps && t.DepsReason == reason {
					return nil, errNoChange
				}
				t.Status, t.DepsReason, t.DepsBlock = StatusWaitingDeps, reason, nil
			}
			return []NewEvent{{Type: api.EventDeps, Turn: 1,
				Data: api.DepsData{State: state, After: t.After, Reason: reason}}}, nil
		})
		if err != nil {
			s.log.Warn("task: check dependencies", "task", id, "err", err)
			continue
		}
		if start {
			s.startRun(id)
		}
	}
}

// startFromDeps queues a dependent: it fixes the context from its prerequisites for the brief and returns the deps
// event. The caller holds the task's lock (inside mutate) and starts the run afterwards.
func (s *Service) startFromDeps(ctx context.Context, t *Task, forced bool) ([]NewEvent, error) {
	var deps []brief.Dep
	for _, id := range t.After {
		d, err := s.repo.Get(ctx, id)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		dep := brief.Dep{ID: d.ID, Title: d.Title, Status: string(d.Status)}
		if d.Review != nil {
			dep.Status = d.Review.Verdict
		}
		if tu := d.LastTurn(); tu != nil {
			dep.Response = tu.Response
		}
		deps = append(deps, dep)
	}
	t.DepsContext = brief.DepsContext(deps)
	t.DepsReason, t.DepsBlock = "", nil
	if err := t.SetStatus(StatusQueued); err != nil {
		return nil, err
	}
	return []NewEvent{{Type: api.EventDeps, Turn: 1,
		Data: api.DepsData{State: api.DepsReady, After: t.After, Forced: forced}}}, nil
}
