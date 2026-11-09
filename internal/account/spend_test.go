package account

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/config"
)

func TestSpendSource(t *testing.T) {
	preset := func(id string) Provider {
		p, err := FromPreset(id, id)
		if err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name string
		p    Provider
		want string
	}{
		{"official", preset("anthropic"), SpendCLI},
		{"legacy custom on api.anthropic.com", Provider{Backend: config.BackendClaude, Preset: "custom-anthropic",
			BaseURL: "https://api.anthropic.com/v1"}, SpendCLI},
		{"deepseek", preset("deepseek"), SpendNone},
		{"custom anthropic-compatible", Provider{Backend: config.BackendClaude, Preset: "custom-anthropic",
			BaseURL: "https://api.z.ai/api/anthropic"}, SpendNone},
		{"ollama", preset("ollama"), SpendFree},
		{"localhost", Provider{Backend: config.BackendCodex, BaseURL: "http://127.0.0.1:8080/v1"}, SpendFree},
		{"codex openai", Provider{Backend: config.BackendCodex, BaseURL: "https://api.openai.com/v1"}, SpendNone},
	}
	for _, c := range cases {
		if got := c.p.SpendSource(); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestFormatUSD(t *testing.T) {
	for in, want := range map[float64]string{200: "$200", 0.155: "$0.155", 12.4: "$12.40", 0.1: "$0.10",
		187.6: "$187.60", 0.003: "$0.003", 0.16: "$0.16"} {
		if got := FormatUSD(in); got != want {
			t.Errorf("FormatUSD(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestBudgetLevel(t *testing.T) {
	b := 200.0
	for _, c := range []struct {
		source string
		spent  float64
		budget *float64
		want   string
	}{
		{SpendCLI, 0.155, nil, ""}, {SpendCLI, 0.155, &b, BudgetOK}, {SpendCLI, 160, &b, BudgetWarn},
		{SpendCLI, 200, &b, BudgetReached}, {SpendNone, 500, &b, BudgetUnknown}, {SpendFree, 0, &b, BudgetUnknown},
	} {
		if got := BudgetLevel(c.source, c.spent, c.budget); got != c.want {
			t.Errorf("BudgetLevel(%s, %v) = %q, want %q", c.source, c.spent, got, c.want)
		}
	}
}

// spendFixture makes an API-key account on the given preset, with a key.
func spendFixture(t *testing.T, f *fixture, presetID string) Account {
	t.Helper()
	p, err := FromPreset(presetID, presetID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := f.m.AddDirect(ctxT(t), DirectNew{New: New{Enabled: true}, NewProvider: &p, Key: "sk-ant-test-0123456789"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return res.Account
}

func (f *fixture) apply(t *testing.T, prev *SpendRow, next SpendRow) []BudgetCrossing {
	t.Helper()
	_, out := f.record(t, prev, next)
	return out
}

// record applies an attempt and returns it as the usage table stores it (with how it was counted).
func (f *fixture) record(t *testing.T, prev *SpendRow, next SpendRow) (SpendRow, []BudgetCrossing) {
	t.Helper()
	var out []BudgetCrossing
	if err := f.db.Write(ctxT(t), func(tx *sql.Tx) error {
		counted, crossings, err := ApplySpendTx(ctxT(t), tx, prev, next)
		next.Source, next.USD, out = counted.Source, counted.USD, crossings
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return next, out
}

func (f *fixture) get(t *testing.T, id string) Account {
	t.Helper()
	a, err := f.m.Repo().Get(ctxT(t), id)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

// The running total adds each attempt once: a record written again replaces its earlier share (upsert twice), an
// attempt that moved to another account is taken back from the first, and an attempt from before the key is not
// counted. Only the official Anthropic API counts the CLI's cost; others count tokens only.
func TestApplySpendDelta(t *testing.T) {
	f := newFixture(t)
	a := spendFixture(t, f, "anthropic")
	b := spendFixture(t, f, "anthropic")
	after := formatTime(f.clock.t.Add(time.Minute))
	before := formatTime(a.KeySetAt.Add(-time.Hour))

	first, _ := f.record(t, nil, SpendRow{AccountID: a.ID, StartedAt: after, CostUSD: 0.01, Input: 100, Output: 20,
		Total: 120})
	if first.Source != SpendCLI || first.USD != 0.01 {
		t.Fatalf("counted as %q %v", first.Source, first.USD)
	}
	again := first
	again.CostUSD, again.Input, again.Output, again.Total = 0.03, 300, 50, 350
	again, _ = f.record(t, &first, again)
	got := f.get(t, a.ID).Spent
	if got != (Spend{USD: 0.03, Tokens: 350, Input: 300, Output: 50}) {
		t.Fatalf("after the same attempt twice: %+v", got)
	}
	moved := again
	moved.AccountID = b.ID
	f.apply(t, &again, moved)
	if got, gotB := f.get(t, a.ID).Spent, f.get(t, b.ID).Spent; got != (Spend{}) || gotB.USD != 0.03 {
		t.Fatalf("moved attempt: %+v, %+v", got, gotB)
	}
	f.apply(t, nil, SpendRow{AccountID: a.ID, StartedAt: before, CostUSD: 5, Total: 1000})
	if got := f.get(t, a.ID).Spent; got != (Spend{}) {
		t.Fatalf("an attempt from before the key counted: %+v", got)
	}

	// A DeepSeek key: the CLI's cost is not trusted, tokens count.
	d := spendFixture(t, f, "deepseek")
	f.apply(t, nil, SpendRow{AccountID: d.ID, StartedAt: formatTime(f.clock.t.Add(time.Minute)), CostUSD: 0.5,
		Input: 10, Output: 5, Total: 15})
	if got := f.get(t, d.ID).Spent; got != (Spend{Tokens: 15, Input: 10, Output: 5, Unpriced: 15}) {
		t.Fatalf("deepseek spend: %+v", got)
	}
	// Sign-in accounts have no spend.
	iso, _, err := f.m.Add(ctxT(t), New{Backend: config.BackendClaude, Kind: KindIsolated, Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.apply(t, nil, SpendRow{AccountID: iso.ID, StartedAt: after, CostUSD: 1, Total: 10})
	if got := f.get(t, iso.ID).Spent; got != (Spend{}) {
		t.Fatalf("sign-in spend: %+v", got)
	}
}

// 80% warns once, 100% is reached once and stops the account while BudgetStop is on; raising the budget lifts it.
// A provider without a trusted cost keeps the state unknown and never stops.
func TestBudgetThresholds(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	a := spendFixture(t, f, "anthropic")
	budget := 0.10
	if _, err := f.m.Repo().Update(ctx, a.ID, Patch{BudgetUSD: &budget}); err != nil {
		t.Fatal(err)
	}
	at := formatTime(f.clock.t.Add(time.Minute))
	row := func(task string, cost float64) SpendRow {
		return SpendRow{AccountID: a.ID, StartedAt: at + task, CostUSD: cost, Total: 1}
	}
	if c := f.apply(t, nil, row("1", 0.05)); len(c) != 0 {
		t.Fatalf("50%%: %+v", c)
	}
	c := f.apply(t, nil, row("2", 0.031))
	if len(c) != 1 || c[0].State != BudgetWarn ||
		c[0].Message() != "Anthropic 1 ("+a.ID+") has used 81% of its $0.10 budget on provider anthropic ($0.081 spent)" {
		t.Fatalf("80%%: %+v %q", c, c[0].Message())
	}
	if c := f.apply(t, nil, row("3", 0.001)); len(c) != 0 {
		t.Fatalf("the warning fired twice: %+v", c)
	}
	if got := f.get(t, a.ID); got.BudgetStopped() {
		t.Fatal("stopped before the budget")
	}
	c = f.apply(t, nil, row("4", 0.02))
	if len(c) != 1 || c[0].State != BudgetReached || !strings.Contains(c[0].Message(), "reached its $0.10 budget") {
		t.Fatalf("100%%: %+v", c)
	}
	got := f.get(t, a.ID)
	if !got.BudgetStopped() || !got.BudgetStop {
		t.Fatalf("not stopped at the budget: %+v", got)
	}
	var err error
	stop := false
	if got, err = f.m.Repo().Update(ctx, a.ID, Patch{BudgetStop: &stop}); err != nil || got.BudgetStopped() {
		t.Fatalf("stop off: %+v, %v", got, err)
	}
	stop = true
	raised := 200.0
	if got, err = f.m.Repo().Update(ctx, a.ID, Patch{BudgetStop: &stop, BudgetUSD: &raised}); err != nil ||
		got.BudgetStopped() {
		t.Fatalf("raised: %+v, %v", got, err)
	}
	zero := 0.0
	if got, err = f.m.Repo().Update(ctx, a.ID, Patch{BudgetUSD: &zero}); err != nil || got.BudgetUSD != nil {
		t.Fatalf("budget removed: %+v, %v", got, err)
	}
	for _, bad := range []float64{-1, 2e7} {
		if _, err := f.m.Repo().Update(ctx, a.ID, Patch{BudgetUSD: &bad}); !errors.Is(err, ErrInvalid) {
			t.Errorf("budget %v: %v", bad, err)
		}
	}

	// No trusted cost: the budget may be set, but never stops the account.
	d := spendFixture(t, f, "deepseek")
	if _, err := f.m.Repo().Update(ctx, d.ID, Patch{BudgetUSD: &budget}); err != nil {
		t.Fatal(err)
	}
	if c := f.apply(t, nil, SpendRow{AccountID: d.ID, StartedAt: at, CostUSD: 5, Total: 100}); len(c) != 0 {
		t.Fatalf("deepseek crossing: %+v", c)
	}
	if got := f.get(t, d.ID); got.BudgetStopped() || BudgetLevel(SpendNone, got.Spent.USD, got.BudgetUSD) != BudgetUnknown {
		t.Fatalf("deepseek: %+v", got)
	}
}

// A new key starts the count again and keeps what the old one spent.
func TestKeyChangeResetsSpend(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	a := spendFixture(t, f, "anthropic")
	budget := 0.05
	if _, err := f.m.Repo().Update(ctx, a.ID, Patch{BudgetUSD: &budget}); err != nil {
		t.Fatal(err)
	}
	f.apply(t, nil, SpendRow{AccountID: a.ID, StartedAt: formatTime(f.clock.t.Add(time.Minute)), CostUSD: 0.06,
		Input: 7, Output: 3, Total: 10})
	if stopped := f.get(t, a.ID); !stopped.BudgetStopped() {
		t.Fatal("not stopped")
	}
	got, err := f.m.SetKey(ctx, a.ID, "sk-ant-test-fedcba9876543210")
	if err != nil {
		t.Fatal(err)
	}
	pk := got.PreviousKey
	if got.Spent != (Spend{}) || got.BudgetStopped() || pk == nil || pk.KeyHint != a.KeyHint ||
		pk.Spent != (Spend{USD: 0.06, Tokens: 10, Input: 7, Output: 3}) || pk.BudgetUSD == nil || *pk.BudgetUSD != budget ||
		!pk.Since.Equal(a.KeySetAt) || pk.Until.IsZero() {
		t.Fatalf("after a new key: %+v, previous %+v", got, pk)
	}
	if got.BudgetUSD == nil || *got.BudgetUSD != budget {
		t.Errorf("the budget should stay: %+v", got.BudgetUSD)
	}
	// A key that spent nothing keeps the archived record of the one before.
	if got, err = f.m.SetKey(ctx, a.ID, "sk-ant-test-0000000000000000"); err != nil || got.PreviousKey == nil ||
		got.PreviousKey.Spent.USD != 0.06 {
		t.Fatalf("second change: %+v, %v", got.PreviousKey, err)
	}
}
