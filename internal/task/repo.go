package task

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// timeFormat is how times are stored: UTC, fixed width, so text order is time order.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeFormat)
}

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad time %q: %w", s, err)
	}
	return t.UTC(), nil
}

// NewEvent is an event to append; the log assigns its seq.
type NewEvent struct {
	Type    api.EventType
	Turn    int
	Attempt int
	Data    any
	// TS is when it happened (the clock's now when zero).
	TS time.Time
}

// Repo stores tasks, turns, attempts and the event log in SQLite. Every write that changes a task also appends the
// events that describe the change, in the same transaction.
type Repo struct {
	db  DB
	now func() time.Time
	// backfillBatch is the batch size of BackfillDigests.
	backfillBatch int
}

// NewRepo returns a repository on db. now is the clock (time.Now when nil).
func NewRepo(db DB, now func() time.Time) *Repo {
	if now == nil {
		now = time.Now
	}
	return &Repo{db: db, now: now, backfillBatch: backfillBatch}
}

// Create inserts a new task with its turns and attempts and appends evs. It returns the stored events.
func (r *Repo) Create(ctx context.Context, t *Task, evs []NewEvent) ([]api.TaskEvent, error) {
	var out []api.TaskEvent
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		data, err := json.Marshal(t)
		if err != nil {
			return err
		}
		args := append([]any{t.ID, t.Title, foldText(t.Title), string(t.Status), t.ProjectKey, t.Cwd, t.Backend,
			string(data), formatTime(t.CreatedAt), formatTime(t.UpdatedAt), t.Kind}, digestOf(t).args()...)
		if _, err := tx.ExecContext(ctx, `INSERT INTO tasks
			(id, title, title_fold, status, project_key, cwd, backend, data, created_at, updated_at, kind, `+
			digestColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, args...); err != nil {
			return fmt.Errorf("insert task %s: %w", t.ID, err)
		}
		if err := saveTurns(ctx, tx, t, t.Turns); err != nil {
			return err
		}
		out, err = r.append(ctx, tx, t.ID, evs)
		return err
	})
	if err != nil {
		return nil, err
	}
	r.applySeq(t, out)
	return out, nil
}

// Save writes the task record and the given turns (with their attempts; every turn when turns is nil) and appends
// evs, in one transaction. It returns the stored events. An attempt's prompt is written only when the attempt is
// new; it never changes afterwards.
func (r *Repo) Save(ctx context.Context, t *Task, turns []*Turn, evs []NewEvent) ([]api.TaskEvent, error) {
	if turns == nil {
		turns = t.Turns
	}
	var out []api.TaskEvent
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		data, err := json.Marshal(t)
		if err != nil {
			return err
		}
		// The digest comes from t.Turns, which holds every turn even when only some are written.
		args := append([]any{t.Title, foldText(t.Title), string(t.Status), t.ProjectKey, t.Cwd, t.Backend,
			string(data), formatTime(t.UpdatedAt), t.Kind}, digestOf(t).args()...)
		res, err := tx.ExecContext(ctx, `UPDATE tasks SET title = ?, title_fold = ?, status = ?, project_key = ?,
			cwd = ?, backend = ?, data = ?, updated_at = ?, kind = ?, turn_count = ?, tokens_used = ?,
			files_changed = ?, gate_turn = ?, gate_state = ?, last_account_id = ?, last_account_label = ?
			WHERE id = ?`, append(args, t.ID)...)
		if err != nil {
			return fmt.Errorf("save task %s: %w", t.ID, err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: task %s", ErrNotFound, t.ID)
		}
		if err := saveTurns(ctx, tx, t, turns); err != nil {
			return err
		}
		out, err = r.append(ctx, tx, t.ID, evs)
		return err
	})
	if err != nil {
		return nil, err
	}
	r.applySeq(t, out)
	return out, nil
}

// Offsets are the committed stream offsets of one attempt.
type Offsets struct {
	Turn, Attempt  int
	Stream, Stderr int64
}

