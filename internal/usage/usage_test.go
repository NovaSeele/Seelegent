package usage

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/event"
	"github.com/NovaSeele/Seelegent/internal/pool"
	"github.com/NovaSeele/Seelegent/internal/quota"
	"github.com/NovaSeele/Seelegent/internal/store"
)

// zone is the local zone of the tests (UTC+7, no DST); t0 is 10:30 local on 6 Nov 2026.
var (
	zone = time.FixedZone("ICT", 7*3600)
	t0   = time.Date(2026, 11, 6, 10, 30, 0, 0, zone)
)

type fakeStates struct {
	mu     sync.Mutex
	states []pool.AccountState
	err    error
}

func (f *fakeStates) States(context.Context) ([]pool.AccountState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]pool.AccountState(nil), f.states...), f.err
}

func (f *fakeStates) set(states []pool.AccountState, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states, f.err = states, err
}

type fixture struct {
	t      *testing.T
	db     *store.Store
	clk    *clock.Fake
	states *fakeStates
	st     *Store
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	f := &fixture{t: t, db: db, clk: clock.NewFake(t0), states: &fakeStates{}}
	f.st = NewStore(db, f.clk, zone)
	return f
}

func (f *fixture) recorder(o RecorderOptions) *Recorder {
	f.t.Helper()
	o.DB, o.States, o.Clock, o.Location = f.db, f.states, f.clk, zone
	r, err := NewRecorder(o)
	if err != nil {
		f.t.Fatal(err)
	}
	return r
}

func (f *fixture) record(a Attempt) {
	f.t.Helper()
	if err := f.st.RecordAttempt(f.t.Context(), a); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fixture) count(table string) int {
	f.t.Helper()
	var n int
	err := f.db.Read(f.t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(f.t.Context(), "SELECT count(*) FROM "+table).Scan(&n)
	})
	if err != nil {
		f.t.Fatal(err)
	}
	return n
}

func accountStates(remaining float64) []pool.AccountState {
	return []pool.AccountState{
		{
			Account: account.Account{ID: "acc1", Backend: "agy", Label: "worker@example.com", Enabled: true},
			Status:  pool.StatusOK, HasQuota: true,
			Quota: quota.Snapshot{Plan: "Pro", Groups: []quota.Group{{Name: "Gemini Models", Windows: []quota.Window{
				{Window: "5h", Label: "5 hours", Remaining: remaining, ResetAt: t0.Add(time.Hour)},
				{Window: "weekly", Remaining: 0.8},
			}}}},
		},
		{Account: account.Account{ID: "acc2", Backend: "claude", Label: "Second"}, Status: pool.StatusDisabled},
	}
}

func TestSafeLabel(t *testing.T) {
	tests := []struct{ label, want string }{
		{"Work", "Work"},
		{"  Work  ", "Work"},
		{"", "acc1"},
		{"me@example.com", "acc1"},
		{"Main <Me.Name@mail.example.org>", "acc1"},
		{"not an @ email", "not an @ email"},
	}
	for _, tt := range tests {
		if got := SafeLabel(tt.label, "acc1"); got != tt.want {
			t.Errorf("SafeLabel(%q) = %q, want %q", tt.label, got, tt.want)
		}
	}
}

