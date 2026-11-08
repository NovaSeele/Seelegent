package pool

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
)

// An API-key account that was never tested is selected like any other (its first turn is the check); a good turn
// marks it working, and a new key makes it not tested again, still selectable.
func TestNotTestedAccountIsSelected(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	keyed := f.apiKeyAccount("openai", "sk-test-0123456789abcdef")
	if keyed.TestState() != account.TestNotTested {
		t.Fatalf("new key: %q", keyed.TestState())
	}
	req := Request{Backend: "codex", Group: "Codex", Model: "gpt-x", Provider: "openai"}
	c, err := f.p.Select(ctx, req)
	if err != nil || c.Account.ID != keyed.ID {
		t.Fatalf("not tested account: %+v, %v", c, err)
	}
	changed := f.p.Changed()
	if err := f.p.MarkSucceeded(ctx, keyed.ID, f.clk.Now()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-changed:
	default:
		t.Error("MarkSucceeded did not wake the account feed")
	}
	if a, err := f.m.Get(ctx, keyed.ID); err != nil || a.TestState() != account.TestOK {
		t.Fatalf("after a good turn: %+v, %v", a, err)
	}
	f.clk.Advance(time.Second)
	if _, err := f.m.SetKey(ctx, keyed.ID, "sk-test-fedcba9876543210"); err != nil {
		t.Fatal(err)
	}
	if c, err = f.p.Select(ctx, req); err != nil || c.Account.ID != keyed.ID || c.Account.TestState() != account.TestNotTested {
		t.Fatalf("after a new key: %+v, %v", c, err)
	}
	// Unknown accounts are no error: the account may be gone by the time its turn ends.
	if err := f.p.MarkSucceeded(ctx, "acc99", f.clk.Now()); err != nil {
		t.Errorf("unknown account: %v", err)
	}
}

// An API-key account whose budget is reached rests (budget_reached) while BudgetStop is on: another key of the
// provider is picked, and with none left the request waits on resting accounts. With stop off, or a higher budget,
// it is picked again. A provider without a trusted cost never rests on its budget.
func TestBudgetReachedAccountRests(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	p, err := account.FromPreset("anthropic", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	p.Tiers.Standard = "claude-haiku-5-5"
	if _, err := f.m.CreateProvider(ctx, p); err != nil {
		t.Fatal(err)
	}
	add := func(priority int) account.Account {
		a := f.add(account.New{Backend: "claude", Kind: account.KindAPIKey, Provider: "anthropic", Priority: priority})
		if a, err = f.m.SetKey(ctx, a.ID, "sk-ant-test-0123456789"); err != nil {
			t.Fatal(err)
		}
		return a
	}
	first, second := add(10), add(0)
	budget := 0.10
	if _, err := f.m.Repo().Update(ctx, first.ID, account.Patch{BudgetUSD: &budget}); err != nil {
		t.Fatal(err)
	}
	spend := func(id string, cost float64) {
		t.Helper()
		if err := f.db.Write(ctx, func(tx *sql.Tx) error {
			_, err := account.ApplySpendTx(ctx, tx, nil, account.SpendRow{AccountID: id,
				StartedAt: f.clk.Now().UTC().Add(time.Minute).Format("2006-01-02T15:04:05.000000000Z"), CostUSD: cost,
				Total: 1})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	req := Request{Backend: "claude", Group: "Claude", Model: "claude-haiku-5-5", Provider: "anthropic"}
	if c, err := f.p.Select(ctx, req); err != nil || c.Account.ID != first.ID {
		t.Fatalf("before the budget: %+v, %v", c, err)
	}
	spend(first.ID, 0.10)
	if c, err := f.p.Select(ctx, req); err != nil || c.Account.ID != second.ID {
		t.Fatalf("budget reached: %+v, %v", c, err)
	}
	if _, err := f.m.Repo().Update(ctx, second.ID, account.Patch{BudgetUSD: &budget}); err != nil {
		t.Fatal(err)
	}
	spend(second.ID, 0.20)
	if _, err := f.p.Select(ctx, req); !errors.Is(err, ErrAllResting) {
		t.Fatalf("every key at its budget: %v", err)
	}
	stop := false
	if _, err := f.m.Repo().Update(ctx, second.ID, account.Patch{BudgetStop: &stop}); err != nil {
		t.Fatal(err)
	}
	if c, err := f.p.Select(ctx, req); err != nil || c.Account.ID != second.ID {
		t.Fatalf("stop off: %+v, %v", c, err)
	}
	raised := 1.0
	if _, err := f.m.Repo().Update(ctx, first.ID, account.Patch{BudgetUSD: &raised}); err != nil {
		t.Fatal(err)
	}
	if c, err := f.p.Select(ctx, req); err != nil || c.Account.ID != first.ID {
		t.Fatalf("budget raised: %+v, %v", c, err)
	}

	// Tokens only (no trusted cost): a budget never rests the account.
	keyed := f.apiKeyAccount("openai", "sk-test-0123456789abcdef")
	if _, err := f.m.Repo().Update(ctx, keyed.ID, account.Patch{BudgetUSD: &budget}); err != nil {
		t.Fatal(err)
	}
	spend(keyed.ID, 5)
	codex := Request{Backend: "codex", Group: "Codex", Model: "gpt-x", Provider: "openai"}
	if c, err := f.p.Select(ctx, codex); err != nil || c.Account.ID != keyed.ID {
		t.Fatalf("tokens-only provider: %+v, %v", c, err)
	}
}