// Append appends evs to the log of task id and, when off is not nil, commits the attempt's stream offsets, in one
// transaction: after a restart every event of the bytes before the offsets is in the log and none after them. The
// task record itself is not written.
func (r *Repo) Append(ctx context.Context, id string, evs []NewEvent, off *Offsets) ([]api.TaskEvent, error) {
	var out []api.TaskEvent
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		if off != nil {
			res, err := tx.ExecContext(ctx, `UPDATE attempts SET data = json_set(data, '$.streamOffset', ?,
				'$.stderrOffset', ?) WHERE task_id = ? AND turn = ? AND n = ?`, off.Stream, off.Stderr, id, off.Turn,
				off.Attempt)
			if err != nil {
				return fmt.Errorf("commit offsets: %w", err)
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("%w: attempt %d of turn %d of task %s", ErrNotFound, off.Attempt, off.Turn, id)
			}
		}
		var err error
		out, err = r.append(ctx, tx, id, evs)
		return err
	})
	return out, err
}
func (r *Repo) applySeq(t *Task, out []api.TaskEvent) {
	if len(out) == 0 {
		return
	}
	last := out[len(out)-1]
	t.LastEventSeq = last.Seq
	if at, err := api.ParseTime(last.TS); err == nil {
		t.LastEventAt = at
	}
}

func (r *Repo) append(ctx context.Context, tx *sql.Tx, id string, evs []NewEvent) ([]api.TaskEvent, error) {
	if len(evs) == 0 {
		return nil, nil
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT last_event_seq FROM tasks WHERE id = ?`, id).Scan(&seq); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: task %s", ErrNotFound, id)
		}
		return nil, err
	}
	out := make([]api.TaskEvent, 0, len(evs))
	var last time.Time
	for _, e := range evs {
		ts := e.TS
		if ts.IsZero() {
			ts = r.now()
		}
		ts = ts.UTC().Truncate(time.Millisecond)
		data := e.Data
		if data == nil {
			data = api.EmptyData{}
		}
		raw, err := json.Marshal(data)
		if err != nil {
			return nil, fmt.Errorf("event %s: %w", e.Type, err)
		}
		seq++
		if _, err := tx.ExecContext(ctx, `INSERT INTO task_events (task_id, seq, ts, type, turn, attempt, data)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, id, seq, formatTime(ts), string(e.Type), e.Turn, e.Attempt,
			string(raw)); err != nil {
			return nil, fmt.Errorf("append event: %w", err)
		}
		last = ts
		out = append(out, api.TaskEvent{Seq: seq, TS: api.FormatTime(ts), TaskID: id, Turn: e.Turn,
			Attempt: e.Attempt, Type: e.Type, Data: json.RawMessage(raw)})
	}
	if _, err := tx.ExecContext(ctx, `UPDATE tasks SET last_event_seq = ?, last_event_at = ? WHERE id = ?`,
		seq, formatTime(last), id); err != nil {
		return nil, err
	}
	return out, nil
}

func saveTurns(ctx context.Context, tx *sql.Tx, t *Task, turns []*Turn) error {
	for _, tu := range turns {
		data, err := json.Marshal(tu)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO turns (task_id, n, status, data) VALUES (?, ?, ?, ?)
			ON CONFLICT (task_id, n) DO UPDATE SET status = excluded.status, data = excluded.data`,
			t.ID, tu.N, tu.Status, string(data)); err != nil {
			return fmt.Errorf("save turn %d of %s: %w", tu.N, t.ID, err)
		}
		for _, a := range tu.Attempts {
			if err := saveAttempt(ctx, tx, t.ID, tu.N, a); err != nil {
				return err
			}
		}
	}
	return nil
}

func saveAttempt(ctx context.Context, tx *sql.Tx, taskID string, turn int, a *Attempt) error {
	data, err := json.Marshal(a)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO attempts (task_id, turn, n, account_id, status, prompt, data)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (task_id, turn, n) DO UPDATE SET account_id = excluded.account_id, status = excluded.status,
		data = excluded.data`, taskID, turn, a.N, a.AccountID, a.Status, a.Prompt, string(data)); err != nil {
		return fmt.Errorf("save attempt %d of turn %d of %s: %w", a.N, turn, taskID, err)
	}
	return nil
}

