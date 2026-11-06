package task

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/role"
)

// titleChars caps a title made from the prompt.
const titleChars = 80

// maxCreateTries bounds the retries when a fresh task id is taken.
const maxCreateTries = 5

// Delegate creates a task and starts it (or lets it wait for its prerequisites).
func (s *Service) Delegate(ctx context.Context, req api.DelegateRequest) (api.TaskSummary, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return api.TaskSummary{}, invalidf("prompt is required")
	}
	cwd, key, name, err := checkCwd(req.Cwd)
	if err != nil {
		return api.TaskSummary{}, err
	}
	mode := req.Mode
	if mode == "" {
		mode = ModeEdit
	}
	if mode != ModeEdit && mode != ModePlan {
		return api.TaskSummary{}, invalidf("mode %q must be edit or plan", req.Mode)
	}
	if req.Member != "" {
		return api.TaskSummary{}, invalidf("department members are not available yet")
	}
	backendName := req.Backend
	if backendName == "" {
		backendName = config.BackendAgy
	}
	if _, err := s.backend(backendName); err != nil {
		return api.TaskSummary{}, err
	}
	var prov *account.Provider
	if req.Provider != "" {
		p, err := s.o.Accounts.GetProvider(ctx, req.Provider)
		if err != nil {
			return api.TaskSummary{}, invalidf("provider %s: %v", req.Provider, err)
		}
		if p.Backend != backendName {
			return api.TaskSummary{}, invalidf("provider %s serves %s, not %s", p.ID, p.Backend, backendName)
		}
		prov = &p
	}
	t := &Task{
		Title: title(req.Title, prompt), Status: StatusQueued, Kind: KindNormal, Mode: mode, Cwd: cwd,
		ProjectKey: key, ProjectName: name, Prompt: prompt, Backend: backendName, Provider: req.Provider,
		AllowGit: req.AllowGit, Session: req.Session, Role: req.Role,
	}
	roleID := req.Profile
	if roleID == "" {
		roleID = req.Role
	}
	var rl *role.Role
	if roleID != "" {
		if s.o.Roles == nil {
			return api.TaskSummary{}, invalidf("roles are not available")
		}
		r, err := s.o.Roles.Get(ctx, roleID, cwd)
		if err != nil {
			return api.TaskSummary{}, invalidf("role %q: %v", roleID, err)
		}
		rl = &r
		t.Profile = &api.ProfileRef{ID: r.ID, Name: r.Name}
		t.Prefer = slices.Clone(r.PreferredAccounts)
	}
	model, tier, effort, err := s.resolveModel(backendName, prov, req.Model, req.Tier, req.Effort, rl)
	if err != nil {
		return api.TaskSummary{}, err
	}
	t.Model, t.Tier, t.Effort = model, tier, effort
	var taskIDs []string
	if req.Spec != nil {
		spec, outside, err := checkSpec(cwd, req.Spec)
		if err != nil {
			return api.TaskSummary{}, err
		}
		t.Spec, t.SpecOutside = spec, outside
		taskIDs = spec.TaskIDs
	}
	if len(req.After) > 0 {
		mode := req.AfterMode
		if mode == "" {
			mode = AfterAccepted
		}
		if mode != AfterAccepted && mode != AfterDone {
			return api.TaskSummary{}, invalidf("afterMode %q must be accepted or done", req.AfterMode)
		}
		seen := map[string]bool{}
		for _, id := range req.After {
			if seen[id] {
				continue
			}
			seen[id] = true
			if _, err := s.repo.Get(ctx, id); err != nil {
				if errors.Is(err, ErrNotFound) {
					return api.TaskSummary{}, invalidf("prerequisite %s not found", id)
				}
				return api.TaskSummary{}, err
			}
			t.After = append(t.After, id)
		}
		t.AfterMode = mode
		t.Status = StatusWaitingDeps
		t.DepsReason = "waiting for " + strings.Join(t.After, ", ")
	}
	now := s.now()
	t.CreatedAt, t.UpdatedAt = now, now
	t.Turns = []*Turn{{N: 1, By: ByLead, Message: prompt, Status: TurnRunning, Model: model,
		RequestedModel: req.Model, Tier: tier, Effort: effort, TaskIDs: taskIDs, StartedAt: now}}
	evs := []NewEvent{{Type: api.EventLeadMessage, Turn: 1,
		Data: api.LeadMessageData{Message: prompt, By: ByLead, Model: model, Effort: effort, Tier: tier}}}
	if t.Status == StatusWaitingDeps {
		evs = append(evs, NewEvent{Type: api.EventDeps, Turn: 1,
			Data: api.DepsData{State: api.DepsWaiting, After: t.After, Reason: t.DepsReason}})
	}
	var out []api.TaskEvent
	for try := 0; ; try++ {
		t.ID = s.newID()
		s.syncAttention(t, &evs)
		out, err = s.repo.Create(ctx, t, evs)
		if err == nil {
			break
		}
		if try >= maxCreateTries {
			return api.TaskSummary{}, err
		}
		if _, getErr := s.repo.Get(ctx, t.ID); getErr != nil {
			return api.TaskSummary{}, err // not an id collision
		}
	}
	s.publish(t.ID, out)
	if t.Status == StatusQueued {
		s.startRun(t.ID)
	} else {
		s.checkDeps(s.background())
	}
	return s.Summary(ctx, t.ID)
}

