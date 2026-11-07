package task

import (
	"context"
	"sort"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/platform"
)

// Event page limits (contract §3).
const (
	defaultEventLimit = 500
	maxEventLimit     = 2000
)

// Summary returns the summary of task id.
func (s *Service) Summary(ctx context.Context, id string) (api.TaskSummary, error) {
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return api.TaskSummary{}, err
	}
	return s.summary(t), nil
}

// Get returns task id with its turns.
func (s *Service) Get(ctx context.Context, id string) (api.TaskDetail, error) {
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return api.TaskDetail{}, err
	}
	d := api.TaskDetail{TaskSummary: s.summary(t), Prompt: t.Prompt, RepoRoot: t.RepoRoot, Error: t.Error,
		Turns: make([]api.Turn, 0, len(t.Turns))}
	for _, tu := range t.Turns {
		at := api.Turn{N: tu.N, By: tu.By, Message: tu.Message, Status: tu.Status, Model: tu.Model,
			RequestedModel: tu.RequestedModel, Tier: tu.Tier, Effort: tu.Effort, StartedAt: api.FormatTime(tu.StartedAt),
			EndedAt: api.FormatTime(tu.EndedAt), Response: tu.Response, Files: api.NonNil(tu.Files),
			Commands: api.NonNil(tu.Commands), BlockedCommands: api.NonNil(tu.BlockedCommands),
			DeniedActions: api.NonNil(tu.DeniedActions), Usage: api.UsageOf(tu.Usage), Handback: tu.HandbackText,
			Attempts: make([]api.Attempt, 0, len(tu.Attempts)), WorkAtRisk: api.NonNil(tu.WorkAtRisk),
			Hygiene: api.NonNil(tu.Hygiene), Scope: api.NonNil(tu.Scope), OutsideDiff: tu.Snap != nil}
		if tu.Snap != nil && tu.Snap.Checked {
			at.Git = &api.TurnGit{Changed: api.NonNil(tu.Changed), Preexisting: api.NonNil(tu.Preexisting)}
		}
		for _, a := range tu.Attempts {
			at.Attempts = append(at.Attempts, api.Attempt{AccountID: a.AccountID, AccountLabel: a.AccountLabel,
				Model: a.Model, StartedAt: api.FormatTime(a.StartedAt), EndedAt: api.FormatTime(a.EndedAt),
				Status: a.Status, Kind: a.Kind, Error: a.Error, Usage: api.UsageOf(a.Usage)})
		}
		d.Turns = append(d.Turns, at)
	}
	d.FileOverlaps = api.NonNil(s.fileOverlaps(ctx, t))
	return d, nil
}

// List returns one page of task summaries, newest first.
func (s *Service) List(ctx context.Context, q Query) (api.TaskPage, error) {
	tasks, next, err := s.repo.List(ctx, q)
	if err != nil {
		return api.TaskPage{}, err
	}
	page := api.TaskPage{Items: make([]api.TaskSummary, 0, len(tasks)), NextCursor: next}
	for _, t := range tasks {
		page.Items = append(page.Items, s.summary(t))
	}
	return page, nil
}

// Events returns the events of task id with seq > after (limit 1..2000, 500 when 0), or with tail > 0 the newest
// tail events (old B6: the dashboard opens at the end of a long log). Both are ascending by seq.
func (s *Service) Events(ctx context.Context, id string, after int64, limit, tail int) (api.EventPage, error) {
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return api.EventPage{}, err
	}
	var evs []api.TaskEvent
	switch {
	case tail < 0 || tail > maxEventLimit:
		return api.EventPage{}, invalidf("tail must be 1..%d", maxEventLimit)
	case tail > 0:
		evs, err = s.repo.Tail(ctx, id, tail)
	default:
		if limit == 0 {
			limit = defaultEventLimit
		}
		if limit < 1 || limit > maxEventLimit {
			return api.EventPage{}, invalidf("limit must be 1..%d", maxEventLimit)
		}
		if after < 0 {
			return api.EventPage{}, invalidf("after must not be negative")
		}
		evs, err = s.repo.Events(ctx, id, after, limit)
	}
	if err != nil {
		return api.EventPage{}, err
	}
	// The task was read before the events: an event appended in between must not be newer than lastSeq.
	last := t.LastEventSeq
	if n := len(evs); n > 0 && evs[n-1].Seq > last {
		last = evs[n-1].Seq
	}
	return api.EventPage{Items: api.NonNil(evs), LastSeq: last}, nil
}