// Get returns the task with its turns and attempts (without prompts), or ErrNotFound.
func (r *Repo) Get(ctx context.Context, id string) (*Task, error) {
	var t *Task
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		row := tx.QueryRowContext(ctx, `SELECT `+taskColumns+` FROM tasks WHERE id = ?`, id)
		var err error
		t, err = scanTask(row)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: task %s", ErrNotFound, id)
		}
		if err != nil {
			return err
		}
		return loadTurns(ctx, tx, []*Task{t})
	})
	if err != nil {
		return nil, err
	}
	return t, nil
}

// IDs returns the ids of the tasks with one of the statuses, oldest first.
func (r *Repo) IDs(ctx context.Context, statuses ...Status) ([]string, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	args := make([]any, len(statuses))
	for i, s := range statuses {
		args[i] = string(s)
	}
	var ids []string
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		//nolint:gosec // only placeholders are concatenated
		rows, err := tx.QueryContext(ctx, `SELECT id FROM tasks WHERE status IN (`+placeholders(len(args))+`)
			ORDER BY created_at, id`, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	return ids, err
}

// AttentionIDs returns the tasks in statuses whose stored record carries an attention flag.
func (r *Repo) AttentionIDs(ctx context.Context, statuses ...Status) ([]string, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	args := make([]any, len(statuses))
	for i, s := range statuses {
		args[i] = string(s)
	}
	var ids []string
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		//nolint:gosec // only placeholders are concatenated
		rows, err := tx.QueryContext(ctx, `SELECT id FROM tasks WHERE status IN (`+placeholders(len(args))+`)
			AND json_extract(data, '$.attention') IS NOT NULL ORDER BY created_at, id`, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return rows.Err()
	})
	return ids, err
}

// BackfillFold fills title_fold for rows written before the column existed (migration 0006).
func (r *Repo) BackfillFold(ctx context.Context) error {
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT id, title FROM tasks WHERE title_fold = '' AND title <> ''`)
		if err != nil {
			return err
		}
		titles := map[string]string{}
		for rows.Next() {
			var id, title string
			if err := rows.Scan(&id, &title); err != nil {
				_ = rows.Close()
				return err
			}
			titles[id] = title
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for id, title := range titles {
			if _, err := tx.ExecContext(ctx, `UPDATE tasks SET title_fold = ? WHERE id = ?`, foldText(title), id); err != nil {
				return err
			}
		}
		return nil
	})
}

// Query filters the task list.
type Query struct {
	Statuses   []Status
	ProjectKey string
	// Text matches the title or the id, case-insensitively.
	Text string
	// Limit is 1..200 (50 when 0).
	Limit  int
	Cursor string
}

// List returns one page of tasks (without their turns), newest first by (created_at, id), and the cursor of the
// next page ("" on the last page).
func (r *Repo) List(ctx context.Context, q Query) ([]*Task, string, error) {
	ls, next, err := r.listPage(ctx, q)
	if err != nil {
		return nil, "", err
	}
	tasks := make([]*Task, len(ls))
	for i, l := range ls {
		tasks[i] = l.t
	}
	return tasks, next, nil
}

// listPage is List with the digest of each task's turns, which are not loaded (only those of tasks stored before
// the digest columns existed).
func (r *Repo) listPage(ctx context.Context, q Query) ([]listed, string, error) {
	sqlText, args, limit, err := listQuery(q)
	if err != nil {
		return nil, "", err
	}
	var tasks []listed
	err = r.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		tasks, err = queryListed(ctx, tx, sqlText, args...)
		return err
	})
	if err != nil {
		return nil, "", err
	}
	next := ""
	if len(tasks) > limit {
		tasks = tasks[:limit]
		last := tasks[len(tasks)-1].t
		next = encodeCursor(formatTime(last.CreatedAt), last.ID)
	}
	return tasks, next, nil
}

// listQuery builds the query of a list page; it asks for one row more than the page, to know whether another
// page follows.
func listQuery(q Query) (sqlText string, args []any, limit int, err error) {
	limit = q.Limit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		return "", nil, 0, invalidf("limit %d is over 200", limit)
	}
	// Hidden runs (verify, consult, meeting, judge) are read through what started them, never listed.
	where := []string{`kind NOT IN (` + placeholders(len(hiddenKinds())) + `)`}
	for _, k := range hiddenKinds() {
		args = append(args, k)
	}
	if len(q.Statuses) > 0 {
		for _, s := range q.Statuses {
			if !ValidStatus(s) {
				return "", nil, 0, invalidf("unknown status %q", s)
			}
			args = append(args, string(s))
		}
		where = append(where, `status IN (`+placeholders(len(q.Statuses))+`)`)
	}
	if q.ProjectKey != "" {
		where = append(where, `project_key = ?`)
		args = append(args, q.ProjectKey)
	}
	if text := strings.TrimSpace(q.Text); text != "" {
		// title_fold and the search are folded in Go (foldText): SQLite's LIKE ignores the case of ASCII only.
		where = append(where, `(title_fold LIKE ? ESCAPE '\' OR id LIKE ? ESCAPE '\')`)
		like := "%" + escapeLike(foldText(text)) + "%"
		args = append(args, like, like)
	}
	if q.Cursor != "" {
		at, id, cerr := decodeCursor(q.Cursor)
		if cerr != nil {
			return "", nil, 0, cerr
		}
		where = append(where, `(created_at < ? OR (created_at = ? AND id < ?))`)
		args = append(args, at, at, id)
	}
	sqlText = `SELECT ` + listedColumns + ` FROM tasks WHERE ` +
		strings.Join(where, " AND ") + ` ORDER BY created_at DESC, id DESC LIMIT ?`
	return sqlText, append(args, limit+1), limit, nil
}

