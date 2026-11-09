package pool

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
)

// budgetFixture is a fixture with the official anthropic provider and one key on it.
func budgetFixture(t *testing.T) (*fixture, account.Account, Request) {
	t.Helper()
	f := newFixture(t)
	p, err := account.FromPreset("anthropic", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	p.Tiers.Standard = "claude-haiku-5-5"
	if _, err := f.m.CreateProvider(t.Context(), p); err != nil {
		t.Fatal(err)
	}
	a := f.add(account.New{Backend: "claude", Kind: account.KindAPIKey, Provider: "anthropic"})
	if a, err = f.m.SetKey(t.Context(), a.ID, "sk-ant-test-0123456789"); err != nil {
		t.Fatal(err)
	}
	return f, a, Request{TaskID: "t", Backend: "claude", Group: "Claude", Model: "claude-haiku-5-5",
		Provider: "anthropic"}
}

// spend adds a turn's cost to the account the way usage.RecordAttempt does.
func (f *fixture) spend(id string, cost float64) {
	f.t.Helper()
	ctx := f.t.Context()
	if err := f.db.Write(ctx, func(tx *sql.Tx) error {
		_, _, err := account.ApplySpendTx(ctx, tx, nil, account.SpendRow{AccountID: id,
			StartedAt: f.clk.Now().UTC().Add(time.Minute).Format("2006-01-02T15:04:05.000000000Z"), CostUSD: cost,
			Total: 1})
		return err
	}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) setBudget(id string, budget float64) {
	f.t.Helper()
	if _, err := f.m.Repo().Update(f.t.Context(), id, account.Patch{BudgetUSD: &budget}); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) state(id string) AccountState {
	f.t.Helper()
	states, err := f.p.States(f.t.Context())
	if err != nil {
		f.t.Fatal(err)
	}
	for i := range states {
		if states[i].Account.ID == id {
			return states[i]
		}
	}
	f.t.Fatalf("no state for %s", id)
	return AccountState{}
}

// TestBudgetReachedWaitReason (review N1): with every key of the provider at its budget, the request waits with
// reason budget_reached and a detail that says what to do; the account's state is the budget hold, never a date;
// the wait wakes when the budget is raised.
func TestBudgetReachedWaitReason(t *testing.T) {
	f, a, req := budgetFixture(t)
	ctx := t.Context()
	f.setBudget(a.ID, 200)
	f.spend(a.ID, 200.5)

	_, err := f.p.Select(ctx, req)
	var u *Unavailable
	if !errors.As(err, &u) || u.Reason != ReasonBudget || !errors.Is(err, ErrAllResting) || len(u.BudgetReached) != 1 ||
		u.BudgetReached[0].BudgetUSD != 200 || u.BudgetReached[0].SpentUSD != 200.5 {
		t.Fatalf("select = %v (%+v)", err, u)
	}
	if want := a.ID + " reached its $200 budget: raise the budget or change the key"; u.Error() != want {
		t.Errorf("detail = %q, want %q", u.Error(), want)
	}
	if st := f.state(a.ID); st.RestReason != RestBudget || len(st.Cooldowns) != 0 {
		t.Errorf("state = %+v", st)
	}
	var he *ProviderRestError
	if err := f.p.ProviderResting(ctx, "anthropic"); !errors.As(err, &he) || he.Credits ||
		err.Error() != "anthropic keys reached their budget ($200); raise it in Accounts" ||
		he.APIDetails()["reason"] != RestBudget {
		t.Errorf("ProviderResting = %v", err)
	}

	// The waiting task wakes when the budget is raised (the account edit notifies the pool).
	done, seen := f.startWait(ctx, req)
	f.waitBlocked()
	if s := seen(); len(s) != 1 || s[0].Reason != ReasonBudget {
		t.Fatalf("wait reasons = %+v", s)
	}
	f.setBudget(a.ID, 500)
	f.p.Notify()
	if r := receive(t, done); r.err != nil || r.lease.AccountID() != a.ID {
		t.Fatalf("after raising the budget: %+v", r)
	}
}

// TestBudgetWaitWakes: switching the stop off, and storing a new key (which starts a new running total), each
// end a budget wait.
func TestBudgetWaitWakes(t *testing.T) {
	for _, act := range []string{"stop off", "new key"} {
		t.Run(act, func(t *testing.T) {
			f, a, req := budgetFixture(t)
			ctx := t.Context()
			f.setBudget(a.ID, 1)
			f.spend(a.ID, 1)
			done, _ := f.startWait(ctx, req)
			f.waitBlocked()
			if act == "stop off" {
				stop := false
				if _, err := f.m.Repo().Update(ctx, a.ID, account.Patch{BudgetStop: &stop}); err != nil {
					t.Fatal(err)
				}
			} else if _, err := f.m.SetKey(ctx, a.ID, "sk-ant-test-9876543210"); err != nil {
				t.Fatal(err)
			}
			f.p.Notify()
			if r := receive(t, done); r.err != nil || r.lease.AccountID() != a.ID {
				t.Fatalf("%s: %+v", act, r)
			}
		})
	}
}

// TestBudgetMixedRest: a budget hold next to an account resting on its quota reads as resting, and the detail
// still names the budget.
func TestBudgetMixedRest(t *testing.T) {
	u := &Unavailable{Backend: "claude", Resting: 2, Candidates: 2,
		BudgetReached: []BudgetHold{{AccountID: "acc9", Provider: "anthropic", BudgetUSD: 200}}}
	if u.restingReason() != ReasonResting {
		t.Fatalf("reason = %s", u.restingReason())
	}
	u.Reason = ReasonResting
	if want := "all 2 claude accounts are resting; acc9 reached its $200 budget: raise the budget or change the key"; u.Error() != want {
		t.Errorf("detail = %q", u.Error())
	}
	u = &Unavailable{Reason: ReasonBudget, Backend: "claude", Resting: 2, BudgetReached: []BudgetHold{
		{AccountID: "acc9", BudgetUSD: 200}, {AccountID: "acc10", BudgetUSD: 12.4}}}
	if want := "acc9 ($200), acc10 ($12.40) reached their budgets: raise the budgets or change the keys"; u.Error() != want {
		t.Errorf("two accounts: %q", u.Error())
	}
}

// TestHandoverBudget: a task whose own account reached its budget mid-task, with no other key, waits with reason
// budget_reached.
func TestHandoverBudget(t *testing.T) {
	f, a, req := budgetFixture(t)
	ctx := t.Context()
	f.setBudget(a.ID, 0.1)
	f.spend(a.ID, 0.1)
	h, err := f.p.Handover(ctx, HandoverInput{Current: a.ID, Request: req})
	if err != nil {
		t.Fatal(err)
	}
	if h.Action != ActionWait || h.Why != "the account reached its budget" || h.Wait.Reason != ReasonBudget ||
		h.Wait.Error() != a.ID+" reached its $0.10 budget: raise the budget or change the key" {
		t.Fatalf("handover = %+v, wait %+v", h, h.Wait)
	}
}

// TestNoCreditHold (G3): the credits rest is a hold on the account (no 9999 date); a spend update does not lift it
// (spend writes never touch updated_at), a failed key test does not either, a key test that passes does. The
// delegate's refusal names the key.
func TestNoCreditHold(t *testing.T) {
	f, a, req := budgetFixture(t)
	ctx := t.Context()
	f.clk.Advance(time.Second)
	if err := f.p.MarkNoCredit(ctx, a.ID, "Your credit balance is too low"); err != nil {
		t.Fatal(err)
	}
	f.clk.Advance(time.Second)
	if st := f.state(a.ID); st.RestReason != RestCredits || len(st.Cooldowns) != 0 {
		t.Fatalf("state = %+v", st)
	}
	var he *ProviderRestError
	if err := f.p.ProviderResting(ctx, "anthropic"); !errors.As(err, &he) || !he.Credits ||
		err.Error() != "the anthropic key has no credit left; top up or change the key" ||
		he.APIDetails()["reason"] != RestCredits {
		t.Fatalf("ProviderResting = %v", err)
	}

	f.setBudget(a.ID, 50) // an edit: lifts the hold like any edit (3b); mark again for the spend check
	f.clk.Advance(time.Second)
	if err := f.p.MarkNoCredit(ctx, a.ID, "Your credit balance is too low"); err != nil {
		t.Fatal(err)
	}
	f.clk.Advance(time.Second)
	before := f.state(a.ID).Account.UpdatedAt
	f.spend(a.ID, 1.25)
	st := f.state(a.ID)
	if st.RestReason != RestCredits || !st.Account.UpdatedAt.Equal(before) || st.Account.Spent.USD != 1.25 {
		t.Fatalf("after a spend update: hold %q, updated %v (was %v), spent %v", st.RestReason, st.Account.UpdatedAt,
			before, st.Account.Spent.USD)
	}
	if _, err := f.p.Select(ctx, req); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("select after a spend update = %v", err)
	}

	if _, err := f.m.Repo().RecordTest(ctx, a.ID, "refused", f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	f.clk.Advance(time.Second)
	if st := f.state(a.ID); st.RestReason != RestCredits {
		t.Fatalf("a failed test lifted the hold: %+v", st)
	}
	if _, err := f.m.Repo().RecordTest(ctx, a.ID, account.TestOK, f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	if st := f.state(a.ID); st.RestReason != "" {
		t.Fatalf("a passing test kept the hold: %+v", st)
	}
	if c, err := f.p.Select(ctx, req); err != nil || c.Account.ID != a.ID {
		t.Fatalf("select after a passing test = %+v, %v", c, err)
	}
	if err := f.p.ProviderResting(ctx, "anthropic"); err != nil {
		t.Errorf("ProviderResting = %v", err)
	}
}
