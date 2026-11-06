package pool

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/backend"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/quota"
)

// restUntil returns the Until of the resting Unavailable error of a gemini request, failing otherwise.
func (f *fixture) restUntil(req Request) time.Time {
	f.t.Helper()
	_, err := f.p.Select(f.t.Context(), req)
	var u *Unavailable
	if !errors.As(err, &u) || u.Reason != ReasonResting {
		f.t.Fatalf("not resting: %v", err)
	}
	return u.Until
}

func (f *fixture) cooldownRows() int {
	f.t.Helper()
	var n int
	err := f.db.Read(f.t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(f.t.Context(), "SELECT count(*) FROM cooldowns").Scan(&n)
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

func TestMarkExhaustedActive(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.agy(0)
	f.read(a.ID, "agy", 0.5, t0.Add(4*time.Hour))
	now := f.clk.Now()
	// The fresh read shows the 5h window used up until reset.
	reset := now.Add(3 * time.Hour)
	f.refresh = func(_ context.Context, id string) (quota.Snapshot, error) {
		f.read(id, "agy", 0, reset)
		snaps, _ := f.quotas.All(ctx)
		return snaps[id], nil
	}
	c, err := f.p.MarkExhausted(ctx, a.ID, Exhaustion{Group: "Gemini Models", Model: "gemini-3.8-flash-medium", Reason: "quota"})
	if err != nil || !c.Until.Equal(reset) || c.Passive || c.Reason != "quota" {
		t.Fatalf("cooldown %+v, %v", c, err)
	}
	if got := f.restUntil(gemini("t")); !got.Equal(reset) {
		t.Fatalf("resting until %s, want %s", got, reset)
	}
	// Another group of the same account is not affected by the cooldown.
	f.clk.Advance(time.Minute)
	f.read(a.ID, "agy", 0.5, reset, func(q *backend.Quota) {
		q.Groups = append(q.Groups, backend.QuotaGroup{Name: "Claude", Buckets: []backend.QuotaBucket{{Window: "5h", UsedFraction: 0.1}}})
	})
	if _, err := f.p.Select(ctx, Request{Backend: "agy", Group: "Claude"}); err != nil {
		t.Fatalf("other group: %v", err)
	}
	states, _ := f.p.States(ctx)
	if len(states[0].Cooldowns) != 0 {
		// The read above (after the cooldown) shows Gemini Models at 0.5: the rest is lifted (B15).
		t.Fatalf("cooldowns %+v", states[0].Cooldowns)
	}
	if _, err := f.p.Select(ctx, gemini("t")); err != nil {
		t.Fatalf("rest not lifted by a later read: %v", err)
	}
}

func TestMarkExhaustedFallbacks(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.agy(0)
	f.read(a.ID, "agy", 0.5, time.Time{})
	now := f.clk.Now()
	// The read fails: the cache (not exhausted) and no hint give the default rest.
	f.refresh = func(context.Context, string) (quota.Snapshot, error) { return quota.Snapshot{}, errors.New("offline") }
	c, err := f.p.MarkExhausted(ctx, a.ID, Exhaustion{Group: "Gemini Models"})
	if err != nil || !c.Until.Equal(now.Add(quota.DefaultRest)) {
		t.Fatalf("cooldown %+v, %v", c, err)
	}
	// A cache read made at the same instant does not lift a rest set at that instant.
	if got := f.restUntil(gemini("t")); !got.Equal(c.Until) {
		t.Fatalf("resting until %s", got)
	}
	// A later hint wins; a shorter one does not shorten the existing rest.
	hint := now.Add(5 * time.Hour)
	if c, err = f.p.MarkExhausted(ctx, a.ID, Exhaustion{Group: "Gemini Models", ResetAt: hint}); err != nil || !c.Until.Equal(hint) {
		t.Fatalf("hinted cooldown %+v, %v", c, err)
	}
	if c, err = f.p.MarkExhausted(ctx, a.ID, Exhaustion{Group: "Gemini Models", ResetAt: now.Add(2 * time.Hour)}); err != nil ||
		!c.Until.Equal(hint) {
		t.Fatalf("shorter hint: %+v, %v", c, err)
	}
	// Expired rows are pruned when the next rest is written.
	b := f.agy(0)
	f.clk.Advance(6 * time.Hour)
	if _, err := f.p.MarkExhausted(ctx, b.ID, Exhaustion{Group: "G"}); err != nil {
		t.Fatal(err)
	}
	if n := f.cooldownRows(); n != 1 {
		t.Fatalf("%d cooldown rows, want 1", n)
	}
	// Unknown accounts give ErrNotFound, never a panic (B15).
	if _, err := f.p.MarkExhausted(ctx, "acc404", Exhaustion{}); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("unknown account: %v", err)
	}
	// Without a refresher the cache is used.
	f.p.o.Refresh = nil
	if c, err := f.p.MarkExhausted(ctx, b.ID, Exhaustion{Group: "G2"}); err != nil || c.Until.IsZero() {
		t.Fatalf("no refresher: %+v, %v", c, err)
	}
}

func TestMarkExhaustedPassive(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	k := f.apiKeyAccount("openai", "sk-test-0123456789abcdef")
	req := Request{Backend: "codex", Group: "Codex", Model: "gpt-x", Provider: "openai"}
	now := f.clk.Now()
	f.setting(func(s *config.Settings) { s.PassiveCooldownMinutes = 30 })
	c, err := f.p.MarkExhausted(ctx, k.ID, Exhaustion{Group: "Codex", Reason: "429"})
	if err != nil || !c.Passive || !c.Until.Equal(now.Add(30*time.Minute)) {
		t.Fatalf("passive cooldown %+v, %v", c, err)
	}
	if got := f.restUntil(req); !got.Equal(c.Until) {
		t.Fatalf("resting until %s", got)
	}
	// A quota "read" never lifts a passive rest; only time does.
	f.clk.Advance(29 * time.Minute)
	f.restUntil(req)
	f.clk.Advance(time.Minute)
	if _, err := f.p.Select(ctx, req); err != nil {
		t.Fatalf("after the rest: %v", err)
	}
	// The provider's reset hint wins over the setting.
	hint := f.clk.Now().Add(3 * time.Minute)
	if c, err = f.p.MarkExhausted(ctx, k.ID, Exhaustion{Group: "Codex", ResetAt: hint}); err != nil || !c.Until.Equal(hint) {
		t.Fatalf("hinted %+v, %v", c, err)
	}
}

func TestClearCooldownAndUnreadableRows(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.agy(0)
	f.read(a.ID, "agy", 1, time.Time{})
	for _, g := range []string{"Gemini Models", "Claude"} {
		if _, err := f.p.MarkExhausted(ctx, a.ID, Exhaustion{Group: g, ResetAt: t0.Add(5 * time.Hour)}); err != nil {
			t.Fatal(err)
		}
	}
	states, _ := f.p.States(ctx)
	if len(states[0].Cooldowns) != 2 || states[0].Cooldowns[0].Group != "Claude" {
		t.Fatalf("cooldowns %+v", states[0].Cooldowns)
	}
	if err := f.p.ClearCooldown(ctx, a.ID, "Gemini Models"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.Select(ctx, gemini("t")); err != nil {
		t.Fatalf("after clearing: %v", err)
	}
	if err := f.p.ClearCooldown(ctx, a.ID, ""); err != nil || f.cooldownRows() != 0 {
		t.Fatalf("clear all: %v, %d rows", err, f.cooldownRows())
	}
	// A row with a broken time is skipped, not fatal.
	err := f.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO cooldowns (account_id, quota_group, until, reason, passive, created_at)
			VALUES (?, 'Gemini Models', 'soon', '', 0, 'then')`, a.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.p.Select(ctx, gemini("t")); err != nil {
		t.Fatalf("broken cooldown row: %v", err)
	}
}

func TestMarkAuthFailed(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	a := f.agy(0)
	f.read(a.ID, "agy", 1, time.Time{})
	k := f.apiKeyAccount("openai", "sk-test-0123456789abcdef")
	status := func(id string) Status {
		states, err := f.p.States(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range states {
			if s.Account.ID == id {
				return s.Status
			}
		}
		t.Fatalf("no state for %s", id)
		return ""
	}
	if err := f.p.MarkAuthFailed(ctx, a.ID, "401 from the CLI"); err != nil {
		t.Fatal(err)
	}
	if err := f.p.MarkAuthFailed(ctx, k.ID, ""); err != nil {
		t.Fatal(err)
	}
	if status(a.ID) != StatusNeedsLogin || status(k.ID) != StatusNeedsLogin {
		t.Fatalf("statuses %s, %s", status(a.ID), status(k.ID))
	}
	if _, err := f.p.Select(ctx, gemini("t")); !errors.Is(err, ErrNoAccount) {
		t.Fatalf("signed-out account chosen: %v", err)
	}
	// A successful read signs the account back in; a new key lifts the API-key account.
	f.clk.Advance(time.Minute)
	f.read(a.ID, "agy", 1, time.Time{})
	if _, err := f.m.SetKey(ctx, k.ID, "sk-test-fedcba9876543210"); err != nil {
		t.Fatal(err)
	}
	if status(a.ID) != StatusOK || status(k.ID) != StatusOK {
		t.Fatalf("after sign-in: %s, %s", status(a.ID), status(k.ID))
	}
	if err := f.p.MarkAuthFailed(ctx, "acc404", ""); !errors.Is(err, account.ErrNotFound) {
		t.Fatalf("unknown account: %v", err)
	}
	f.p.o.Quotas = failingQuotas{}
	if err := f.p.MarkAuthFailed(ctx, a.ID, ""); err == nil {
		t.Fatal("store failure not reported")
	}
}
