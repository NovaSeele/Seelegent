// Package memory keeps the per-project notes the lead writes for workers and the lead's review verdicts. Both are
// keyed by the project key of the folder (fsutil.ProjectKey), so every spelling of one folder shares them, and both
// live in seelegent.db (migration 0005): the notes in project_memory, the verdicts in review_history. The notes are
// capped at MaxBytes; an append or replace that would exceed the cap is refused, never truncated.
package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
)

// MaxBytes caps the notes of one project. The old version had no cap; the notes go into every worker brief, so a
// runaway append would make every brief huge.
const MaxBytes = 64 << 10

// HistoryShown is how many verdicts a worker brief shows.
const HistoryShown = 8

// notesChars caps the review notes kept in one history line.
const notesChars = 200

// Errors.
var (
	// ErrInvalid: cwd is not an absolute path to an existing folder.
	ErrInvalid = errors.New("invalid")
	// ErrTooLarge: the notes would exceed MaxBytes.
	ErrTooLarge = errors.New("too large")
)

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// timeFormat is how times are stored: UTC, fixed width.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Store reads and writes project memory.
type Store struct {
	db  DB
	now func() time.Time
}

// New returns a store on db. now is the clock (time.Now when nil).
func New(db DB, now func() time.Time) *Store {
	if now == nil {
		now = time.Now
	}
	return &Store{db: db, now: now}
}

// Note is the memory of one project.
type Note struct {
	Cwd  string
	Key  string
	Text string
	// UpdatedAt is zero when the project has no notes.
	UpdatedAt time.Time
}

// API returns the note as the API shows it.
func (n Note) API() api.Memory {
	return api.Memory{Cwd: n.Cwd, Text: n.Text, UpdatedAt: api.FormatTime(n.UpdatedAt)}
}

// Project returns the project key and canonical folder of cwd, which must be an absolute path to an existing
// folder.
func Project(cwd string) (key, canonical string, err error) {
	canonical, key, _, err = fsutil.ProjectOf(cwd)
	if err != nil {
		return "", "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return key, canonical, nil
}

// Get returns the notes of the project of cwd (empty text when there are none).
func (s *Store) Get(ctx context.Context, cwd string) (Note, error) {
	key, canonical, err := Project(cwd)
	if err != nil {
		return Note{}, err
	}
	n := Note{Cwd: canonical, Key: key}
	err = s.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		n, err = get(ctx, tx, n)
		return err
	})
	return n, err
}

func get(ctx context.Context, tx *sql.Tx, n Note) (Note, error) {
	var at string
	err := tx.QueryRowContext(ctx, `SELECT text, updated_at FROM project_memory WHERE project_key = ?`, n.Key).
		Scan(&n.Text, &at)
	if errors.Is(err, sql.ErrNoRows) {
		return n, nil
	}
	if err != nil {
		return Note{}, err
	}
	if n.UpdatedAt, err = time.Parse(time.RFC3339Nano, at); err != nil {
		return Note{}, fmt.Errorf("project memory of %s is unreadable: %w", n.Key, err)
	}
	return n, nil
}

// Append adds text (trimmed, on its own line) to the notes of the project of cwd and returns the result.
func (s *Store) Append(ctx context.Context, cwd, text string) (Note, error) {
	return s.write(ctx, cwd, func(old string) string {
		text = strings.TrimSpace(text)
		if text == "" {
			return old
		}
		return old + text + "\n"
	})
}

// Replace sets the notes of the project of cwd to text (ending with a line break) and returns the result. An empty
// text clears them.
func (s *Store) Replace(ctx context.Context, cwd, text string) (Note, error) {
	return s.write(ctx, cwd, func(string) string {
		if strings.TrimSpace(text) == "" {
			return ""
		}
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return text
	})
}

func (s *Store) write(ctx context.Context, cwd string, change func(old string) string) (Note, error) {
	key, canonical, err := Project(cwd)
	if err != nil {
		return Note{}, err
	}
	n := Note{Cwd: canonical, Key: key}
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		cur, err := get(ctx, tx, n)
		if err != nil {
			return err
		}
		text := change(cur.Text)
		if len(text) > MaxBytes {
			return fmt.Errorf("%w: project memory would be %d bytes, over the %d byte cap", ErrTooLarge, len(text),
				MaxBytes)
		}
		at := s.now().UTC()
		if _, err := tx.ExecContext(ctx, `INSERT INTO project_memory (project_key, cwd, text, updated_at)
			VALUES (?, ?, ?, ?) ON CONFLICT (project_key) DO UPDATE SET cwd = excluded.cwd, text = excluded.text,
			updated_at = excluded.updated_at`, key, canonical, text, at.Format(timeFormat)); err != nil {
			return err
		}
		n.Text, n.UpdatedAt = text, at
		return nil
	})
	if err != nil {
		return Note{}, err
	}
	return n, nil
}

// Verdict is one review verdict of the lead.
type Verdict struct {
	// Cwd is the task's folder.
	Cwd     string
	TaskID  string
	Title   string
	Verdict string
	Notes   string
}

// AddVerdict records a verdict in the project's history.
func (s *Store) AddVerdict(ctx context.Context, v Verdict) error {
	key, _, err := Project(v.Cwd)
	if err != nil {
		return err
	}
	notes := strings.Join(strings.Fields(v.Notes), " ")
	if r := []rune(notes); len(r) > notesChars {
		notes = string(r[:notesChars])
	}
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO review_history (project_key, at, task_id, title, verdict, notes)
			VALUES (?, ?, ?, ?, ?, ?)`, key, s.now().UTC().Format(timeFormat), v.TaskID, v.Title, v.Verdict, notes)
		return err
	})
}

// History returns the newest n verdicts of the project of cwd, oldest first, as brief lines:
// `- 2026-11-07T09:30 <task id> "<title>": accepted - <notes>`.
func (s *Store) History(ctx context.Context, cwd string, n int) ([]string, error) {
	key, _, err := Project(cwd)
	if err != nil {
		return nil, err
	}
	var lines []string
	err = s.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT at, task_id, title, verdict, notes FROM (
			SELECT id, at, task_id, title, verdict, notes FROM review_history WHERE project_key = ?
			ORDER BY id DESC LIMIT ?) ORDER BY id`, key, n)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var at, id, title, verdict, notes string
			if err := rows.Scan(&at, &id, &title, &verdict, &notes); err != nil {
				return err
			}
			t, err := time.Parse(time.RFC3339Nano, at)
			if err != nil {
				return fmt.Errorf("review history of %s is unreadable: %w", key, err)
			}
			line := fmt.Sprintf("- %s %s %q: %s", t.UTC().Format("2006-01-02T15:04"), id, title, verdict)
			if notes != "" {
				line += " - " + notes
			}
			lines = append(lines, line)
		}
		return rows.Err()
	})
	return lines, err
}
