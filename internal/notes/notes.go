// Package notes keeps the review notes on a task's diff (docs/team.md): line notes the user or the lead leaves on
// the changes of a task, sent to the worker as one followup and moved with their line after each new turn. Notes
// are rows of review_notes in seelegent.db (old bug D6: they were one JSON file per task, reset on a read error).
package notes

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/fsutil"
	"github.com/NovaSeele/Seelegent/internal/task"
)

// Errors; the HTTP API maps them to not_found, invalid_argument and bad_state.
var (
	ErrNotFound = errors.New("not found")
	ErrInvalid  = errors.New("invalid")
	ErrBadState = errors.New("bad state")
)

// Limits.
const (
	// ReanchorWindow is how far (in lines) a note looks for its anchored text after the file changed.
	ReanchorWindow = 50
	maxBody        = 8000
	maxAnchor      = 500
	maxExtra       = 20000
	// maxFile caps the files read to re-anchor notes.
	maxFile = 8 << 20
)

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// Tasks is the task engine (task.Service).
type Tasks interface {
	Get(ctx context.Context, id string) (api.TaskDetail, error)
	Followup(ctx context.Context, id string, req api.FollowupRequest) (api.TaskSummary, error)
}

// Options configure a Store. DB and Tasks are required.
type Options struct {
	DB    DB
	Tasks Tasks
	// Root returns the repository root of a folder (gitx.Repos.Root); the task's folder is used when nil or when
	// it fails.
	Root func(ctx context.Context, dir string) (string, error)
	Now  func() time.Time
	// Changed is called after the notes of a task changed (notes.changed).
	Changed func(taskID string)
}

// Store reads and writes review notes.
type Store struct {
	o Options
	// mu makes send (pick, followup, mark) one step per store.
	mu sync.Mutex
}

// New returns a store.
func New(o Options) *Store {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Changed == nil {
		o.Changed = func(string) {}
	}
	return &Store{o: o}
}

const timeFormat = "2006-01-02T15:04:05.000000000Z"

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, args...))
}

// busy reports whether a task's turn is still going (its notes cannot be sent or re-anchored).
func busy(s api.TaskStatus) bool {
	return s == api.StatusQueued || s == api.StatusRunning || s == api.StatusWaitingQuota ||
		s == api.StatusWaitingDeps
}

// taskOf returns the task of notes; hidden runs have none.
func (s *Store) taskOf(ctx context.Context, id string) (api.TaskDetail, error) {
	d, err := s.o.Tasks.Get(ctx, id)
	if errors.Is(err, task.ErrNotFound) || (err == nil && task.Hidden(d.Kind)) {
		return api.TaskDetail{}, fmt.Errorf("%w: no task %s", ErrNotFound, id)
	}
	return d, err
}

// root is the folder note files are relative to: the task's repository, or its folder outside git.
func (s *Store) root(ctx context.Context, d api.TaskDetail) string {
	if d.RepoRoot != "" {
		return d.RepoRoot
	}
	if s.o.Root != nil {
		if r, err := s.o.Root(ctx, d.Cwd); err == nil && r != "" {
			return r
		}
	}
	return d.Cwd
}

// normFile makes file relative to root with '/'; it must stay inside root, compared the way the file system does
// (case, 8.3 names, junctions: fsutil.Within, as proof.CheckCite does), and must not lead out through a link.
func normFile(root, file string) (string, error) {
	file = strings.TrimSpace(file)
	if file == "" {
		return "", invalidf("file is required")
	}
	p := filepath.FromSlash(file)
	if !filepath.IsAbs(p) {
		p = filepath.Join(root, p)
	}
	p = filepath.Clean(p)
	outside := invalidf("file is outside the task's repository: %s", file)
	if in, err := fsutil.Within(root, p); err != nil || !in {
		return "", outside
	}
	if c, err := fsutil.Canonical(p); err == nil {
		if in, err := fsutil.Within(root, c); err != nil || !in {
			return "", invalidf("file leads outside the task's repository: %s", file)
		}
	}
	rel, err := filepath.Rel(root, p)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) ||
		filepath.IsAbs(rel) {
		// Inside by key but not by spelling (another case or an 8.3 name): spell both the canonical way.
		cr, err1 := fsutil.Canonical(root)
		cp, err2 := fsutil.Canonical(p)
		if err1 != nil || err2 != nil {
			return "", outside
		}
		if rel, err = filepath.Rel(cr, cp); err != nil || rel == "." || strings.HasPrefix(rel, "..") {
			return "", outside
		}
	}
	return filepath.ToSlash(rel), nil
}

