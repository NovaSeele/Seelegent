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

// Spawn holds what only the daemon's own features set on a new task: never the HTTP body, so no caller can attach
// a task to a meeting, contest or parent it does not own (old C-B21).
type Spawn struct {
	// Kind is KindNormal (the default) or a hidden kind (KindMeeting, KindJudge, KindConsult): a hidden run is
	// read-only (plan mode), has no gate and is never listed.
	Kind string
	// ParentID is the task, meeting or contest that started a hidden run.
	ParentID  string
	MeetingID string
	ContestID string
	// TicketID and AutomationID link a ticket task (internal/ticket) and an automation run (internal/automation).
	TicketID     string
	AutomationID string
	// Variant and ProjectCwd describe a contest variant: its label and the user's folder its worktree stands for.
	Variant    string
	ProjectCwd string
	// Prefer lists accounts to try first, before the member's and the role's.
	Prefer []string
}

// Delegate creates a task and starts it (or lets it wait for its prerequisites).
func (s *Service) Delegate(ctx context.Context, req api.DelegateRequest) (api.TaskSummary, error) {
	return s.DelegateWith(ctx, req, Spawn{})
}

// DelegateWith is Delegate for the daemon's own features (meetings, contests, consults): sp sets the links and the
// kind of the new task.
func (s *Service) DelegateWith(ctx context.Context, req api.DelegateRequest, sp Spawn) (api.TaskSummary, error) {
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return api.TaskSummary{}, invalidf("prompt is required")
	}
	cwd, key, name, err := checkCwd(req.Cwd)
	if err != nil {
		return api.TaskSummary{}, err
	}
	projectCwd := ""
	if sp.ProjectCwd != "" {
		if projectCwd, key, name, err = checkCwd(sp.ProjectCwd); err != nil {
			return api.TaskSummary{}, err
		}
	}
	kind := sp.Kind
	if kind == "" {
		kind = KindNormal
	}
	if kind != KindNormal && (!Hidden(kind) || kind == KindVerify) {
		return api.TaskSummary{}, invalidf("kind %q cannot be delegated", kind)
	}
	mode := req.Mode
	if mode == "" {
		mode = ModeEdit
		if Hidden(kind) {
			mode = ModePlan
		}
	}
	if mode != ModeEdit && mode != ModePlan {
		return api.TaskSummary{}, invalidf("mode %q must be edit or plan", req.Mode)
	}
	if Hidden(kind) && mode != ModePlan {
		return api.TaskSummary{}, invalidf("a %s run is read-only (plan mode)", kind)
	}
	preq := proofRequest{gate: req.Gate, gateCommands: req.GateCommands, lane: req.Lane, noLane: req.NoLane,
		forceLane: req.ForceLane, verify: req.Verify}
	if mode == ModePlan && preq.asksGate() {
		return api.TaskSummary{}, invalidf("a plan task has no gate, lane or verifier")
	}
	session, err := checkSession(req.Session)
	if err != nil {
		return api.TaskSummary{}, err
	}
	var mem *api.Member
	if req.Member != "" {
		m, err := s.usableMember(ctx, req.Member)
		if err != nil {
			return api.TaskSummary{}, err
		}
		mem = &m
	}
	backendName, providerID, reqModel, reqTier, roleID := memberDefaults(req, mem)
	if backendName == "" {
		backendName = config.BackendAgy
	}
	if _, err := s.backend(backendName); err != nil {
		return api.TaskSummary{}, err
	}
	var prov *account.Provider
	if providerID != "" {
		p, err := s.o.Accounts.GetProvider(ctx, providerID)
		if err != nil {
			return api.TaskSummary{}, invalidf("provider %s: %v", providerID, err)
		}
		if p.Backend != backendName {
			return api.TaskSummary{}, invalidf("provider %s serves %s, not %s", p.ID, p.Backend, backendName)
		}
		prov = &p
	}
	t := &Task{
		Title: title(req.Title, prompt), Status: StatusQueued, Kind: kind, Mode: mode, Cwd: cwd,
		ProjectKey: key, ProjectName: name, Prompt: prompt, Backend: backendName, Provider: providerID,
		Session: session, Role: req.Role, ParentID: sp.ParentID, MeetingID: sp.MeetingID,
		ContestID: sp.ContestID, TicketID: sp.TicketID, AutomationID: sp.AutomationID, Variant: sp.Variant,
		ProjectCwd: projectCwd, BaselineTaken: Hidden(kind),
	}
	var memberPrefer []string
	if mem != nil {
		t.Member, memberPrefer = mem.ID, mem.PreferredAccounts
	}
	var rl *role.Role
	if roleID != "" {
		if s.o.Roles == nil {
			return api.TaskSummary{}, invalidf("roles are not available")
		}
		r, err := s.o.Roles.Get(ctx, roleID, t.projectDir())
		if err != nil {
			byMember := ""
			if mem != nil && req.Profile == "" && req.Role == "" {
				byMember = mem.ID
			}
			return api.TaskSummary{}, roleErr(roleID, byMember, err)
		}
		rl = &r
		t.Profile = &api.ProfileRef{ID: r.ID, Name: r.Name}
		t.Prefer = slices.Clone(r.PreferredAccounts)
	}
	t.Prefer = mergePrefer(sp.Prefer, memberPrefer, t.Prefer)
	model, tier, effort, err := s.resolveModel(backendName, prov, reqModel, reqTier, req.Effort, rl)
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
	afterMode := req.AfterMode
	if afterMode == "" {
		afterMode = AfterAccepted
	}
	if afterMode != AfterAccepted && afterMode != AfterDone {
		return api.TaskSummary{}, invalidf("afterMode %q must be accepted or done", req.AfterMode)
	}
	t.AfterMode = afterMode
	if gated(t) {
		t.RepoRoot = s.gitRoot(ctx, cwd)
		if t.Proof, err = s.resolveProof(t, preq, nil, false); err != nil {
			return api.TaskSummary{}, err
		}
	}
	if len(req.After) > 0 {
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
		t.Status = StatusWaitingDeps
		t.DepsReason = "waiting for " + strings.Join(t.After, ", ")
	}
	now := s.now()
	t.CreatedAt, t.UpdatedAt = now, now
	t.Turns = []*Turn{{N: 1, By: ByLead, Message: prompt, Status: TurnRunning, Model: model,
		RequestedModel: reqModel, Tier: tier, Effort: effort, TaskIDs: taskIDs, AllowGit: req.AllowGit, StartedAt: now}}
	evs := []NewEvent{{Type: api.EventLeadMessage, Turn: 1,
		Data: api.LeadMessageData{Message: prompt, By: ByLead, Model: model, Effort: effort, Tier: tier}}}
	if t.Status == StatusWaitingDeps {
		evs = append(evs, NewEvent{Type: api.EventDeps, Turn: 1,
			Data: api.DepsData{State: api.DepsWaiting, After: t.After, Reason: t.DepsReason}})
	}
	// The lane check and the create are one step: two delegates cannot both claim the same files (old D25a).
	// The new task's first flags are read against the accounts as they are now, not as the last attention tick saw
	// them: an account added or signed in since must not show as no_accounts, which wakes the delegate's wait (N2).
	s.refreshHealth(ctx)
	s.claimMu.Lock()
	laneEvs, err := s.checkLane(ctx, t, t.Proof, req.ForceLane)
	if err != nil {
		s.claimMu.Unlock()
		return api.TaskSummary{}, err
	}
	for i := range laneEvs {
		laneEvs[i].Turn = 1
	}
	evs = append(evs, laneEvs...)
	var out []api.TaskEvent
	for try := 0; ; try++ {
		t.ID = s.newID()
		s.syncAttention(t, &evs)
		out, err = s.repo.Create(ctx, t, evs)
		if err == nil {
			break
		}
		if try >= maxCreateTries {
			s.claimMu.Unlock()
			return api.TaskSummary{}, err
		}
		if _, getErr := s.repo.Get(ctx, t.ID); getErr != nil {
			s.claimMu.Unlock()
			return api.TaskSummary{}, err // not an id collision
		}
	}
	s.claimMu.Unlock()
	s.forgetOverlaps()
	s.publishNotices(t.ID, true, out)
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
	s.refreshHealth(ctx) // the new turn's flags against the accounts as they are now (see DelegateWith)
	preq := proofRequest{gate: req.Gate, gateCommands: listOf(req.GateCommands), lane: req.Lane,
		noLane: req.NoLane, forceLane: req.ForceLane, verify: req.Verify}
	var mem *api.Member
	if req.Member != "" {
		m, err := s.usableMember(ctx, req.Member)
		if err != nil {
			return api.TaskSummary{}, err
		}
		mem = &m
	}
	start := false
	// A new lane is checked and saved as one step, as in Delegate (old D25a): claimMu is held through the mutate
	// (lock order claimMu, then the task).
	s.claimMu.Lock()
	_, err := s.mutate(ctx, id, func(t *Task) ([]NewEvent, error) {
		start = false
		if Hidden(t.Kind) && mem != nil {
			return nil, invalidf("task %s is a hidden %s run; its member cannot change", id, t.Kind)
		}
		if t.Manual {
			return nil, badStatef("task %s is taken over by the user", id)
		}
		if s.consoleOpen(id) {
			return nil, badStatef("the takeover console of task %s is still open; close it first", id)
		}
		if Active(t.Status) {
			return nil, badStatef("task %s is %s; wait for it or cancel it first", id, t.Status)
		}
		if err := s.checkContest(ctx, t); err != nil {
			return nil, err
		}
		if req.Profile != "" {
			if err := s.setProfile(ctx, t, req.Profile); err != nil {
				return nil, err
			}
		}
		if t.Status == StatusWaitingDeps || t.Status == StatusBlockedDeps {
			if mem != nil {
				return nil, badStatef("task %s has not run yet; delegate a new task with the member instead", id)
			}
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
			// The force starts the delegated command (turn 1): it keeps the delegate's allow_git unless this request
			// passes one.
			if tu := t.TurnN(1); tu != nil {
				tu.AllowGit = derefOr(req.AllowGit, tu.AllowGit)
			}
			laneEvs, _, err := s.followupProof(ctx, t, preq, false)
			if err != nil {
				return nil, err
			}
			more, err := s.startFromDeps(ctx, t, true)
			if err != nil {
				return nil, err
			}
			start = true
			return append(append(evs, laneEvs...), more...), nil
		}
		if msg == "" {
			return nil, invalidf("message is required")
		}
		turnReq := req
		if mem != nil {
			var err error
			if turnReq, err = s.switchMember(ctx, t, *mem, req); err != nil {
				return nil, err
			}
		}
		ev, err := s.addTurn(ctx, t, by, msg, turnReq)
		if err != nil {
			return nil, err
		}
		laneEvs, changed, err := s.followupProof(ctx, t, preq, len(req.TaskIDs) > 0)
		if err != nil {
			return nil, err
		}
		t.LastTurn().ProofChanged = changed
		start = true
		return append([]NewEvent{ev}, laneEvs...), nil
	})
	s.claimMu.Unlock()
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
	// allow_git is per command: a followup that does not pass it runs guarded (Turn.AllowGit).
	tu := &Turn{N: len(t.Turns) + 1, By: by, Message: msg, Status: TurnRunning, Model: model,
		RequestedModel: req.Model, Tier: tier, Effort: effort, AllowGit: derefOr(req.AllowGit, false),
		StartedAt: s.now()}
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
		return roleErr(id, "", err)
	}
	t.Profile = &api.ProfileRef{ID: r.ID, Name: r.Name}
	t.Prefer = slices.Clone(r.PreferredAccounts)
	return nil
}