// Prompt returns the full brief of attempt n of turn (the latest attempt when n is 0).
func (s *Service) Prompt(ctx context.Context, id string, turn, n int) (string, error) {
	if _, err := s.repo.Get(ctx, id); err != nil {
		return "", err
	}
	return s.repo.Prompt(ctx, id, turn, n)
}

// LiveWorkers lists the worker process trees the engine follows right now (for the worker guard, contract §1.3):
// the task, the account and the launch directory, whose launch.json names the tree.
func (s *Service) LiveWorkers() []api.LiveWorker {
	s.mu.Lock()
	runs := make([]*run, 0, len(s.runs))
	for _, r := range s.runs {
		runs = append(runs, r)
	}
	s.mu.Unlock()
	out := []api.LiveWorker{}
	for _, r := range runs {
		r.mu.Lock()
		if r.handle != nil {
			out = append(out, api.LiveWorker{TaskID: r.id, AccountID: r.account, LaunchDir: r.handle.Files().Dir})
		}
		r.mu.Unlock()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TaskID < out[j].TaskID })
	return out
}

// WorkerTrees returns the process trees of the workers the engine runs right now, for the worker guard (contract
// §1.3): one per task, with the task id as the tree's ID. A tree is known from the moment its launcher runs (and,
// after a daemon restart, from Start on), from what this daemon recorded when it started the launcher or from a
// launch record that checks out (launch.Handle.Tree); a tree that cannot be verified lists nothing, so the guard
// fails closed. Takeover consoles are the user's and are not listed.
func (s *Service) WorkerTrees() []*platform.WorkerTree {
	s.mu.Lock()
	runs := make([]*run, 0, len(s.runs))
	for _, r := range s.runs {
		runs = append(runs, r)
	}
	s.mu.Unlock()
	var out []*platform.WorkerTree
	for _, r := range runs {
		h := r.tree()
		if h == nil {
			continue
		}
		if t, ok := h.Tree(); ok {
			t.ID = r.id
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// summary converts a task into its summary, with what the worker does right now.
func (s *Service) summary(t *Task) api.TaskSummary {
	sum := api.TaskSummary{
		ID: t.ID, Title: t.Title, Status: t.Status, Kind: t.Kind, Mode: t.Mode, Cwd: t.Cwd, ProjectKey: t.ProjectKey,
		ProjectName: t.ProjectName, Backend: t.Backend, Provider: t.Provider, Model: t.Model, Effort: t.Effort,
		Tier: t.Tier, AccountID: t.AccountID, Member: t.Member, Role: t.Role, Profile: t.Profile, Session: t.Session,
		TurnCount: len(t.Turns), TokensUsed: t.TokensUsed(), FilesChanged: len(t.FilesChanged()),
		CreatedAt: api.FormatTime(t.CreatedAt), UpdatedAt: api.FormatTime(t.UpdatedAt),
		LastEventAt: api.FormatTime(t.LastEventAt), WaitingUntil: api.FormatTime(t.WaitingUntil),
		NeedsSince: api.FormatTime(t.NeedsSince), Attention: t.Attention, Review: t.Review, Spec: t.Spec,
		After: api.NonNil(t.After), AfterMode: t.AfterMode, DepsReason: t.DepsReason, Manual: t.Manual,
		ParentID: t.ParentID, ContestID: t.ContestID, TicketID: t.TicketID, MeetingID: t.MeetingID,
		LastEventSeq: t.LastEventSeq,
	}
	if sum.AfterMode == "" { // tasks stored before afterMode was always set
		sum.AfterMode = AfterAccepted
	}
	if tu := t.LastTurn(); tu != nil {
		for i := len(tu.Attempts) - 1; i >= 0; i-- {
			if a := tu.Attempts[i]; a.AccountID != "" {
				sum.AccountID, sum.AccountLabel = a.AccountID, a.AccountLabel
				break
			}
		}
	}
	if live := s.live(t.ID); live.running {
		step := &api.ActiveStep{Label: live.step, Tool: live.tool, Since: api.FormatTime(live.lastEvent)}
		if live.tool != "" {
			step.Since = api.FormatTime(live.toolSince)
		}
		if step.Label == "" {
			step.Label = "starting"
			step.Since = api.FormatTime(live.started)
		}
		sum.ActiveStep = step
	}
	return sum
}
