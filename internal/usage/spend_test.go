package usage

import (
	"context"
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