// fileLines reads a file of the repository as lines (nil when it cannot be read).
type fileLines struct {
	root  string
	cache map[string][]string
}

func (f *fileLines) get(rel string) []string {
	if l, ok := f.cache[rel]; ok {
		return l
	}
	var lines []string
	p := filepath.Join(f.root, filepath.FromSlash(rel))
	if c, err := fsutil.Canonical(p); err == nil {
		if in, err := fsutil.Within(f.root, c); err != nil || !in {
			f.cache[rel] = nil // a link out of the repository is not read
			return nil
		}
	}
	if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() && fi.Size() <= maxFile {
		if data, err := os.ReadFile(p); err == nil {
			lines = strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
		}
	}
	f.cache[rel] = lines
	return lines
}

func sameLine(a, b string) bool {
	return strings.TrimRight(a, " \t\r\n") == strings.TrimRight(b, " \t\r\n")
}

// Reanchor finds the note's anchored text in lines (the file now; nil when it is gone), at its line or within
// ReanchorWindow lines of it. It returns the new line and end line, and whether the text was found. A note without
// anchor text stays where it is.
func Reanchor(n api.Note, lines []string) (line, endLine int, found bool) {
	if n.AnchorText == "" {
		return n.Line, n.EndLine, true
	}
	if lines == nil {
		return n.Line, n.EndLine, false
	}
	hit := func(i int) bool { return i >= 1 && i <= len(lines) && sameLine(lines[i-1], n.AnchorText) }
	if hit(n.Line) {
		return n.Line, n.EndLine, true
	}
	for d := 1; d <= ReanchorWindow; d++ {
		for _, i := range []int{n.Line - d, n.Line + d} {
			if hit(i) {
				end := n.EndLine
				if end != 0 {
					end += i - n.Line
				}
				return i, end, true
			}
		}
	}
	return n.Line, n.EndLine, false
}

// status is what a note means now (api.Note.Status). found is nil when it cannot be judged (a removed line, no
// anchor text).
func status(n api.Note, found *bool, turnEnded bool) string {
	switch {
	case n.State == api.NoteResolved:
		return api.NoteStatusResolved
	case n.SentInTurn == 0:
		if found != nil && !*found {
			return api.NoteStatusOutdated
		}
		return api.NoteStatusDraft
	case !turnEnded:
		return api.NoteStatusSent
	case found != nil && !*found:
		return api.NoteStatusAddressed
	}
	return api.NoteStatusUnaddressed
}

// view fills the computed fields of a note.
func view(n api.Note, d api.TaskDetail, files *fileLines) api.Note {
	var found *bool
	n.CurrentLine = n.Line // nothing to look for: it stays where it is
	if n.AnchorText != "" && n.Side != "old" {
		n.CurrentLine = 0
		line, _, ok := Reanchor(n, files.get(n.File))
		found = &ok
		if ok {
			n.CurrentLine = line
		}
	}
	ended := false
	for i := range d.Turns {
		if tu := &d.Turns[i]; tu.N == n.SentInTurn && tu.EndedAt != "" {
			ended = true
		}
	}
	n.Status = status(n, found, ended)
	return n
}

// List returns the notes of task id, oldest first, after moving them to where their lines are now.
func (s *Store) List(ctx context.Context, id string) ([]api.Note, error) {
	d, err := s.taskOf(ctx, id)
	if err != nil {
		return nil, err
	}
	list, err := s.refresh(ctx, d)
	if err != nil {
		return nil, err
	}
	files := &fileLines{root: s.root(ctx, d), cache: map[string][]string{}}
	out := make([]api.Note, 0, len(list))
	for i := range list {
		out = append(out, view(list[i], d, files))
	}
	return out, nil
}