func TestTickOncePerHour(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.states.set(accountStates(0.5), nil)
	// Usage of today (and of yesterday, which does not count) reaches the snapshot.
	f.record(Attempt{TaskID: "t1", Turn: 1, AccountID: "acc1", Backend: "agy", EndedAt: t0.Add(-time.Hour),
		Usage: event.Usage{InputTokens: 100, OutputTokens: 20, CostUSD: 0.25}})
	f.record(Attempt{TaskID: "t1", Turn: 2, AccountID: "acc1", Backend: "agy", EndedAt: t0.Add(-11 * time.Hour),
		Usage: event.Usage{TotalTokens: 999}})
	r := f.recorder(RecorderOptions{Settings: config.Defaults})
	if n, err := r.Tick(ctx); n != 2 || err != nil {
		t.Fatalf("first tick = %d, %v", n, err)
	}
	if n, err := r.Tick(ctx); n != 0 || err != nil {
		t.Fatalf("same hour = %d, %v", n, err)
	}
	// A restarted recorder in the same hour adds nothing.
	if n, err := f.recorder(RecorderOptions{}).Tick(ctx); n != 0 || err != nil {
		t.Fatalf("after restart = %d, %v", n, err)
	}
	f.clk.Advance(time.Hour)
	f.states.set(accountStates(0.2), nil)
	if n, _ := r.Tick(ctx); n != 2 {
		t.Fatalf("next hour = %d", n)
	}
	h, err := f.st.History(ctx, HistoryQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Rows) != 4 || h.Bucket != BucketHour || !h.FirstAt.Equal(t0) || !h.Until.Equal(f.clk.Now()) {
		t.Fatalf("history %+v", h)
	}
	first := h.Rows[0]
	if first.AccountID != "acc1" || first.Label != "acc1" || first.Plan != "Pro" || len(first.Quota) != 2 ||
		first.Quota[0].Remaining != 0.5 || first.Quota[0].Group != "Gemini Models" ||
		!first.Quota[0].ResetAt.Equal(t0.Add(time.Hour)) || first.TokensToday != 120 || first.CostToday != 0.25 ||
		!first.Enabled || first.Status != "ok" {
		t.Fatalf("first row %+v", first)
	}
	if second := h.Rows[1]; second.AccountID != "acc2" || second.Label != "Second" || len(second.Quota) != 0 ||
		second.Enabled || second.Status != "disabled" {
		t.Fatalf("second row %+v", second)
	}
	// One account only.
	h, _ = f.st.History(ctx, HistoryQuery{AccountID: "acc2"})
	if len(h.Rows) != 2 {
		t.Fatalf("account filter: %d rows", len(h.Rows))
	}
}