// Followup adds a turn to a finished task and starts it. A task that waits for (or is blocked by) its
// prerequisites starts only with Force; the message then joins its first request.
func (s *Service) Followup(ctx context.Context, id string, req api.FollowupRequest) (api.TaskSummary, error) {
	msg := strings.TrimSpace(req.Message)
	if msg == "" && !req.Force {
		return api.TaskSummary{}, invalidf("message is required")
	}
	by := req.By
	if by == "" {
		by = ByLead
	}
	if by != ByLead && by != ByUser {
		return api.TaskSummary{}, invalidf("by %q must be lead or user", req.By)
	}
	start := false
	_, err := s.mutate(ctx, id, func(t *Task) ([]NewEvent, error) {
		start = false
		if t.Manual {
			return nil, badStatef("task %s is taken over by the user", id)
		}
		if s.consoleOpen(id) {
			return nil, badStatef("the takeover console of task %s is still open; close it first", id)
		}
		if Active(t.Status) {
			return nil, badStatef("task %s is %s; wait for it or cancel it first", id, t.Status)
		}
		if t.AllowGit = derefOr(req.AllowGit, t.AllowGit); req.Profile != "" {
			if err := s.setProfile(ctx, t, req.Profile); err != nil {
				return nil, err
			}
		}
		if t.Status == StatusWaitingDeps || t.Status == StatusBlockedDeps {
			if !req.Force {
				return nil, badStatef("task %s waits for its prerequisites (%s); pass force to start it anyway", id,
					t.DepsReason)
			}
			// force may come without a message: it only starts the task (old C-B8).
			var evs []NewEvent
			if msg != "" {
				who := "lead"
				if by == ByUser {
					who = "user"
				}
				tu := t.TurnN(1)
				tu.Message += "\n\n## Additional instructions from the " + who + "\n\n" + msg
				evs = append(evs, NewEvent{Type: api.EventLeadMessage, Turn: 1, Data: api.LeadMessageData{Message: msg, By: by}})
			}
			more, err := s.startFromDeps(ctx, t, true)
			if err != nil {
				return nil, err
			}
			start = true
			return append(evs, more...), nil
		}
		if msg == "" {
			return nil, invalidf("message is required")
		}
		ev, err := s.addTurn(ctx, t, by, msg, req)
		if err != nil {
			return nil, err
		}
		start = true
		return []NewEvent{ev}, nil
	})
	if err != nil {
		return api.TaskSummary{}, err
	}
	if start {
		s.startRun(id)
	}
	s.checkDeps(s.background())
	return s.Summary(ctx, id)
}

// Answer answers the question a task asked: a followup by the user.
func (s *Service) Answer(ctx context.Context, id string, req api.AnswerRequest) (api.TaskSummary, error) {
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return api.TaskSummary{}, err
	}
	if t.Attention == nil || t.Attention.State != api.AttentionQuestion {
		return api.TaskSummary{}, badStatef("task %s has no open question", id)
	}
	return s.Followup(ctx, id, api.FollowupRequest{Message: req.Answer, By: ByUser})
}

