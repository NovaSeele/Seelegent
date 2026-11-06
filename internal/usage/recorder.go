package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/NovaSeele/Seelegent/internal/clock"
	"github.com/NovaSeele/Seelegent/internal/config"
	"github.com/NovaSeele/Seelegent/internal/pool"
)

// Recorder timing (old usage-history.js): the first snapshot is taken a minute after start, then the hour is
// checked every five minutes (a plain hourly interval can drift across an hour boundary and skip one).
const (
	DefaultStartDelay = time.Minute
	DefaultCheck      = 5 * time.Minute
)

// States returns the state of every account; *pool.Pool satisfies it.
type States interface {
	States(ctx context.Context) ([]pool.AccountState, error)
}

// Window is one quota window in a snapshot.
type Window struct {
	Group  string `json:"group"`
	Window string `json:"window"`
	Label  string `json:"label,omitempty"`
	// Remaining is the fraction left, 0..1.
	Remaining float64   `json:"remaining"`
	ResetAt   time.Time `json:"resetAt,omitzero"`
	// MinRemaining is the lowest Remaining of the day (daily rollup only).
	MinRemaining *float64 `json:"minRemaining,omitempty"`
}

// Snapshot is one account at one hour.
type Snapshot struct {
	At        time.Time `json:"at"`
	AccountID string    `json:"accountId"`
	Backend   string    `json:"backend"`
	Label     string    `json:"label"`
	Enabled   bool      `json:"enabled"`
	Status    string    `json:"status"`
	Plan      string    `json:"plan,omitempty"`
	Quota     []Window  `json:"quota"`
	// TokensToday and CostToday are the account's usage on the snapshot's local day so far.
	TokensToday int64   `json:"tokensToday"`
	CostToday   float64 `json:"costToday"`
}

// RecorderOptions configure a Recorder. DB and States are required.
type RecorderOptions struct {
	DB     DB
	States States
	// Settings returns the current settings; usageHistory false stops the snapshots (nil: always on).
	Settings func() config.Settings
	// Clock is the time source (the system clock when nil).
	Clock clock.Clock
	// Location is the zone of the clock hours and days (time.Local when nil).
	Location *time.Location
	// StartDelay and Check default to DefaultStartDelay and DefaultCheck.
	StartDelay time.Duration
	Check      time.Duration
	// Logger receives write failures, once until a write succeeds (discarded when nil).
	Logger *slog.Logger
}

// Recorder writes the hourly snapshots and prunes old ones.
type Recorder struct {
	o RecorderOptions

	mu        sync.Mutex
	lastHour  string // hour key of the newest snapshot written by this recorder
	lastPrune string // hour key of the last prune
	warned    bool
}

// NewRecorder returns a recorder.
func NewRecorder(o RecorderOptions) (*Recorder, error) {
	if o.DB == nil || o.States == nil {
		return nil, errors.New("usage: db and states are required")
	}
	if o.Clock == nil {
		o.Clock = clock.Real()
	}
	if o.Location == nil {
		o.Location = time.Local
	}
	if o.StartDelay <= 0 {
		o.StartDelay = DefaultStartDelay
	}
	if o.Check <= 0 {
		o.Check = DefaultCheck
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.DiscardHandler)
	}
	return &Recorder{o: o}, nil
}

// Run takes a snapshot after the start delay and then checks the hour every Check until ctx ends. It never fails:
// write errors are logged.
func (r *Recorder) Run(ctx context.Context) {
	wait := r.o.StartDelay
	for {
		t := r.o.Clock.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-t.C():
		}
		_, _ = r.Tick(ctx)
		wait = r.o.Check
	}
}

// Tick writes this hour's snapshot unless this recorder already handled the hour, and prunes old rows. It returns
// the number of accounts written. Rows already stored for the hour (before a restart) are kept, not duplicated.
// With usageHistory off no snapshot is written, but old snapshots and attempt usage are still pruned every hour.
func (r *Recorder) Tick(ctx context.Context) (int, error) {
	history := r.o.Settings == nil || r.o.Settings().UsageHistory
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.o.Clock.Now()
	key := hourKey(now, r.o.Location)
	if (history && r.lastHour == key) || (!history && r.lastPrune == key) {
		return 0, nil
	}
	n, err := r.write(ctx, now, key, history)
	if err != nil {
		if !r.warned && ctx.Err() == nil {
			r.warned = true
			r.o.Logger.Warn("usage: snapshot not written; further failures are not logged until a write succeeds",
				"err", err)
		}
		return 0, err
	}
	if history {
		r.lastHour = key
	}
	r.lastPrune, r.warned = key, false
	return n, nil
}