// Prompt returns the brief of attempt n of turn (the latest attempt when n is 0).
func (r *Repo) Prompt(ctx context.Context, id string, turn, n int) (string, error) {
	var prompt string
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		var row *sql.Row
		if n > 0 {
			row = tx.QueryRowContext(ctx, `SELECT prompt FROM attempts WHERE task_id = ? AND turn = ? AND n = ?`,
				id, turn, n)
		} else {
			row = tx.QueryRowContext(ctx, `SELECT prompt FROM attempts WHERE task_id = ? AND turn = ?
				ORDER BY n DESC LIMIT 1`, id, turn)
		}
		err := row.Scan(&prompt)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var one int
		switch err := tx.QueryRowContext(ctx, `SELECT 1 FROM turns WHERE task_id = ? AND n = ?`, id, turn).
			Scan(&one); {
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("%w: task %s has no turn %d", ErrNotFound, id, turn)
		case err != nil:
			return err
		case n > 0:
			return fmt.Errorf("%w: turn %d of task %s has no attempt %d", ErrNotFound, turn, id, n)
		}
		return fmt.Errorf("%w: turn %d of task %s has not started an attempt yet", ErrNotFound, turn, id)
	})
	return prompt, err
}

// Events returns up to limit events with seq > after, ascending.
func (r *Repo) Events(ctx context.Context, id string, after int64, limit int) ([]api.TaskEvent, error) {
	return r.events(ctx, id, `SELECT seq, ts, type, turn, attempt, data FROM task_events
		WHERE task_id = ? AND seq > ? ORDER BY seq LIMIT ?`, id, after, limit)
}

// Tail returns the newest n events, ascending.
func (r *Repo) Tail(ctx context.Context, id string, n int) ([]api.TaskEvent, error) {
	return r.events(ctx, id, `SELECT seq, ts, type, turn, attempt, data FROM (
		SELECT seq, ts, type, turn, attempt, data FROM task_events WHERE task_id = ? ORDER BY seq DESC LIMIT ?)
		ORDER BY seq`, id, n)
}

