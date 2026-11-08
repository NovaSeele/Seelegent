package account

import (
	"testing"
	"time"
)

// An OpenRouter reading is the account's spend (source service) and judges its budget; a failed read keeps the
// values and marks them stale; a read that started before the key was set is dropped; a new key archives the
// service's count and clears the reading.
func TestRecordServiceOpenRouter(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	a := spendFixture(t, f, "openrouter")
	p := providerOf(t, f, a.Provider)
	if src, _ := a.SpendOf(p); src != SpendNone {
		t.Fatalf("before any read: %s", src)
	}
	budget := 10.0
	if _, err := f.m.Repo().Update(ctx, a.ID, Patch{BudgetUSD: &budget}); err != nil {
		t.Fatal(err)
	}
	limit, left := 20.0, 11.5
	reading := &ServiceReading{Usage: &ServiceUsage{TotalUSD: 8.5, LimitUSD: &limit, LimitRemainingUSD: &left},
		Balance: &ServiceBalance{Amount: left, Currency: "USD", Available: true}}
	started := f.clock.t
	svc, crossings, stored, err := f.m.RecordService(ctx, a.ID, started, reading, nil, false)
	if err != nil || !stored || svc.ReadAt.IsZero() || svc.Usage.TotalUSD != 8.5 {
		t.Fatalf("stored %v: %+v, %v", stored, svc, err)
	}
	if len(crossings) != 1 || crossings[0].State != BudgetWarn || crossings[0].SpentUSD != 8.5 {
		t.Fatalf("crossings: %+v", crossings)
	}
	got := f.get(t, a.ID)
	if src, usd := got.SpendOf(p); src != SpendService || usd != 8.5 {
		t.Fatalf("spend: %s %v", src, usd)
	}
	if got.SuggestedBudget() != nil {
		t.Fatal("a suggested budget while one is set")
	}

	// A failure keeps the values, with the error after them: stale.
	readErr := &ServiceError{Reason: ServiceUnreachable, Message: "openrouter.ai: no answer"}
	svc, _, stored, err = f.m.RecordService(ctx, a.ID, f.clock.t, nil, readErr, false)
	if err != nil || !stored || svc.Usage == nil || svc.Usage.TotalUSD != 8.5 || svc.Error == nil ||
		!svc.Stale(svc.Error.At, time.Hour) {
		t.Fatalf("failed read: %+v, %v", svc, err)
	}
	if svc.LastAttempt() != svc.Error.At {
		t.Fatal("last attempt")
	}

	// Reached through the service: the account stops.
	reading.Usage.TotalUSD = 10.2
	_, crossings, _, _ = f.m.RecordService(ctx, a.ID, f.clock.t, reading, nil, false)
	if got := f.get(t, a.ID); len(crossings) != 1 || crossings[0].State != BudgetReached || !got.BudgetStopped() {
		t.Fatalf("reached: %+v", crossings)
	}

	// A new key: the old key's count is archived, the reading cleared, and a read that started before is dropped.
	before := f.clock.t
	got, err = f.m.SetKey(ctx, a.ID, "sk-or-v1-new-key-0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if got.Service != nil || got.PreviousKey == nil || got.PreviousKey.Spent.USD != 10.2 || got.BudgetStopped() {
		t.Fatalf("after a new key: %+v, previous %+v", got.Service, got.PreviousKey)
	}
	if _, _, stored, err := f.m.RecordService(ctx, a.ID, before, reading, nil, false); err != nil || stored {
		t.Fatalf("a read of the old key was stored: %v %v", stored, err)
	}

	// No budget: the key's limit is suggested, never set.
	zero := 0.0
	if _, err := f.m.Repo().Update(ctx, a.ID, Patch{BudgetUSD: &zero}); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := f.m.RecordService(ctx, a.ID, f.clock.t, reading, nil, false); err != nil {
		t.Fatal(err)
	}
	got = f.get(t, a.ID)
	if s := got.SuggestedBudget(); s == nil || *s != 20 || got.BudgetUSD != nil {
		t.Fatalf("suggested: %v, budget %v", s, got.BudgetUSD)
	}
}

// An account-wide balance (DeepSeek) is shown, never counted as the key's spend; a no-credit read records when it
// held the account, and a read with credit clears it.
func TestRecordServiceBalance(t *testing.T) {
	f := newFixture(t)
	ctx := ctxT(t)
	d := spendFixture(t, f, "deepseek")
	reading := &ServiceReading{NoCredit: true, Balance: &ServiceBalance{Amount: 0, Currency: "CNY", AccountWide: true}}
	svc, _, stored, err := f.m.RecordService(ctx, d.ID, f.clock.t, reading, nil, true)
	if err != nil || !stored || svc.NoCreditAt.IsZero() || svc.Balance.Currency != "CNY" {
		t.Fatalf("no credit: %+v, %v", svc, err)
	}
	got := f.get(t, d.ID)
	if src, usd := got.SpendOf(providerOf(t, f, d.Provider)); src != SpendNone || usd != 0 {
		t.Fatalf("a balance counted as spend: %s %v", src, usd)
	}
	// Still none, not held again: the time of the hold stays.
	at := svc.NoCreditAt
	if svc, _, _, _ = f.m.RecordService(ctx, d.ID, f.clock.t, reading, nil, false); !svc.NoCreditAt.Equal(at) {
		t.Fatalf("hold time moved: %v", svc.NoCreditAt)
	}
	reading = &ServiceReading{Balance: &ServiceBalance{Amount: 50, Currency: "CNY", AccountWide: true, Available: true}}
	if svc, _, _, _ = f.m.RecordService(ctx, d.ID, f.clock.t, reading, nil, false); !svc.NoCreditAt.IsZero() {
		t.Fatalf("credit again, hold time kept: %v", svc.NoCreditAt)
	}
	// Not an API-key account, or no key: nothing stored.
	iso := f.mustAddIsolated(t)
	if _, _, stored, err := f.m.RecordService(ctx, iso, f.clock.t, reading, nil, false); err != nil || stored {
		t.Fatalf("sign-in account: %v %v", stored, err)
	}
}

func (f *fixture) mustAddIsolated(t *testing.T) string {
	t.Helper()
	a, _, err := f.m.Add(ctxT(t), New{Backend: "claude", Kind: KindIsolated, Enabled: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return a.ID
}

func TestServiceSpendStale(t *testing.T) {
	now := time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)
	var none *ServiceSpend
	if none.Stale(now, time.Hour) || !none.LastAttempt().IsZero() {
		t.Fatal("nil spend")
	}
	fresh := &ServiceSpend{ReadAt: now.Add(-time.Minute)}
	old := &ServiceSpend{ReadAt: now.Add(-3 * time.Hour)}
	failed := &ServiceSpend{ReadAt: now.Add(-time.Minute), Error: &ServiceError{At: now}}
	recovered := &ServiceSpend{ReadAt: now, Error: &ServiceError{At: now.Add(-time.Minute)}}
	if fresh.Stale(now, 2*time.Hour) || !old.Stale(now, 2*time.Hour) || !failed.Stale(now, 2*time.Hour) ||
		recovered.Stale(now, 2*time.Hour) {
		t.Fatal("stale")
	}
}