// write stores the snapshot of the hour key (only when history is on) and prunes old rows, in one transaction.
func (r *Recorder) write(ctx context.Context, now time.Time, key string, history bool) (int, error) {
	if !history {
		err := r.o.DB.Write(ctx, func(tx *sql.Tx) error { return prune(ctx, tx, now, r.o.Location) })
		if err != nil {
			return 0, fmt.Errorf("usage prune: %w", err)
		}
		return 0, nil
	}
	states, err := r.o.States.States(ctx)
	if err != nil {
		return 0, fmt.Errorf("usage snapshot: %w", err)
	}
	today := startOfDay(now, r.o.Location)
	n := 0
	err = r.o.DB.Write(ctx, func(tx *sql.Tx) error {
		n = 0
		spent, err := dayUsage(ctx, tx, today, now)
		if err != nil {
			return err
		}
		for i := range states {
			s := &states[i]
			snap := snapshotOf(s, now)
			u := spent[snap.AccountID]
			quota, err := json.Marshal(snap.Quota)
			if err != nil {
				return err
			}
			res, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO usage_snapshots (account_id, hour, at, backend, label,
				enabled, status, plan, quota, tokens_today, cost_today) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				snap.AccountID, key, formatTime(now), snap.Backend, snap.Label, boolInt(snap.Enabled), snap.Status,
				snap.Plan, string(quota), u.tokens, roundCost(u.cost))
			if err != nil {
				return err
			}
			if k, _ := res.RowsAffected(); k > 0 {
				n++
			}
		}
		return prune(ctx, tx, now, r.o.Location)
	})
	if err != nil {
		return 0, fmt.Errorf("usage snapshot: %w", err)
	}
	return n, nil
}

// snapshotOf turns an account state into a snapshot row (allow-list: nothing else is copied).
func snapshotOf(s *pool.AccountState, now time.Time) Snapshot {
	a := &s.Account
	snap := Snapshot{
		At: now.UTC(), AccountID: a.ID, Backend: a.Backend, Label: SafeLabel(a.Label, a.ID), Enabled: a.Enabled,
		Status: string(s.Status), Quota: []Window{},
	}
	if !s.HasQuota {
		return snap
	}
	snap.Plan = s.Quota.Plan
	for _, g := range s.Quota.Groups {
		for _, w := range g.Windows {
			snap.Quota = append(snap.Quota, Window{Group: g.Name, Window: w.Window, Label: w.Label,
				Remaining: w.Remaining, ResetAt: w.ResetAt.UTC()})
		}
	}
	return snap
}

type spend struct {
	tokens int64
	cost   float64
}

// dayUsage sums the attempts that ended in [from, to] per account.
func dayUsage(ctx context.Context, tx *sql.Tx, from, to time.Time) (map[string]spend, error) {
	rows, err := tx.QueryContext(ctx, `SELECT account_id, sum(total_tokens), sum(coalesce(cost_usd, 0)) FROM usage_turns
		WHERE ended_at >= ? AND ended_at <= ? GROUP BY account_id`, formatTime(from), formatTime(to))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]spend{}
	for rows.Next() {
		var id string
		var s spend
		if err := rows.Scan(&id, &s.tokens, &s.cost); err != nil {
			return nil, err
		}
		out[id] = s
	}
	return out, rows.Err()
}

// prune deletes snapshots older than RetentionDays local days and attempts older than TurnRetentionDays.
func prune(ctx context.Context, tx *sql.Tx, now time.Time, loc *time.Location) error {
	today := startOfDay(now, loc)
	if _, err := tx.ExecContext(ctx, "DELETE FROM usage_snapshots WHERE at < ?",
		formatTime(addDays(today, -(RetentionDays-1)))); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "DELETE FROM usage_turns WHERE ended_at < ?",
		formatTime(addDays(today, -(TurnRetentionDays-1))))
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