func (r *Repo) events(ctx context.Context, id, query string, args ...any) ([]api.TaskEvent, error) {
	var out []api.TaskEvent
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, query, args...)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var e api.TaskEvent
			var ts, typ, data string
			if err := rows.Scan(&e.Seq, &ts, &typ, &e.Turn, &e.Attempt, &data); err != nil {
				return err
			}
			at, err := parseTime(ts)
			if err != nil {
				return err
			}
			e.TS = api.FormatTime(at)
			e.TaskID = id
			e.Type = api.EventType(typ)
			e.Data = json.RawMessage(data)
			out = append(out, e)
		}
		return rows.Err()
	})
	return out, err
}

const taskColumns = `id, status, data, last_event_seq, last_event_at, created_at, updated_at`

type scanner interface{ Scan(dest ...any) error }

func scanTask(s scanner) (*Task, error) {
	var id, status, data, lastAt, created, updated string
	var seq int64
	if err := s.Scan(&id, &status, &data, &seq, &lastAt, &created, &updated); err != nil {
		return nil, err
	}
	return decodeTask(id, status, data, seq, lastAt, created, updated)
}

// decodeTask builds a task from the values of taskColumns.
func decodeTask(id, status, data string, seq int64, lastAt, created, updated string) (*Task, error) {
	t := &Task{}
	if err := json.Unmarshal([]byte(data), t); err != nil {
		return nil, fmt.Errorf("task %s is unreadable: %w", id, err)
	}
	t.ID = id
	t.Status = Status(status)
	t.LastEventSeq = seq
	var err error
	if t.LastEventAt, err = parseTime(lastAt); err != nil {
		return nil, err
	}
	if t.CreatedAt, err = parseTime(created); err != nil {
		return nil, err
	}
	if t.UpdatedAt, err = parseTime(updated); err != nil {
		return nil, err
	}
	return t, nil
}

// loadTurns fills the turns and attempts of tasks (prompts are left out).
func loadTurns(ctx context.Context, tx *sql.Tx, tasks []*Task) error {
	if len(tasks) == 0 {
		return nil
	}
	byID := make(map[string]*Task, len(tasks))
	args := make([]any, 0, len(tasks))
	for _, t := range tasks {
		t.Turns = nil
		byID[t.ID] = t
		args = append(args, t.ID)
	}
	in := placeholders(len(args))
	//nolint:gosec // only placeholders are concatenated
	rows, err := tx.QueryContext(ctx, `SELECT task_id, data FROM turns WHERE task_id IN (`+in+`)
		ORDER BY task_id, n`, args...)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id, data string
		if err := rows.Scan(&id, &data); err != nil {
			_ = rows.Close()
			return err
		}
		tu := &Turn{}
		if err := json.Unmarshal([]byte(data), tu); err != nil {
			_ = rows.Close()
			return fmt.Errorf("a turn of task %s is unreadable: %w", id, err)
		}
		byID[id].Turns = append(byID[id].Turns, tu)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	//nolint:gosec // only placeholders are concatenated
	rows, err = tx.QueryContext(ctx, `SELECT task_id, turn, data FROM attempts WHERE task_id IN (`+in+`)
		ORDER BY task_id, turn, n`, args...)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, data string
		var turn int
		if err := rows.Scan(&id, &turn, &data); err != nil {
			return err
		}
		a := &Attempt{}
		if err := json.Unmarshal([]byte(data), a); err != nil {
			return fmt.Errorf("an attempt of task %s is unreadable: %w", id, err)
		}
		tu := byID[id].TurnN(turn)
		if tu == nil {
			return fmt.Errorf("attempt %d of task %s has no turn %d", a.N, id, turn)
		}
		tu.Attempts = append(tu.Attempts, a)
	}
	return rows.Err()
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func encodeCursor(createdAt, id string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(createdAt + "|" + id))
}

func decodeCursor(c string) (createdAt, id string, err error) {
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil {
		return "", "", invalidf("bad cursor")
	}
	createdAt, id, ok := strings.Cut(string(raw), "|")
	if !ok || id == "" {
		return "", "", invalidf("bad cursor")
	}
	if _, err := parseTime(createdAt); err != nil {
		return "", "", invalidf("bad cursor")
	}
	return createdAt, id, nil
}
