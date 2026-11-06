package pool

import (
	"context"
	"slices"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/event"
)

// Action is what a task does with its account after a turn.
type Action string

// Handover actions.
const (
	// ActionStay: keep the current account (and its conversation).
	ActionStay Action = "stay"
	// ActionMove: continue on Handover.To in a fresh conversation, with the handoff brief.
	ActionMove Action = "move"
	// ActionWait: no other account is free now; wait (Pool.Wait) with the request. Handover.Wait says why; with
	// reason no_account nothing can run the task at all.
	ActionWait Action = "wait"
)

// HandoverInput describes the turn that just ended.
type HandoverInput struct {
	// Current is the account the turn ran on.
	Current string
	// Class is the failure class of the turn ("" when it succeeded). Call MarkExhausted before Handover for
	// quota_exhausted, so the current account is already resting.
	Class event.Class
	// Request is the task's next request (backend, group, model, provider, preferences, exclusions).
	Request Request
}

// Handover is the decision.
type Handover struct {
	Action Action
	// To is the account to move to (ActionMove).
	To Choice
	// Wait says why nothing is free (ActionWait).
	Wait *Unavailable
	// Why explains the decision in a few words.
	Why string
}

// Handover decides whether a task leaves its account after a turn, and for which one. The current account is kept
// (its conversation can be resumed) unless it may not run the task any more: it was disabled, removed or signed
// out, the turn needs a new sign-in (auth_required), or it rests for the task's quota group. Being busy is not a
// reason to leave: the task waits for its own slot. Otherwise the best free account other than the current one is
// proposed, without taking it (acquire it with Request.Account set to it). Handover reads the state; it changes
// nothing.
func (p *Pool) Handover(ctx context.Context, in HandoverInput) (Handover, error) {
	v, err := p.load(ctx)
	if err != nil {
		return Handover{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	req := in.Request
	req.Account = ""

	var cur *account.Account
	for i := range v.accounts {
		if v.accounts[i].ID == in.Current {
			cur = &v.accounts[i]
		}
	}
	var own eval
	if cur != nil {
		own = v.evaluate(cur, &req, 0) // its own finished turn does not count against it
	}
	why := ""
	switch {
	case cur == nil:
		why = "the account was removed"
	case in.Class == event.ClassAuthRequired:
		why = "the account must sign in again"
	case own.state == stateIneligible:
		why = "the account can no longer run this task (" + string(v.status[cur.ID]) + ")"
	case own.state == stateResting:
		why = "the account is resting for this quota group"
	case own.state == statePending:
		why = "the account's quota is not known yet"
	default:
		return Handover{Action: ActionStay, Why: "the account can continue"}, nil
	}

	others := req
	if in.Current != "" && !slices.Contains(others.Exclude, in.Current) {
		others.Exclude = append(slices.Clone(others.Exclude), in.Current)
	}
	c, u := p.choose(v, &others)
	if u == nil {
		return Handover{Action: ActionMove, To: c, Why: why}, nil
	}
	// Nothing else is free. A resting current account still counts: the task can wait for it.
	if own.state == stateResting || own.state == statePending {
		u.Candidates++
		u.Resting++
		if u.Reason == ReasonNoAccount {
			u.Reason = ReasonResting
		}
		if !own.until.IsZero() && (u.Until.IsZero() || own.until.Before(u.Until)) {
			u.Until = own.until
		}
	}
	return Handover{Action: ActionWait, Wait: u, Why: why}, nil
}