func TestTickOffAndErrors(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.states.set(accountStates(0.5), nil)
	off := config.Defaults()
	off.UsageHistory = false
	if n, err := f.recorder(RecorderOptions{Settings: func() config.Settings { return off }}).Tick(ctx); n != 0 || err != nil {
		t.Fatalf("history off = %d, %v", n, err)
	}
	var logs bytes.Buffer
	r := f.recorder(RecorderOptions{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	f.states.set(nil, errors.New("pool down"))
	for range 3 {
		if _, err := r.Tick(ctx); err == nil {
			t.Fatal("state error not returned")
		}
	}
	if c := strings.Count(logs.String(), "snapshot not written"); c != 1 {
		t.Fatalf("logged %d times:\n%s", c, logs.String())
	}
	// A success re-arms the warning.
	f.states.set(accountStates(0.5), nil)
	if n, err := r.Tick(ctx); n != 2 || err != nil {
		t.Fatalf("after recovery = %d, %v", n, err)
	}
	f.clk.Advance(time.Hour)
	f.states.set(nil, errors.New("pool down again"))
	_, _ = r.Tick(ctx)
	if c := strings.Count(logs.String(), "snapshot not written"); c != 2 {
		t.Fatalf("logged %d times after recovery", c)
	}
	if _, err := NewRecorder(RecorderOptions{}); err == nil {
		t.Fatal("NewRecorder without dependencies")
	}
}

func TestPrune(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.states.set(accountStates(0.5), nil)
	r := f.recorder(RecorderOptions{})
	f.record(Attempt{TaskID: "old", EndedAt: t0})
	if n, _ := r.Tick(ctx); n != 2 {
		t.Fatal("no snapshot")
	}
	// 29 days later (30 local days counting today) the first snapshot is still kept.
	f.clk.Advance(29 * 24 * time.Hour)
	_, _ = r.Tick(ctx)
	if n := f.count("usage_snapshots"); n != 4 {
		t.Fatalf("%d snapshots after 29 days, want 4", n)
	}
	f.clk.Advance(24 * time.Hour)
	_, _ = r.Tick(ctx)
	if n := f.count("usage_snapshots"); n != 4 {
		t.Fatalf("%d snapshots after 30 days, want 4 (two pruned, two added)", n)
	}
	if n := f.count("usage_turns"); n != 1 {
		t.Fatalf("attempt usage pruned after 30 days")
	}
	f.clk.Advance(30 * 24 * time.Hour)
	_, _ = r.Tick(ctx)
	if n := f.count("usage_turns"); n != 0 {
		t.Fatalf("attempt usage kept after 60 days")
	}
}

func TestRunTicksAfterDelayAndStops(t *testing.T) {
	f := newFixture(t)
	f.states.set(accountStates(0.5), nil)
	r := f.recorder(RecorderOptions{StartDelay: time.Minute, Check: 5 * time.Minute})
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	if !f.clk.WaitTimers(1, 5*time.Second) {
		t.Fatal("no start timer")
	}
	if f.count("usage_snapshots") != 0 {
		t.Fatal("snapshot before the start delay")
	}
	f.clk.Advance(time.Minute)
	if !f.clk.WaitTimers(1, 5*time.Second) {
		t.Fatal("no check timer")
	}
	if n := f.count("usage_snapshots"); n != 2 {
		t.Fatalf("%d snapshots after the start delay", n)
	}
	f.clk.Advance(time.Hour) // the check fires once (one pending timer)
	if !f.clk.WaitTimers(1, 5*time.Second) {
		t.Fatal("no next check timer")
	}
	if n := f.count("usage_snapshots"); n != 4 {
		t.Fatalf("%d snapshots after an hour", n)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop")
	}
}

func TestHistoryQueries(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	if _, err := f.st.History(ctx, HistoryQuery{Bucket: "week"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("bad bucket: %v", err)
	}
	h, err := f.st.History(ctx, HistoryQuery{Bucket: BucketDay})
	if err != nil || h.Days == nil || len(h.Days) != 0 || !h.FirstAt.IsZero() {
		t.Fatalf("empty history %+v, %v", h, err)
	}
	now := f.clk.Now()
	if h, _ = f.st.History(ctx, HistoryQuery{Since: now.Add(-90 * 24 * time.Hour)}); !h.Since.Equal(now.Add(-MaxDays * 24 * time.Hour).UTC()) {
		t.Fatalf("since not clamped to %d days: %s", MaxDays, h.Since)
	}
	if h, _ = f.st.History(ctx, HistoryQuery{Since: now.Add(time.Hour)}); !h.Since.Equal(now.UTC()) {
		t.Fatalf("future since: %s", h.Since)
	}
	if h, _ = f.st.History(ctx, HistoryQuery{}); !h.Since.Equal(now.Add(-HistoryDefaultDays * 24 * time.Hour).UTC()) {
		t.Fatalf("default since: %s", h.Since)
	}
	// Damaged rows: an unreadable time is skipped, an unreadable quota comes back empty.
	err = f.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO usage_snapshots (account_id, hour, at, backend, enabled, quota)
			VALUES ('acc1', 'h1', ?, 'agy', 1, '{broken'), ('acc1', 'h2', '2026-11-06Tnope', 'agy', 1, '[]')`,
			formatTime(now.Add(-time.Hour)))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	h, err = f.st.History(ctx, HistoryQuery{})
	if err != nil || len(h.Rows) != 1 || h.Rows[0].Quota == nil || len(h.Rows[0].Quota) != 0 {
		t.Fatalf("damaged rows %+v, %v", h.Rows, err)
	}
}

func TestDailyRollup(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	r := f.recorder(RecorderOptions{})
	// Day 1: 10:30 remaining 0.5, 11:30 0.1, 12:30 0.4 (tokens 0, 120, 120); day 2: 0.9.
	for i, rem := range []float64{0.5, 0.1, 0.4} {
		f.states.set(accountStates(rem), nil)
		if i == 1 {
			f.record(Attempt{TaskID: "t", AccountID: "acc1", EndedAt: f.clk.Now(), Usage: event.Usage{TotalTokens: 120}})
		}
		if _, err := r.Tick(ctx); err != nil {
			t.Fatal(err)
		}
		f.clk.Advance(time.Hour)
	}
	f.clk.Advance(24 * time.Hour)
	f.states.set(accountStates(0.9), nil)
	_, _ = r.Tick(ctx)
	h, err := f.st.History(ctx, HistoryQuery{Bucket: BucketDay, AccountID: "acc1"})
	if err != nil || len(h.Days) != 2 || h.Rows != nil {
		t.Fatalf("days %+v, %v", h, err)
	}
	d := h.Days[0]
	if d.Date != "2026-11-06" || d.Samples != 3 || !d.FirstAt.Equal(t0) || !d.LastAt.Equal(t0.Add(2*time.Hour)) ||
		d.TokensToday != 120 || len(d.Quota) != 2 || d.Quota[0].Remaining != 0.4 || *d.Quota[0].MinRemaining != 0.1 ||
		*d.Quota[1].MinRemaining != 0.8 || d.Label != "acc1" || d.Plan != "Pro" {
		t.Fatalf("day 1 %+v", d)
	}
	if d := h.Days[1]; d.Date != "2026-11-07" || d.Samples != 1 || d.TokensToday != 0 || *d.Quota[0].MinRemaining != 0.9 {
		t.Fatalf("day 2 %+v", d)
	}
	// The local day decides: 23:30 local is still the 6th although it is the 6th 16:30 UTC.
	rows := []Snapshot{
		{At: time.Date(2026, 11, 6, 16, 30, 0, 0, time.UTC), AccountID: "b"},
		{At: time.Date(2026, 11, 6, 17, 30, 0, 0, time.UTC), AccountID: "b"},
		{At: time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC), AccountID: "a"},
	}
	days := rollup(rows, zone)
	if len(days) != 3 || days[0].AccountID != "a" || days[1].AccountID != "b" || days[2].Date != "2026-11-07" {
		t.Fatalf("rollup by local day %+v", days)
	}
}

func TestDayHelpers(t *testing.T) {
	berlin, err := time.LoadLocation("Europe/Berlin")
	if err != nil {
		t.Skip("no time zone data:", err)
	}
	// 25 Oct 2026 has 25 hours in Berlin; adding days keeps local midnight.
	day := startOfDay(time.Date(2026, 10, 24, 15, 0, 0, 0, berlin), berlin)
	if got := addDays(day, 2); got.Hour() != 0 || got.Day() != 26 {
		t.Fatalf("addDays across DST = %s", got)
	}
	if got := hourKey(time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC), berlin); got != "2026-10-25T02" {
		t.Fatalf("hourKey = %s", got)
	}
	if roundCost(-1) != 0 || roundCost(0.1234564) != 0.123456 {
		t.Fatal("roundCost")
	}
}

// With usageHistory off nothing is recorded, but old rows are still pruned (review finding D).
func TestPruneWhenHistoryOff(t *testing.T) {
	f := newFixture(t)
	ctx := t.Context()
	f.states.set(accountStates(0.5), nil)
	if n, _ := f.recorder(RecorderOptions{}).Tick(ctx); n != 2 {
		t.Fatal("no snapshot")
	}
	f.record(Attempt{TaskID: "old", EndedAt: t0})
	var on atomic.Bool
	settings := func() config.Settings {
		s := config.Defaults()
		s.UsageHistory = on.Load()
		return s
	}
	r := f.recorder(RecorderOptions{Settings: settings})
	f.clk.Advance(61 * 24 * time.Hour)
	if n, err := r.Tick(ctx); n != 0 || err != nil {
		t.Fatalf("tick with history off = %d, %v", n, err)
	}
	if f.count("usage_snapshots") != 0 || f.count("usage_turns") != 0 {
		t.Fatalf("not pruned: %d snapshots, %d attempts", f.count("usage_snapshots"), f.count("usage_turns"))
	}
	// Turned on again within the same hour: the snapshot of the hour is written.
	on.Store(true)
	if n, err := r.Tick(ctx); n != 2 || err != nil {
		t.Fatalf("tick after turning history on = %d, %v", n, err)
	}
}
