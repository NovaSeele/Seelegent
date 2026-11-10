// Package memory keeps the per-project notes the lead writes for workers and the lead's review verdicts. Both are
// keyed by the project key of the folder (projectkey.Key), so every spelling of one folder shares them, and both
// live in seelegent.db (migration 0005): the notes in project_memory, the verdicts in review_history. The notes are
// capped at MaxBytes; an append, replace or lesson that would exceed the cap is refused, never truncated.
//
// Lessons are the one-line rules the lead keeps from reviews (AddLesson): bullets in the LessonsHeading section of
// the notes, so every later brief of the project carries them, unlike the verdicts, of which a brief shows only the
// newest HistoryShown.
package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/projectkey"
)

// MaxBytes caps the notes of one project. The old version had no cap; the notes go into every worker brief, so a
// runaway append would make every brief huge.
const MaxBytes = 64 << 10

// HistoryShown is how many verdicts a worker brief shows.
const HistoryShown = 8

// notesChars caps the review notes kept in one history line.
const notesChars = 200

// LessonsHeading heads the section of the notes that holds the lessons from reviews.
const LessonsHeading = "## Lessons from reviews"

// MaxLessonBytes caps one lesson, after its white space is collapsed: a lesson is one line, not a paragraph.
const MaxLessonBytes = 300

// Errors.
var (
	// ErrInvalid: cwd is not an absolute path to an existing folder, or a lesson is empty or over MaxLessonBytes.
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
	canonical, key, _, err = projectkey.Of(cwd)
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
	return s.write(ctx, cwd, func(old string) (string, bool) {
		text = strings.TrimSpace(text)
		if text == "" {
			return old, true
		}
		return old + text + "\n", true
	})
}

// Replace sets the notes of the project of cwd to text (ending with a line break) and returns the result. An empty
// text clears them.
func (s *Store) Replace(ctx context.Context, cwd, text string) (Note, error) {
	return s.write(ctx, cwd, func(string) (string, bool) {
		if strings.TrimSpace(text) == "" {
			return "", true
		}
		if !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return text, true
	})
}

// AddLesson adds rule as a bullet ("- <rule>") at the end of the LessonsHeading section of the notes of the project
// of cwd, creating that section at the end of the notes when they have none, and returns the result. The rule is
// one line (see Lesson); an empty rule or one over MaxLessonBytes is ErrInvalid. A rule the section already has
// (ignoring case) changes nothing: the notes come back as they are. The cap is Append's: notes that would exceed
// MaxBytes are ErrTooLarge and stay as they were.
func (s *Store) AddLesson(ctx context.Context, cwd, rule string) (Note, error) {
	rule, err := Lesson(rule)
	if err != nil {
		return Note{}, err
	}
	return s.write(ctx, cwd, func(old string) (string, bool) { return addLesson(old, rule) })
}

// Lesson returns rule as AddLesson stores it: trimmed, every run of white space (line breaks included) collapsed to
// one space, and a leading "- " or "* " dropped. An empty rule or one over MaxLessonBytes is ErrInvalid.
func Lesson(rule string) (string, error) {
	rule = oneLine(rule)
	for _, mark := range []string{"- ", "* "} {
		if r, isBullet := strings.CutPrefix(rule, mark); isBullet {
			rule = strings.TrimSpace(r)
			break
		}
	}
	switch {
	case rule == "" || rule == "-" || rule == "*":
		return "", fmt.Errorf("%w: the rule is empty", ErrInvalid)
	case len(rule) > MaxLessonBytes:
		return "", fmt.Errorf("%w: the rule is %d bytes; a lesson is one line of at most %d bytes", ErrInvalid,
			len(rule), MaxLessonBytes)
	}
	return rule, nil
}

// oneLine collapses every run of white space in s, line breaks included, to one space and trims it.
func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// addLesson returns notes with the bullet of rule at the end of their lessons section (a new section at the end of
// the notes when they have none), or notes and false when the section already has the rule. Notes written with
// CRLF line ends keep them.
func addLesson(notes, rule string) (string, bool) {
	eol := "\n"
	if strings.Contains(notes, "\r\n") {
		eol = "\r\n"
	}
	bullet := "- " + rule
	if strings.TrimSpace(notes) == "" {
		return LessonsHeading + eol + bullet + eol, true
	}
	lines := strings.Split(strings.TrimSuffix(notes, "\n"), "\n")
	start, end := lessonsSection(lines)
	if start < 0 {
		return strings.TrimRight(notes, "\r\n") + eol + eol + LessonsHeading + eol + bullet + eol, true
	}
	last := start
	for i := start + 1; i < end; i++ {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		last = i
		for _, mark := range []string{"- ", "* "} {
			if b, isBullet := strings.CutPrefix(l, mark); isBullet && strings.EqualFold(oneLine(b), rule) {
				return notes, false
			}
		}
	}
	lines = slices.Insert(lines, last+1, strings.TrimSuffix(bullet+eol, "\n"))
	return strings.Join(lines, "\n") + "\n", true
}

// lessonsSection finds the lessons section in lines: the index of its heading (-1 when there is none) and the index
// after its last line (that of the next heading of level 1 or 2, or len(lines)). Lines in fenced code are never
// headings.
func lessonsSection(lines []string) (start, end int) {
	start = -1
	fenced := false
	for i, l := range lines {
		t := strings.TrimRight(l, " \t\r")
		if f := strings.TrimLeft(t, " "); strings.HasPrefix(f, "```") || strings.HasPrefix(f, "~~~") {
			fenced = !fenced
			continue
		}
		if fenced {
			continue
		}
		switch {
		case start < 0 && t == LessonsHeading:
			start = i
		case start >= 0 && (t == "#" || t == "##" || strings.HasPrefix(t, "# ") || strings.HasPrefix(t, "## ")):
			return start, i
		}
	}
	return start, len(lines)
}

// write runs change on the notes of the project of cwd and saves the result, in one transaction. A change that
// answers false leaves the notes, and their time, as they are.
func (s *Store) write(ctx context.Context, cwd string, change func(old string) (string, bool)) (Note, error) {
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
		text, changed := change(cur.Text)
		if !changed {
			n = cur
			return nil
		}
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

// CountVerdicts looks at the newest n verdicts of the project of cwd and returns how many of them are verdict and
// how many there are (at most n).
func (s *Store) CountVerdicts(ctx context.Context, cwd string, n int, verdict string) (matched, total int, err error) {
	key, _, err := Project(cwd)
	if err != nil {
		return 0, 0, err
	}
	err = s.db.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT COALESCE(SUM(verdict = ?), 0), COUNT(*) FROM (
			SELECT verdict FROM review_history WHERE project_key = ? ORDER BY id DESC LIMIT ?)`, verdict, key, n).
			Scan(&matched, &total)
	})
	if err != nil {
		return 0, 0, err
	}
	return matched, total, nil
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
