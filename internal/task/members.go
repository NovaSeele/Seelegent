package task

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/role"
)

// usableMember returns the member with id for new work.
func (s *Service) usableMember(ctx context.Context, id string) (api.Member, error) {
	if s.o.Members == nil {
		return api.Member{}, invalidf("department members are not available")
	}
	m, err := s.o.Members.Usable(ctx, id)
	if err != nil {
		// A turned-off member is a conflict with details (member_disabled); anything else is a bad argument of
		// this request, not a missing task.
		var detailed interface{ APIDetails() map[string]any }
		if ctx.Err() != nil {
			return api.Member{}, err
		}
		if errors.As(err, &detailed) {
			if detailed.APIDetails()["reason"] == "member_missing" {
				// A member that is gone: a bad argument of this request that keeps its message and details (F22:
				// no "invalid: not found:" prefixes in what the lead reads).
				return api.Member{}, &invalidError{err: err}
			}
			return api.Member{}, err
		}
		return api.Member{}, invalidf("%v", err)
	}
	return m, nil
}

// ContestEndedError refuses a followup on a variant task of a contest that was applied, cancelled or failed (F46):
// the variant's changes were stored once when the contest ended, so a new turn would never show in the contest.
type ContestEndedError struct {
	Task, Contest, Status string
}

func (e *ContestEndedError) Error() string {
	return fmt.Sprintf("task %s is a variant of contest %s, which is %s: its changes were kept when the contest "+
		"ended, so it takes no more turns; delegate a new task instead", e.Task, e.Contest, e.Status)
}

// Is makes errors.Is(err, ErrBadState) true.
func (e *ContestEndedError) Is(target error) bool { return target == ErrBadState }

// APIDetails are the details of the API error (reason contest_ended).
func (e *ContestEndedError) APIDetails() map[string]any {
	return map[string]any{"reason": "contest_ended", "contest": e.Contest, "status": e.Status}
}

// checkContest refuses a followup on t when it is a variant of a contest that ended.
func (s *Service) checkContest(ctx context.Context, t *Task) error {
	if t.ContestID == "" || t.Kind != KindNormal || s.o.ContestEnded == nil {
		return nil
	}
	status, ended, err := s.o.ContestEnded(ctx, t.ContestID)
	if err != nil {
		return fmt.Errorf("read contest %s: %w", t.ContestID, err)
	}
	if ended {
		return &ContestEndedError{Task: t.ID, Contest: t.ContestID, Status: status}
	}
	return nil
}

// invalidError is a bad argument (ErrInvalid) that keeps the message and the API details of the error it wraps.
type invalidError struct{ err error }

func (e *invalidError) Error() string { return e.err.Error() }

// Is makes errors.Is(err, ErrInvalid) true.
func (e *invalidError) Is(target error) bool { return target == ErrInvalid }

func (e *invalidError) Unwrap() error { return e.err }

// RoleMissingError is a role named for a task (by the request or by its member) that is neither a role of the
// task's project nor a global one. A member's role is checked only when the member is used: roles can be
// project-level, so a role that is missing for one project may exist in another.
type RoleMissingError struct {
	// Role is the role id; Member is the member whose role it is ("" when the request named the role).
	Role, Member string
}

func (e *RoleMissingError) Error() string {
	if e.Member != "" {
		return fmt.Sprintf("member %q has the role %q, which is not a role of this project or a global role; "+
			"fix the member's role in Members or pick another role", e.Member, e.Role)
	}
	return fmt.Sprintf("no role %q in this project or the global roles; pick another role", e.Role)
}

// Is makes errors.Is(err, ErrInvalid) true.
func (e *RoleMissingError) Is(target error) bool { return target == ErrInvalid }

// APIDetails are the details of the API error (reason role_missing).
func (e *RoleMissingError) APIDetails() map[string]any {
	d := map[string]any{"reason": "role_missing", "role": e.Role}
	if e.Member != "" {
		d["member"] = e.Member
	}
	return d
}

// roleErr is the error of a role lookup that failed: RoleMissingError when there is no such role. member is the
// member whose role it is, or "".
func roleErr(id, member string, err error) error {
	if errors.Is(err, role.ErrNotFound) {
		return &RoleMissingError{Role: id, Member: member}
	}
	return invalidf("role %q: %v", id, err)
}

// memberDefaults applies a member to a delegate: the request's own backend, provider, model, tier and role win,
// the member fills what the request leaves out (old members.resolveForDelegate). The member's model belongs to its
// backend, so it is not used when the request names another backend; a provider likewise.
func memberDefaults(req api.DelegateRequest, m *api.Member) (backendName, providerID, model, tier, roleID string) {
	backendName, providerID, model, tier = req.Backend, req.Provider, req.Model, req.Tier
	roleID = req.Profile
	if roleID == "" {
		roleID = req.Role
	}
	if m == nil {
		return backendName, providerID, model, tier, roleID
	}
	same := backendName == "" || backendName == m.Backend
	if backendName == "" {
		backendName = m.Backend
	}
	if providerID == "" && same {
		providerID = m.Provider
	}
	if model == "" && tier == "" {
		tier = m.Tier
		if same {
			model = m.Model
		}
	}
	if roleID == "" {
		roleID = m.Role
	}
	return backendName, providerID, model, tier, roleID
}