// refresh moves the open and sent notes of an idle task to where their anchored text is after new turns; a draft
// whose text is gone becomes outdated. It returns the notes.
func (s *Store) refresh(ctx context.Context, d api.TaskDetail) ([]api.Note, error) {
	list, err := s.load(ctx, d.ID)
	if err != nil || busy(d.Status) {
		return list, err
	}
	turns := len(d.Turns)
	files := &fileLines{root: s.root(ctx, d), cache: map[string][]string{}}
	var changed []api.Note
	for i := range list {
		n := &list[i]
		if n.AnchorTurn >= turns {
			continue
		}
		n.AnchorTurn = turns
		if (n.State == api.NoteOpen || n.State == api.NoteSent) && n.Side != "old" {
			line, end, found := Reanchor(*n, files.get(n.File))
			n.Line, n.EndLine = line, end
			if n.SentInTurn == 0 && !found {
				n.State = api.NoteOutdated
			}
		}
		changed = append(changed, *n)
	}
	if len(changed) == 0 {
		return list, nil
	}
	// Each note is read again in the transaction and only its place changes: a delete, resolve or edit made since
	// the list was read stays.
	if err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		for i := range changed {
			c := &changed[i]
			cur, err := get(ctx, tx, d.ID, c.ID)
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if cur.AnchorTurn >= c.AnchorTurn {
				continue // moved already
			}
			cur.Line, cur.EndLine, cur.AnchorTurn = c.Line, c.EndLine, c.AnchorTurn
			if c.State == api.NoteOutdated && cur.State == api.NoteOpen && cur.SentInTurn == 0 {
				cur.State = api.NoteOutdated
			}
			if err := put(ctx, tx, cur); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return nil, err
	}
	s.o.Changed(d.ID)
	return s.load(ctx, d.ID)
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "n" + hex.EncodeToString(b[:])
}

func checkBody(body string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", invalidf("body is required")
	}
	if utf8.RuneCountInString(body) > maxBody {
		return "", invalidf("body is longer than %d characters", maxBody)
	}
	return body, nil
}

// Add adds a note to task id.
func (s *Store) Add(ctx context.Context, id string, in api.NoteInput) (api.Note, error) {
	d, err := s.taskOf(ctx, id)
	if err != nil {
		return api.Note{}, err
	}
	root := s.root(ctx, d)
	file, err := normFile(root, in.File)
	if err != nil {
		return api.Note{}, err
	}
	if in.Line < 1 {
		return api.Note{}, invalidf("line must be a positive integer")
	}
	if in.EndLine != 0 && in.EndLine < in.Line {
		return api.Note{}, invalidf("endLine must be at least line")
	}
	side := in.Side
	if side == "" {
		side = "new"
	}
	if side != "new" && side != "old" {
		return api.Note{}, invalidf("side must be new or old")
	}
	by := in.By
	if by == "" {
		by = "user"
	}
	if by != "user" && by != "lead" {
		return api.Note{}, invalidf("by must be user or lead")
	}
	body, err := checkBody(in.Body)
	if err != nil {
		return api.Note{}, err
	}
	anchor := in.AnchorText
	files := &fileLines{root: root, cache: map[string][]string{}}
	if anchor == "" && side == "new" {
		if lines := files.get(file); in.Line <= len(lines) {
			anchor = lines[in.Line-1]
		}
	}
	if i := strings.IndexAny(anchor, "\r\n"); i >= 0 {
		anchor = anchor[:i]
	}
	if r := []rune(anchor); len(r) > maxAnchor {
		anchor = string(r[:maxAnchor])
	}
	endLine := in.EndLine
	if endLine == in.Line {
		endLine = 0
	}
	n := api.Note{TaskID: d.ID, File: file, Line: in.Line, EndLine: endLine, Side: side,
		AnchorText: anchor, Body: body, By: by, State: api.NoteOpen, AnchorTurn: len(d.Turns),
		CreatedAt: s.o.Now().UTC().Format(timeFormat)}
	err = errIDTaken
	for try := 0; try < 5 && errors.Is(err, errIDTaken); try++ {
		n.ID = newID()
		err = s.o.DB.Write(ctx, func(tx *sql.Tx) error { return insert(ctx, tx, n) })
	}
	if err != nil {
		return api.Note{}, err
	}
	s.o.Changed(d.ID)
	return view(n, d, files), nil
}

