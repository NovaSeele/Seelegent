package proof

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// timeFormat is how times are stored: UTC, fixed width, so they sort as text.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Ledger limits.
const (
	historyKept = 20
	runsKept    = 50
)

// Counter names.
const (
	CounterLaneRefusals = "laneRefusals"
	CounterLaneForced   = "laneForced"
)

// Ledger stores the gate results of spec tasks, the regression runs and the lane counters (migration 0008).
type Ledger struct {
	db  DB
	now func() time.Time
}

// NewLedger returns a ledger on db; now is the clock (time.Now when nil).
func NewLedger(db DB, now func() time.Time) *Ledger {
	if now == nil {
		now = time.Now
	}
	return &Ledger{db: db, now: now}
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeFormat)
}

func parseTime(s string) time.Time {
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// Feature names a feature folder in the ledger: Key is fsutil.Key of the folder, Display what users see.
type Feature struct {
	Key     string
	Display string
}

// Result is a gate's result for one spec task, to record.
type Result struct {
	TaskID  string
	State   string // green, red or error
	Source  string
	Ref     string
	GitHead string
	Cmds    []EntryCmd
}

// historyItem is a former result of an entry.
type historyItem struct {
	State   string `json:"state"`
	At      string `json:"at"`
	Source  string `json:"source"`
	Ref     string `json:"ref,omitempty"`
	GitHead string `json:"gitHead,omitempty"`
}

// Record writes the results of one gate (or one regression command group) for spec tasks of a feature: each
// becomes the task's last result, the former one moves into its history.
func (l *Ledger) Record(ctx context.Context, project string, f Feature, results []Result) error {
	if len(results) == 0 {
		return nil
	}
	now := fmtTime(l.now())
	return l.db.Write(ctx, func(tx *sql.Tx) error {
		for _, r := range results {
			var state, at, source, ref, head, lastGreen, hist string
			err := tx.QueryRowContext(ctx, `SELECT state, at, source, ref, git_head, last_green_at, history
				FROM proof_entries WHERE project_key = ? AND feature_key = ? AND task_id = ?`, project, f.Key, r.TaskID).
				Scan(&state, &at, &source, &ref, &head, &lastGreen, &hist)
			var history []historyItem
			switch {
			case errors.Is(err, sql.ErrNoRows):
			case err != nil:
				return err
			default:
				if err := json.Unmarshal([]byte(hist), &history); err != nil {
					return fmt.Errorf("proof ledger: history of %s#%s: %w", f.Display, r.TaskID, err)
				}
				history = append([]historyItem{{State: state, At: at, Source: source, Ref: ref, GitHead: head}},
					history...)
				if len(history) > historyKept {
					history = history[:historyKept]
				}
			}
			if r.State == api.GateGreen {
				lastGreen = now
			}
			cmds, err := json.Marshal(nonNilCmds(r.Cmds))
			if err != nil {
				return err
			}
			h, err := json.Marshal(nonNilHistory(history))
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO proof_entries (project_key, feature_key, feature, task_id,
				state, at, source, ref, git_head, cmds, last_green_at, history) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)
				ON CONFLICT (project_key, feature_key, task_id) DO UPDATE SET feature = excluded.feature,
				state = excluded.state, at = excluded.at, source = excluded.source, ref = excluded.ref,
				git_head = excluded.git_head, cmds = excluded.cmds, last_green_at = excluded.last_green_at,
				history = excluded.history`, project, f.Key, f.Display, r.TaskID, r.State, now, r.Source, r.Ref,
				r.GitHead, string(cmds), lastGreen, string(h)); err != nil {
				return err
			}
		}
		return nil
	})
}

func nonNilCmds(c []EntryCmd) []EntryCmd {
	if c == nil {
		return []EntryCmd{}
	}
	return c
}

func nonNilHistory(h []historyItem) []historyItem {
	if h == nil {
		return []historyItem{}
	}
	return h
}

// Entries returns the entries of a feature by spec task id.
func (l *Ledger) Entries(ctx context.Context, project, featureKey string) (map[string]Entry, error) {
	out := map[string]Entry{}
	err := l.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT task_id, state, at, source, ref, git_head, cmds, last_green_at
			FROM proof_entries WHERE project_key = ? AND feature_key = ?`, project, featureKey)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var e Entry
			var at, cmds, green string
			if err := rows.Scan(&e.TaskID, &e.State, &at, &e.Source, &e.Ref, &e.GitHead, &cmds, &green); err != nil {
				return err
			}
			if err := json.Unmarshal([]byte(cmds), &e.Cmds); err != nil {
				return fmt.Errorf("proof ledger: commands of %s: %w", e.TaskID, err)
			}
			e.At, e.LastGreenAt = parseTime(at), parseTime(green)
			out[e.TaskID] = e
		}
		return rows.Err()
	})
	return out, err
}