// addTurn opens the next turn of a finished task.
func (s *Service) addTurn(ctx context.Context, t *Task, by, msg string, req api.FollowupRequest) (NewEvent, error) {
	model, tier, effort := t.Model, t.Tier, t.Effort
	if req.Effort != "" {
		effort = req.Effort
	}
	if req.Model != "" || req.Tier != "" {
		var prov *account.Provider
		if t.Provider != "" {
			p, err := s.o.Accounts.GetProvider(ctx, t.Provider)
			if err != nil {
				return NewEvent{}, invalidf("provider %s: %v", t.Provider, err)
			}
			prov = &p
		}
		var err error
		if model, tier, effort, err = s.resolveModel(t.Backend, prov, req.Model, req.Tier, effort, nil); err != nil {
			return NewEvent{}, err
		}
	}
	tu := &Turn{N: len(t.Turns) + 1, By: by, Message: msg, Status: TurnRunning, Model: model,
		RequestedModel: req.Model, Tier: tier, Effort: effort, StartedAt: s.now()}
	if len(req.TaskIDs) > 0 {
		if t.Spec == nil {
			return NewEvent{}, invalidf("taskIds need a task with a spec")
		}
		t.Spec.TaskIDs = slices.Clone(req.TaskIDs)
		tu.TaskIDs, tu.TaskIDsPassed = slices.Clone(req.TaskIDs), true
	}
	if err := t.SetStatus(StatusQueued); err != nil {
		return NewEvent{}, err
	}
	t.Turns = append(t.Turns, tu)
	t.Model, t.Tier, t.Effort = model, tier, effort
	t.Error, t.Review, t.CancelRequested = "", nil, false
	t.AttentionDismissedAt = time.Time{}
	return NewEvent{Type: api.EventLeadMessage, Turn: tu.N,
		Data: api.LeadMessageData{Message: msg, By: by, Model: model, Effort: effort, Tier: tier}}, nil
}

// resolveModel picks the model of a turn: an explicit model, else the tier, else the role's default tier, else the
// settings' default tier (backend.ModelPolicy.Resolve). The effort defaults to the role's.
func (s *Service) resolveModel(backendName string, prov *account.Provider, model, tier, effort string,
	rl *role.Role) (resolved, outTier, outEffort string, err error) {
	set := s.settings()
	policy, err := backend.PolicyFor(backendName, set, prov)
	if err != nil {
		return "", "", "", invalidf("%v", err)
	}
	defaultTier := set.DefaultTier
	if rl != nil {
		if rl.DefaultTier != "" {
			defaultTier = rl.DefaultTier
		}
		if effort == "" {
			effort = rl.DefaultEffort
		}
	}
	if tier != "" && !slices.Contains(config.Tiers(), tier) {
		return "", "", "", invalidf("tier %q must be one of %s", tier, strings.Join(config.Tiers(), ", "))
	}
	resolved, err = policy.Resolve(model, tier, defaultTier)
	if err != nil {
		return "", "", "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if tier == "" && model == "" {
		tier = defaultTier
	}
	return resolved, tier, effort, nil
}

func (s *Service) setProfile(ctx context.Context, t *Task, id string) error {
	if s.o.Roles == nil {
		return invalidf("roles are not available")
	}
	r, err := s.o.Roles.Get(ctx, id, t.Cwd)
	if err != nil {
		return invalidf("role %q: %v", id, err)
	}
	t.Profile = &api.ProfileRef{ID: r.ID, Name: r.Name}
	t.Prefer = slices.Clone(r.PreferredAccounts)
	return nil
}

// checkCwd validates a task folder: an absolute path to an existing folder. It returns the canonical path and the
// project key and name.
func checkCwd(cwd string) (canonical, key, name string, err error) {
	canonical, key, name, err = fsutil.ProjectOf(cwd)
	if err != nil {
		return "", "", "", invalidf("%v", err)
	}
	return canonical, key, name, nil
}

// checkSpec validates a Spec Kit feature directory: relative to cwd (inside it) or absolute (outside it).
func checkSpec(cwd string, req *api.SpecRequest) (spec *api.SpecRef, outside bool, err error) {
	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		return nil, false, invalidf("spec.dir is required")
	}
	abs := dir
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, dir)
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return nil, false, invalidf("spec.dir %q is not an existing folder", req.Dir)
	}
	rel, inside := relInside(cwd, abs)
	if inside {
		dir = filepath.ToSlash(rel)
	} else {
		if c, err := fsutil.Canonical(abs); err == nil {
			abs = c
		}
		dir = filepath.ToSlash(abs)
	}
	return &api.SpecRef{Dir: dir, TaskIDs: api.NonNil(slices.Clone(req.TaskIDs))}, !inside, nil
}

func title(given, prompt string) string {
	if t := strings.TrimSpace(given); t != "" {
		return t
	}
	line, _, _ := strings.Cut(prompt, "\n")
	line = strings.TrimSpace(strings.TrimLeft(line, "#> "))
	if r := []rune(line); len(r) > titleChars {
		line = string(r[:titleChars-1]) + "…"
	}
	return line
}

func derefOr(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}