// Update changes the body of a note, or resolves or reopens it.
func (s *Store) Update(ctx context.Context, id, noteID string, p api.NotePatch) (api.Note, error) {
	d, err := s.taskOf(ctx, id)
	if err != nil {
		return api.Note{}, err
	}
	if p.Body == nil && p.State == "" {
		return api.Note{}, invalidf("nothing to change: give body or state")
	}
	var n api.Note
	err = s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if n, err = get(ctx, tx, id, noteID); err != nil {
			return err
		}
		if p.Body != nil {
			if n.Body, err = checkBody(*p.Body); err != nil {
				return err
			}
		}
		switch p.State {
		case "":
		case api.NoteResolved:
			n.State, n.ResolvedAt = api.NoteResolved, s.o.Now().UTC().Format(timeFormat)
		case api.NoteOpen:
			n.ResolvedAt = ""
			n.State = api.NoteOpen
			if n.SentInTurn != 0 {
				n.State = api.NoteSent
			}
		default:
			return invalidf("state must be resolved or open")
		}
		return put(ctx, tx, n)
	})
	if err != nil {
		return api.Note{}, err
	}
	s.o.Changed(d.ID)
	return view(n, d, &fileLines{root: s.root(ctx, d), cache: map[string][]string{}}), nil
}

// Delete removes a note.
func (s *Store) Delete(ctx context.Context, id, noteID string) error {
	if _, err := s.taskOf(ctx, id); err != nil {
		return err
	}
	err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM review_notes WHERE id = ? AND task_id = ?`, noteID, id)
		if err != nil {
			return err
		}
		if k, err := res.RowsAffected(); err == nil && k == 0 {
			return fmt.Errorf("%w: no note %s on task %s", ErrNotFound, noteID, id)
		}
		return err
	})
	if err != nil {
		return err
	}
	s.o.Changed(id)
	return nil
}

// Send sends the draft notes and the sent notes left unaddressed to the worker as one followup, with req.Extra
// after them. Addressed, outdated and resolved notes are not sent.
func (s *Store) Send(ctx context.Context, id string, req api.NotesSendRequest) (api.NotesSendResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.taskOf(ctx, id)
	if err != nil {
		return api.NotesSendResult{}, err
	}
	if busy(d.Status) || d.Status == api.StatusBlockedDeps || d.Manual {
		return api.NotesSendResult{}, fmt.Errorf("%w: task %s is %s; send the notes when its turn has ended",
			ErrBadState, id, d.Status)
	}
	by := req.By
	if by == "" {
		by = "user"
	}
	if by != "user" && by != "lead" {
		return api.NotesSendResult{}, invalidf("by must be user or lead")
	}
	extra := strings.TrimSpace(req.Extra)
	if utf8.RuneCountInString(extra) > maxExtra {
		return api.NotesSendResult{}, invalidf("extra is longer than %d characters", maxExtra)
	}
	list, err := s.refresh(ctx, d)
	if err != nil {
		return api.NotesSendResult{}, err
	}
	files := &fileLines{root: s.root(ctx, d), cache: map[string][]string{}}
	var send []api.Note
	for i := range list {
		if st := view(list[i], d, files).Status; st == api.NoteStatusDraft || st == api.NoteStatusUnaddressed {
			send = append(send, list[i])
		}
	}
	if len(send) == 0 && extra == "" {
		return api.NotesSendResult{}, invalidf("nothing to send: no draft notes and no sent notes left unaddressed")
	}
	msg := Message(send, extra)
	var sum api.TaskSummary
	if turn := sentBefore(d, list, send, msg); turn > 0 {
		// A Send whose followup went out but whose notes were never marked (a lost save): mark them, send nothing.
		sum, sum.TurnCount = d.TaskSummary, turn
	} else if sum, err = s.o.Tasks.Followup(ctx, id, api.FollowupRequest{Message: msg, By: by}); err != nil {
		return api.NotesSendResult{}, err
	}
	ctx = context.WithoutCancel(ctx) // the followup went out: its notes must be marked
	now := s.o.Now().UTC().Format(timeFormat)
	sent := make([]string, 0, len(send))
	err = s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		sent = sent[:0]
		for i := range send {
			n, err := get(ctx, tx, id, send[i].ID) // as it is now: a body edit or a resolve made meanwhile stays
			if errors.Is(err, ErrNotFound) {
				continue
			}
			if err != nil {
				return err
			}
			if n.State != api.NoteResolved {
				n.State = api.NoteSent
			}
			n.SentInTurn, n.SentAt, n.AnchorTurn = sum.TurnCount, now, sum.TurnCount-1
			if err := put(ctx, tx, n); err != nil {
				return err
			}
			sent = append(sent, n.ID)
		}
		return nil
	})
	if err != nil {
		return api.NotesSendResult{}, err
	}
	s.o.Changed(id)
	return api.NotesSendResult{Sent: sent, Task: sum}, nil
}

// sentBefore returns the task's last turn when it carries exactly msg for notes, no note claims that turn yet and
// notes are in it: an earlier Send of the same notes went out but was never recorded. Otherwise 0.
func sentBefore(d api.TaskDetail, all, send []api.Note, msg string) int {
	n := len(d.Turns)
	if n == 0 || len(send) == 0 || d.Turns[n-1].Message != msg {
		return 0
	}
	for i := range all {
		if all[i].SentInTurn == n {
			return 0 // that turn carried notes on record: this is a new send of the same words
		}
	}
	return n
}

// Message is the followup for notes: grouped by author, numbered, each with its place and the anchored line.
// Bodies are the user's and the lead's own words, kept as written.
func Message(notes []api.Note, extra string) string {
	var parts []string
	for _, who := range []string{"user", "lead"} {
		var b strings.Builder
		k := 0
		for i := range notes {
			n := &notes[i]
			author := "user"
			if n.By == "lead" {
				author = "lead"
			}
			if author != who {
				continue
			}
			if k == 0 {
				b.WriteString("## Review notes from the " + who + "\n\nEach note points at a line of your diff. " +
					"Address every note; if you disagree with one, say why in your report.\n")
			}
			k++
			where := n.File + ":" + strconv.Itoa(n.Line)
			if n.EndLine != 0 && n.EndLine != n.Line {
				where += "-" + strconv.Itoa(n.EndLine)
			}
			if n.Side == "old" {
				where += " (removed line)"
			}
			if n.SentInTurn != 0 {
				where += " (sent before, not addressed yet)"
			}
			body := strings.ReplaceAll(strings.ReplaceAll(n.Body, "\r\n", "\n"), "\n", "\n   ")
			fmt.Fprintf(&b, "\n%d. %s — %s", k, where, body)
			if n.AnchorText != "" {
				b.WriteString("\n   > " + n.AnchorText)
			}
		}
		if k > 0 {
			parts = append(parts, b.String())
		}
	}
	if extra = strings.TrimSpace(extra); extra != "" {
		parts = append(parts, extra)
	}
	return strings.Join(parts, "\n\n")
}

func (s *Store) load(ctx context.Context, id string) ([]api.Note, error) {
	var out []api.Note
	err := s.o.DB.Read(ctx, func(tx *sql.Tx) error {
		out = nil
		rows, err := tx.QueryContext(ctx, `SELECT data FROM review_notes WHERE task_id = ? ORDER BY created_at, id`, id)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var data string
			if err := rows.Scan(&data); err != nil {
				return err
			}
			n, err := decode(data)
			if err != nil {
				return err
			}
			out = append(out, n)
		}
		return rows.Err()
	})
	return out, err
}

func get(ctx context.Context, tx *sql.Tx, taskID, id string) (api.Note, error) {
	var data string
	err := tx.QueryRowContext(ctx, `SELECT data FROM review_notes WHERE id = ? AND task_id = ?`, id, taskID).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return api.Note{}, fmt.Errorf("%w: no note %s on task %s", ErrNotFound, id, taskID)
	}
	if err != nil {
		return api.Note{}, err
	}
	return decode(data)
}

func decode(data string) (api.Note, error) {
	var n api.Note
	if err := json.Unmarshal([]byte(data), &n); err != nil {
		return api.Note{}, fmt.Errorf("note record: %w", err)
	}
	return n, nil
}

func encode(n api.Note) (string, error) {
	n.Status, n.CurrentLine = "", 0
	data, err := json.Marshal(n)
	return string(data), err
}

// put stores a note that exists (of its task); a note deleted meanwhile stays deleted.
func put(ctx context.Context, tx *sql.Tx, n api.Note) error {
	data, err := encode(n)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE review_notes SET data = ? WHERE id = ? AND task_id = ?`, data, n.ID, n.TaskID)
	return err
}

// insert stores a new note; an id that is taken already is ErrIDTaken (never another note overwritten).
func insert(ctx context.Context, tx *sql.Tx, n api.Note) error {
	data, err := encode(n)
	if err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO review_notes (id, task_id, created_at, data) VALUES (?, ?, ?, ?)
		ON CONFLICT (id) DO NOTHING`, n.ID, n.TaskID, n.CreatedAt, data)
	if err != nil {
		return err
	}
	if k, err := res.RowsAffected(); err == nil && k == 0 {
		return errIDTaken
	}
	return err
}

var errIDTaken = errors.New("note id taken")