// Features returns the feature folders the ledger has entries for in a project (key → display).
func (l *Ledger) Features(ctx context.Context, project string) ([]Feature, error) {
	var out []Feature
	err := l.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT feature_key, MAX(feature) FROM proof_entries WHERE project_key = ?
			GROUP BY feature_key ORDER BY feature_key`, project)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var f Feature
			if err := rows.Scan(&f.Key, &f.Display); err != nil {
				return err
			}
			out = append(out, f)
		}
		return rows.Err()
	})
	return out, err
}

// Bump adds one to a counter of a project.
func (l *Ledger) Bump(ctx context.Context, project, name string) error {
	return l.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO proof_counters (project_key, name, value) VALUES (?, ?, 1)
			ON CONFLICT (project_key, name) DO UPDATE SET value = value + 1`, project, name)
		return err
	})
}

// Counters returns the counters of a project.
func (l *Ledger) Counters(ctx context.Context, project string) (map[string]int, error) {
	out := map[string]int{}
	err := l.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT name, value FROM proof_counters WHERE project_key = ?`, project)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var n string
			var v int
			if err := rows.Scan(&n, &v); err != nil {
				return err
			}
			out[n] = v
		}
		return rows.Err()
	})
	return out, err
}

// SaveRun writes a regression run (insert or update) and keeps only the latest runsKept of the project.
func (l *Ledger) SaveRun(ctx context.Context, project string, r api.ProofRun) error {
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	return l.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO proof_runs (project_key, id, state, started_at, data)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT (project_key, id) DO UPDATE SET state = excluded.state,
			data = excluded.data`, project, r.ID, r.State, r.StartedAt, string(data)); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM proof_runs WHERE project_key = ? AND id NOT IN (
			SELECT id FROM proof_runs WHERE project_key = ? ORDER BY started_at DESC, id DESC LIMIT ?)`,
			project, project, runsKept)
		return err
	})
}

// ErrNoRun: no regression run with that id in the project.
var ErrNoRun = errors.New("no such regression run")

// Run returns a regression run.
func (l *Ledger) Run(ctx context.Context, project, id string) (api.ProofRun, error) {
	var r api.ProofRun
	err := l.db.Read(ctx, func(tx *sql.Tx) error {
		var data string
		err := tx.QueryRowContext(ctx, `SELECT data FROM proof_runs WHERE project_key = ? AND id = ?`, project, id).
			Scan(&data)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrNoRun, id)
		}
		if err != nil {
			return err
		}
		return json.Unmarshal([]byte(data), &r)
	})
	return r, err
}

// Runs returns the latest regression runs of a project, newest first, without their results.
func (l *Ledger) Runs(ctx context.Context, project string, limit int) ([]api.ProofRun, error) {
	out := []api.ProofRun{}
	err := l.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT data FROM proof_runs WHERE project_key = ?
			ORDER BY started_at DESC, id DESC LIMIT ?`, project, limit)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var data string
			if err := rows.Scan(&data); err != nil {
				return err
			}
			var r api.ProofRun
			if err := json.Unmarshal([]byte(data), &r); err != nil {
				return err
			}
			r.Results = []api.ProofRunResult{}
			out = append(out, r)
		}
		return rows.Err()
	})
	return out, err
}

// AbandonRuns marks the runs still recorded as running as errors: they belonged to an earlier daemon.
func (l *Ledger) AbandonRuns(ctx context.Context) error {
	type row struct{ project, data string }
	var stale []row
	if err := l.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT project_key, data FROM proof_runs WHERE state = ?`, api.RunRunning)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var r row
			if err := rows.Scan(&r.project, &r.data); err != nil {
				return err
			}
			stale = append(stale, r)
		}
		return rows.Err()
	}); err != nil {
		return err
	}
	for _, s := range stale {
		var r api.ProofRun
		if err := json.Unmarshal([]byte(s.data), &r); err != nil {
			return err
		}
		r.State, r.Error = api.RunError, "the daemon stopped during the run"
		if r.EndedAt == "" {
			r.EndedAt = api.FormatTime(l.now())
		}
		if err := l.SaveRun(ctx, s.project, r); err != nil {
			return err
		}
	}
	return nil
}
