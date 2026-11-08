package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/usage"
)

// Spend and budget through the API: the budget set with the account, the spend after a run, the account resting
// on its budget, the delegate refused with the budget message, and the budget raised with PATCH.
func TestSpendAndBudget(t *testing.T) {
	e := newEnv(t)
	ctx := t.Context()
	e.must(e.req(http.MethodPatch, "/api/v1/settings", map[string]any{"backends": map[string]any{"claude": map[string]any{
		"enabled": true}}}, nil), http.StatusOK)
	var created api.AccountCreated
	e.must(e.post("/api/v1/accounts", map[string]any{"newProvider": map[string]any{"preset": "anthropic"},
		"apiKey": "sk-ant-test-0123456789", "budgetUsd": 0.10}), http.StatusCreated).json(t, &created)
	if created.SpendSource != api.SpendCLI || created.BudgetUSD == nil || *created.BudgetUSD != 0.10 ||
		created.BudgetStop == nil || !*created.BudgetStop || created.BudgetState != api.BudgetOK || created.SpentUSD != 0 ||
		created.NewProvider == nil || created.NewProvider.SpendSource != api.SpendCLI {
		t.Fatalf("created = %+v", created)
	}
	account := func() api.Account {
		t.Helper()
		var list []api.Account
		e.must(e.get("/api/v1/accounts"), http.StatusOK).json(t, &list)
		for _, a := range list {
			if a.ID == created.ID {
				return a
			}
		}
		t.Fatalf("account %s missing", created.ID)
		return api.Account{}
	}
	now := time.Now()
	if err := e.a.Usage.RecordAttempt(ctx, usage.Attempt{TaskID: "t-spend", Turn: 1, Attempt: 1,
		AccountID: created.ID, Backend: "claude", StartedAt: now, EndedAt: now.Add(time.Second),
		Usage: event.Usage{InputTokens: 1000, OutputTokens: 200, CostUSD: 0.155}}); err != nil {
		t.Fatal(err)
	}
	a := account()
	if a.SpentUSD != 0.155 || a.SpentTokens != 1200 || a.SpentInputTokens != 1000 || a.BudgetState != api.BudgetReached ||
		!a.Resting || a.RestReason != api.RestBudgetReached || a.SpentSince == "" {
		t.Fatalf("after a run past the budget: %+v", a)
	}
	got := e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "hi", "cwd": e.cwd, "backend": "claude",
		"provider": "anthropic", "model": "claude-haiku-5-5"}), http.StatusBadRequest, api.CodeInvalidArgument, "")
	if got.Message != "anthropic keys reached their budget ($0.10); raise it in Accounts" ||
		got.Details["reason"] != "budget_reached" {
		t.Fatalf("delegate at the budget = %+v", got)
	}
	e.must(e.req(http.MethodPatch, "/api/v1/accounts/"+created.ID, map[string]any{"budgetUsd": 200}, nil),
		http.StatusOK)
	if a = account(); a.BudgetState != api.BudgetOK || a.Resting || a.RestReason != "" || *a.BudgetUSD != 200 {
		t.Fatalf("budget raised: %+v", a)
	}
	e.wantError(e.req(http.MethodPatch, "/api/v1/accounts/"+created.ID, map[string]any{"budgetUsd": -1}, nil),
		http.StatusBadRequest, api.CodeInvalidArgument, "budgetUsd")

	// No credit left (G3): the same kind of hold, by reason and never as a date; delegate names the key; an edit of
	// the account (switched off and on after a top-up) lifts it.
	if err := e.a.Pool.MarkNoCredit(ctx, created.ID, "Credit balance is too low"); err != nil {
		t.Fatal(err)
	}
	if a = account(); !a.Resting || a.RestReason != api.RestCredits || a.RestingUntil != "" {
		t.Fatalf("no credit: %+v", a)
	}
	got = e.wantError(e.post("/api/v1/tasks", map[string]any{"prompt": "hi", "cwd": e.cwd, "backend": "claude",
		"provider": "anthropic", "model": "claude-haiku-5-5"}), http.StatusBadRequest, api.CodeInvalidArgument, "")
	if got.Message != "the anthropic key has no credit left; top up or change the key" || got.Details["reason"] != "credits" {
		t.Fatalf("delegate without credit = %+v", got)
	}
	time.Sleep(2 * time.Millisecond) // the edit must come after the mark
	e.must(e.req(http.MethodPatch, "/api/v1/accounts/"+created.ID, map[string]any{"enabled": true}, nil),
		http.StatusOK)
	if a = account(); a.Resting || a.RestReason != "" {
		t.Fatalf("after an edit: %+v", a)
	}

	// A tokens-only provider: the budget can be set, its state is unknown, and the cost does not count.
	var ds api.AccountCreated
	e.must(e.post("/api/v1/accounts", map[string]any{"newProvider": map[string]any{"preset": "deepseek"},
		"apiKey": "sk-test-0123456789", "budgetUsd": 1}), http.StatusCreated).json(t, &ds)
	if err := e.a.Usage.RecordAttempt(ctx, usage.Attempt{TaskID: "t-ds", Turn: 1, Attempt: 1, AccountID: ds.ID,
		Backend: "claude", StartedAt: time.Now(), EndedAt: time.Now(), Usage: event.Usage{TotalTokens: 50,
			CostUSD: 3}}); err != nil {
		t.Fatal(err)
	}
	var list []api.Account
	e.must(e.get("/api/v1/accounts"), http.StatusOK).json(t, &list)
	for _, x := range list {
		if x.ID == ds.ID && (x.SpendSource != api.SpendNone || x.BudgetState != api.BudgetUnknown || x.SpentUSD != 0 ||
			x.SpentTokens != 50 || x.Resting) {
			t.Fatalf("deepseek = %+v", x)
		}
	}
}

// The DTO passes the account package's spend sources and budget states through: they must be the API's values.
func TestSpendConstantsMatchAPI(t *testing.T) {
	for got, want := range map[string]string{account.SpendCLI: api.SpendCLI, account.SpendFree: api.SpendFree,
		account.SpendNone: api.SpendNone, account.BudgetOK: api.BudgetOK, account.BudgetWarn: api.BudgetWarn,
		account.BudgetReached: api.BudgetReached, account.BudgetUnknown: api.BudgetUnknown} {
		if got != want {
			t.Errorf("account %q, api %q", got, want)
		}
	}
}