// mergePrefer joins account preference lists in order, without duplicates.
func mergePrefer(lists ...[]string) []string {
	var out []string
	for _, l := range lists {
		for _, id := range l {
			if id != "" && !slices.Contains(out, id) {
				out = append(out, id)
			}
		}
	}
	return out
}

// switchMember moves a finished task to member m for its next turn: the member's backend, provider, role and
// preferred accounts apply, and its model or tier unless the followup names one. A new backend or provider starts a
// new conversation. It returns the followup with the member's model or tier filled in.
func (s *Service) switchMember(ctx context.Context, t *Task, m api.Member, req api.FollowupRequest) (
	api.FollowupRequest, error) {
	if m.Backend != t.Backend || m.Provider != t.Provider {
		if _, err := s.backend(m.Backend); err != nil {
			return req, err
		}
		if m.Provider != "" {
			p, err := s.o.Accounts.GetProvider(ctx, m.Provider)
			if err != nil {
				return req, invalidf("provider %s: %v", m.Provider, err)
			}
			if p.Backend != m.Backend {
				return req, invalidf("provider %s serves %s, not %s", p.ID, p.Backend, m.Backend)
			}
		}
		t.Backend, t.Provider = m.Backend, m.Provider
		t.AccountID, t.ConversationID = "", ""
		if req.Model == "" && req.Tier == "" {
			// The old model belongs to the old backend.
			req.Tier = m.Tier
			if req.Tier == "" {
				req.Tier = s.settings().DefaultTier
			}
			req.Model = m.Model
		}
	} else if req.Model == "" && req.Tier == "" && (m.Model != "" || m.Tier != "") {
		req.Model, req.Tier = m.Model, m.Tier
	}
	t.Member = m.ID
	if req.Profile == "" && m.Role != "" {
		if err := s.setProfile(ctx, t, m.Role); err != nil {
			return req, err
		}
	}
	t.Prefer = mergePrefer(m.PreferredAccounts, t.Prefer)
	return req, nil
}

// memberFallback moves a task whose backend has no account that can run it to the next fallback backend of its
// member (api.Member.FallbackBackends), forward only: once the list is used up the task waits as any other. A task
// on a provider, or on a backend the lead chose outside the member's list, stays. It reports whether the task moved.
func (s *Service) memberFallback(ctx context.Context, t *Task, tu *Turn) (bool, error) {
	if t.Member == "" || t.Provider != "" || s.o.Members == nil || Hidden(t.Kind) {
		return false, nil
	}
	m, err := s.o.Members.Get(ctx, t.Member)
	if err != nil || !m.Enabled || m.Invalid != "" || len(m.FallbackBackends) == 0 {
		return false, nil //nolint:nilerr // a member that is gone or off just has no fallback
	}
	chain := append([]string{m.Backend}, m.FallbackBackends...)
	at := slices.Index(chain, t.Backend)
	if at < 0 {
		return false, nil
	}
	next := ""
	for _, b := range chain[at+1:] {
		if _, err := s.backend(b); err == nil {
			next = b
			break
		}
	}
	if next == "" {
		return false, nil
	}
	tier := m.Tier
	if tier == "" {
		tier = t.Tier
	}
	model, tier, effort, err := s.resolveModel(next, nil, "", tier, t.Effort, nil)
	if err != nil {
		s.log.Warn("task: member fallback model", "task", t.ID, "backend", next, "err", err)
		return false, nil
	}
	from := t.Backend
	_, err = s.mutate(ctx, t.ID, func(cur *Task) ([]NewEvent, error) {
		ct := cur.LastTurn()
		if cur.Backend != from || cur.CancelRequested || cur.Manual || ct == nil || ct.N != tu.N ||
			len(ct.Attempts) != len(tu.Attempts) {
			return nil, errNoChange
		}
		cur.Backend, cur.Model, cur.Tier, cur.Effort = next, model, tier, effort
		cur.AccountID, cur.ConversationID = "", ""
		ct.Model, ct.Tier, ct.Effort = model, tier, effort
		return []NewEvent{{Type: api.EventHandoff, Turn: ct.N, Data: api.HandoffData{From: from, To: next,
			Kind:   api.HandoffBackend,
			Reason: fmt.Sprintf("no %s account can run this task; member %s falls back to %s", from, m.ID, next)}}}, nil
	})
	if err != nil {
		return false, err
	}
	return true, nil
}