// checkSession validates the lead session of a delegate: its lead, when given, must be one of api.Leads (any case).
// It returns a copy with the lead in lower case, or nil when there is no session.
func checkSession(s *api.Session) (*api.Session, error) {
	if s == nil {
		return nil, nil
	}
	out := *s
	out.Lead = strings.ToLower(strings.TrimSpace(out.Lead))
	if out.Lead != "" && !slices.Contains(api.Leads(), out.Lead) {
		return nil, invalidf("session.lead %q must be one of %s", s.Lead, strings.Join(api.Leads(), ", "))
	}
	return &out, nil
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

// checkSpec validates a Spec Kit feature directory: relative to cwd (inside it) or absolute (outside it). A relative
// one that leaves cwd once cleaned and resolved (.., a link or junction out) is refused, as the old version, GET
// /spec and proof refuse it (A-L9h): a folder outside cwd is named by its absolute path.
func checkSpec(cwd string, req *api.SpecRequest) (spec *api.SpecRef, outside bool, err error) {
	dir := strings.TrimSpace(req.Dir)
	if dir == "" {
		return nil, false, invalidf("spec.dir is required")
	}
	abs := dir
	relative := !filepath.IsAbs(abs)
	if relative {
		abs = filepath.Join(cwd, dir)
	}
	if st, err := os.Stat(abs); err != nil || !st.IsDir() {
		return nil, false, invalidf("spec.dir %q is not an existing folder", req.Dir)
	}
	rel, inside := relInside(cwd, abs)
	if relative && !inside {
		return nil, false, invalidf("spec.dir %q leaves cwd; pass an absolute path", req.Dir)
	}
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
