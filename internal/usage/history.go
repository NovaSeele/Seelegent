package usage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/NovaSeele/Seelegent/internal/account"
	"github.com/NovaSeele/Seelegent/internal/clock"
)

// Store answers the usage queries and records attempt usage.
type Store struct {
	db    DB
	clock clock.Clock
	loc   *time.Location
	// budget hears of API-key accounts whose budget state rose (nil: nobody listens).
	budget func(context.Context, account.BudgetCrossing)
}

// OnBudget sets who hears of API-key accounts whose budget state rose to warn or reached while an attempt was
// recorded. Call it while wiring, before the store is used.
func (s *Store) OnBudget(fn func(context.Context, account.BudgetCrossing)) { s.budget = fn }

// NewStore returns the usage store on db. Days and hours are local to loc (time.Local when nil); clk is the time
// source (the system clock when nil).
func NewStore(db DB, clk clock.Clock, loc *time.Location) *Store {
	if clk == nil {
		clk = clock.Real()
	}
	if loc == nil {
		loc = time.Local
	}
	return &Store{db: db, clock: clk, loc: loc}
}

// Bucket is the resolution of a history query.
type Bucket string

// Buckets.
const (
	BucketHour Bucket = "hour"
	BucketDay  Bucket = "day"
)

// HistoryQuery selects snapshots.
type HistoryQuery struct {
	// Since is the start (zero: HistoryDefaultDays ago); it is clamped to the last MaxDays and to now.
	Since time.Time
	// AccountID restricts the rows to one account ("" for all).
	AccountID string
	// Bucket is hour (one row per snapshot, the default) or day (one row per account and local day).
	Bucket Bucket
}

// Day is the daily rollup of one account: the last reading of the day, and per quota window also the day's
// lowest remaining fraction; the day's usage is its largest running counter.
type Day struct {
	Date        string    `json:"date"`
	AccountID   string    `json:"accountId"`
	Backend     string    `json:"backend"`
	Label       string    `json:"label"`
	Enabled     bool      `json:"enabled"`
	Status      string    `json:"status"`
	Plan        string    `json:"plan,omitempty"`
	Samples     int       `json:"samples"`
	FirstAt     time.Time `json:"firstAt"`
	LastAt      time.Time `json:"lastAt"`
	Quota       []Window  `json:"quota"`
	TokensToday int64     `json:"tokensToday"`
	CostToday   float64   `json:"costToday"`
}

// History is the answer to a history query: Rows for bucket hour, Days for bucket day.
type History struct {
	Bucket Bucket    `json:"bucket"`
	Since  time.Time `json:"since"`
	Until  time.Time `json:"until"`
	// FirstAt is the oldest snapshot kept (zero when there is none).
	FirstAt time.Time  `json:"firstAt,omitzero"`
	Rows    []Snapshot `json:"rows,omitempty"`
	Days    []Day      `json:"days,omitempty"`
}

// History returns the snapshots at or after q.Since, oldest first. An unknown bucket gives an error matching
// ErrInvalid. A row whose quota cannot be read comes back with an empty quota list.
func (s *Store) History(ctx context.Context, q HistoryQuery) (History, error) {
	bucket := q.Bucket
	if bucket == "" {
		bucket = BucketHour
	}
	if bucket != BucketHour && bucket != BucketDay {
		return History{}, fmt.Errorf("%w: bucket must be hour or day", ErrInvalid)
	}
	now := s.clock.Now().UTC()
	since := q.Since
	if since.IsZero() {
		since = now.Add(-HistoryDefaultDays * 24 * time.Hour)
	}
	if floor := now.Add(-MaxDays * 24 * time.Hour); since.Before(floor) {
		since = floor
	}
	if since.After(now) {
		since = now
	}
	h := History{Bucket: bucket, Since: since.UTC(), Until: now}
	var rows []Snapshot
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		var first sql.NullString
		if err := tx.QueryRowContext(ctx, "SELECT min(at) FROM usage_snapshots").Scan(&first); err != nil {
			return err
		}
		if t, ok := parseTime(first.String); ok {
			h.FirstAt = t
		}
		query := `SELECT at, account_id, backend, label, enabled, status, plan, quota, tokens_today, cost_today
			FROM usage_snapshots WHERE at >= ? AND at <= ?`
		args := []any{formatTime(since), formatTime(now)}
		if q.AccountID != "" {
			query += " AND account_id = ?"
			args = append(args, q.AccountID)
		}
		r, err := tx.QueryContext(ctx, query+" ORDER BY at, account_id", args...)
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()
		for r.Next() {
			var (
				snap      Snapshot
				at, quota string
				enabled   int
			)
			if err := r.Scan(&at, &snap.AccountID, &snap.Backend, &snap.Label, &enabled, &snap.Status, &snap.Plan,
				&quota, &snap.TokensToday, &snap.CostToday); err != nil {
				return err
			}
			t, ok := parseTime(at)
			if !ok {
				continue
			}
			snap.At, snap.Enabled = t, enabled != 0
			snap.Label = SafeLabel(snap.Label, snap.AccountID)
			if err := json.Unmarshal([]byte(quota), &snap.Quota); err != nil || snap.Quota == nil {
				snap.Quota = []Window{}
			}
			rows = append(rows, snap)
		}
		return r.Err()
	})
	if err != nil {
		return History{}, fmt.Errorf("read usage history: %w", err)
	}
	if bucket == BucketDay {
		h.Days = rollup(rows, s.loc)
		if h.Days == nil {
			h.Days = []Day{}
		}
		return h, nil
	}
	if rows == nil {
		rows = []Snapshot{}
	}
	h.Rows = rows
	return h, nil
}

// rollup folds snapshots (oldest first) into one entry per local day and account.
func rollup(rows []Snapshot, loc *time.Location) []Day {
	type key struct{ date, account string }
	days := map[key]*Day{}
	windows := map[key]map[string]int{} // group + window -> index in Day.Quota
	var order []key
	for i := range rows {
		r := &rows[i]
		k := key{dateOf(r.At, loc), r.AccountID}
		d, ok := days[k]
		if !ok {
			d = &Day{Date: k.date, AccountID: r.AccountID, FirstAt: r.At, Quota: []Window{}}
			days[k], windows[k] = d, map[string]int{}
			order = append(order, k)
		}
		d.Samples++
		// Rows come oldest first: the last one sets the day's account fields and quota readings.
		d.LastAt, d.Backend, d.Label, d.Enabled, d.Status, d.Plan = r.At, r.Backend, r.Label, r.Enabled, r.Status, r.Plan
		for _, w := range r.Quota {
			wk := w.Group + "\x00" + w.Window + "\x00" + w.Label
			rem := w.Remaining
			if j, ok := windows[k][wk]; ok {
				low := min(*d.Quota[j].MinRemaining, rem)
				w.MinRemaining = &low
				d.Quota[j] = w
				continue
			}
			w.MinRemaining = &rem
			windows[k][wk] = len(d.Quota)
			d.Quota = append(d.Quota, w)
		}
		d.TokensToday = max(d.TokensToday, r.TokensToday)
		d.CostToday = max(d.CostToday, r.CostToday)
	}
	sort.SliceStable(order, func(i, j int) bool {
		if order[i].date != order[j].date {
			return order[i].date < order[j].date
		}
		return order[i].account < order[j].account
	})
	out := make([]Day, 0, len(order))
	for _, k := range order {
		out = append(out, *days[k])
	}
	return out
}
