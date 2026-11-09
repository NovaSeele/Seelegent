package usage

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
)

// The spend of an API-key account is a running total kept with each recorded attempt: an attempt recorded twice
// counts once, it survives the pruning of the usage table, and it counts while usageHistory is off.
func TestRecordAttemptSpend(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	dir := t.TempDir()
	for _, d := range []string{"profiles", "home"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	m := account.NewManager(f.db, account.NewProfiles(filepath.Join(dir, "profiles"), filepath.Join(dir, "home")),
		f.clk.Now)
	p, err := account.FromPreset("anthropic", "anthropic")
	if err != nil {
		t.Fatal(err)
	}
	budget := 0.19
	res, err := m.AddDirect(ctx, account.DirectNew{New: account.New{Enabled: true}, NewProvider: &p,
		Key: "sk-ant-test-0123456789", BudgetUSD: &budget}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := res.Account.ID
	var crossings []account.BudgetCrossing
	f.st.OnBudget(func(_ context.Context, c account.BudgetCrossing) { crossings = append(crossings, c) })
	spent := func() account.Spend {
		t.Helper()
		a, err := m.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		return a.Spent
	}

	// usageHistory off from the start: it stops the snapshots, never the spend.
	off := func() config.Settings {
		s := config.Defaults()
		s.UsageHistory = false
		return s
	}
	r := f.recorder(RecorderOptions{Settings: off})
	f.clk.Advance(time.Minute)
	attempt := Attempt{TaskID: "t1", Turn: 1, Attempt: 1, AccountID: id, Backend: "claude", StartedAt: f.clk.Now(),
		EndedAt: f.clk.Now().Add(time.Minute), Usage: event.Usage{InputTokens: 100, OutputTokens: 20, CostUSD: 0.1}}
	f.record(attempt)
	attempt.Usage = event.Usage{InputTokens: 300, OutputTokens: 50, CostUSD: 0.155}
	f.record(attempt) // the same attempt again (a background wait recorded once before its end)
	if got := spent(); got != (account.Spend{USD: 0.155, Tokens: 350, Input: 300, Output: 50}) {
		t.Fatalf("after the same attempt twice: %+v", got)
	}
	if len(crossings) != 1 || crossings[0].State != account.BudgetWarn {
		t.Fatalf("crossings = %+v", crossings)
	}

	f.clk.Advance(61 * 24 * time.Hour)
	if _, err := r.Tick(ctx); err != nil {
		t.Fatal(err)
	}
	if f.count("usage_turns") != 0 {
		t.Fatal("old attempts not pruned")
	}
	if got := spent(); got.USD != 0.155 || got.Tokens != 350 {
		t.Fatalf("the spend shrank with the pruning: %+v", got)
	}
	f.record(Attempt{TaskID: "t2", Turn: 1, Attempt: 1, AccountID: id, Backend: "claude", StartedAt: f.clk.Now(),
		EndedAt: f.clk.Now().Add(time.Minute), Usage: event.Usage{TotalTokens: 10, CostUSD: 0.05}})
	if got := spent(); got.USD != 0.205 || got.Tokens != 360 {
		t.Fatalf("with usageHistory off: %+v", got)
	}
	if len(crossings) != 2 || crossings[1].State != account.BudgetReached {
		t.Fatalf("crossings = %+v", crossings)
	}
}

// With prices, the attempt's cost is tokens × the model's price (cached input at its own price), stored with the
// attempt; recording the attempt again after the prices changed takes back exactly what it added the first time.
func TestRecordAttemptPrices(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	dir := t.TempDir()
	m := account.NewManager(f.db, account.NewProfiles(filepath.Join(dir, "profiles"), filepath.Join(dir, "home")),
		f.clk.Now)
	p, err := account.FromPreset("zai", "zai")
	if err != nil {
		t.Fatal(err)
	}
	res, err := m.AddDirect(ctx, account.DirectNew{New: account.New{Enabled: true}, NewProvider: &p,
		Key: "zai-test-0123456789"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := res.Account.ID
	cached := 0.1
	setPrices := func(in, out float64) {
		t.Helper()
		if _, err := m.SetPrices(ctx, res.Provider.ID, []account.Price{{Model: "glm-5", InputUSD: in,
			CachedInputUSD: &cached, OutputUSD: out}}); err != nil {
			t.Fatal(err)
		}
	}
	stored := func() (string, float64) {
		t.Helper()
		var src string
		var usd float64
		if err := f.db.Read(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, `SELECT spend_source, spend_usd FROM usage_turns WHERE task_id = 't1'`).Scan(
				&src, &usd)
		}); err != nil {
			t.Fatal(err)
		}
		return src, usd
	}
	setPrices(1, 4)
	f.clk.Advance(time.Minute)
	attempt := Attempt{TaskID: "t1", Turn: 1, Attempt: 1, AccountID: id, Backend: config.BackendClaude,
		Model: "glm-5", StartedAt: f.clk.Now(), EndedAt: f.clk.Now().Add(time.Minute),
		Usage: event.Usage{InputTokens: 200_000, CachedTokens: 1_000_000, OutputTokens: 50_000, CostUSD: 7}}
	f.record(attempt)
	if src, usd := stored(); src != account.SpendPrices || usd != 0.5 {
		t.Fatalf("stored %s %v", src, usd)
	}
	setPrices(2, 8)
	f.record(attempt) // the same attempt again, at the new prices: 0.5 is taken back, 0.9 added
	a, err := m.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if a.Spent.USD != 0.9 || a.Spent.Tokens != 250_000 {
		t.Fatalf("after the same attempt twice: %+v", a.Spent)
	}
	if src, usd := stored(); src != account.SpendPrices || usd != 0.9 {
		t.Fatalf("stored again %s %v", src, usd)
	}
	// The same attempt with its cache writes (100k, 50k of them for one hour) at the default rule: 50k × 2.5 +
	// 50k × 4 = 0.325 more, replacing the 0.9.
	attempt.Usage.CacheWriteTokens, attempt.Usage.CacheWrite1hTokens = 100_000, 50_000
	f.record(attempt)
	if a, err = m.Get(ctx, id); err != nil || a.Spent.USD != 1.225 {
		t.Fatalf("with cache writes: %+v, %v", a.Spent, err)
	}
	if src, usd := stored(); src != account.SpendPrices || usd != 1.225 {
		t.Fatalf("stored with cache writes %s %v", src, usd)
	}
}
