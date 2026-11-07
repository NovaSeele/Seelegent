package notes

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/api"
	"github.com/NovaSeele/Seelegent/internal/store"
	"github.com/NovaSeele/Seelegent/internal/task"
)

type fakeTasks struct {
	d    api.TaskDetail
	sent []api.FollowupRequest
	// onFollowup runs inside Followup (something that happens while a send is under way).
	onFollowup func()
}

func (f *fakeTasks) Get(_ context.Context, id string) (api.TaskDetail, error) {
	if id != f.d.ID {
		return api.TaskDetail{}, task.ErrNotFound
	}
	return f.d, nil
}

func (f *fakeTasks) Followup(_ context.Context, _ string, req api.FollowupRequest) (api.TaskSummary, error) {
	f.sent = append(f.sent, req)
	if f.onFollowup != nil {
		f.onFollowup()
	}
	f.d.Turns = append(f.d.Turns, api.Turn{N: len(f.d.Turns) + 1, Message: req.Message})
	f.d.Status = api.StatusRunning
	f.d.TurnCount = len(f.d.Turns)
	return f.d.TaskSummary, nil
}

// endTurn ends the running turn.
func (f *fakeTasks) endTurn() {
	f.d.Turns[len(f.d.Turns)-1].EndedAt = "2026-11-07T10:00:00Z"
	f.d.Status = api.StatusDone
}

func setup(t *testing.T) (*Store, *fakeTasks, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(context.Background(), filepath.Join(dir, "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(filepath.Join(repo, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "src", "a.go"), "package a\n\nfunc A() int {\n\treturn 1\n}\n")
	ft := &fakeTasks{d: api.TaskDetail{TaskSummary: api.TaskSummary{ID: "t1", Status: api.StatusDone,
		Kind: "normal", Cwd: repo}, RepoRoot: repo, Turns: []api.Turn{{N: 1, EndedAt: "2026-11-07T09:00:00Z"}}}}
	ft.d.TurnCount = 1
	return New(Options{DB: db, Tasks: ft, Now: func() time.Time { return time.Date(2026, 11, 7, 9, 0, 0, 0, time.UTC) }}),
		ft, repo
}

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestNotesLifecycle(t *testing.T) {
	s, ft, repo := setup(t)
	ctx := t.Context()
	if _, err := s.Add(ctx, "t1", api.NoteInput{File: "../x.go", Line: 1, Body: "x"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("outside = %v", err)
	}
	if _, err := s.Add(ctx, "nope", api.NoteInput{File: "a", Line: 1, Body: "x"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown task = %v", err)
	}
	n1, err := s.Add(ctx, "t1", api.NoteInput{File: filepath.Join(repo, "src", "a.go"), Line: 4, Body: "return 2"})
	if err != nil {
		t.Fatal(err)
	}
	if n1.File != "src/a.go" || n1.AnchorText != "\treturn 1" || n1.Status != api.NoteStatusDraft || n1.By != "user" {
		t.Fatalf("note = %+v", n1)
	}
	n2, err := s.Add(ctx, "t1", api.NoteInput{File: "src/a.go", Line: 3, Body: "rename A", By: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := s.Send(ctx, "t1", api.NotesSendRequest{Extra: "Also run the tests."})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sent) != 2 || len(ft.sent) != 1 {
		t.Fatalf("sent = %+v", res)
	}
	msg := ft.sent[0].Message
	for _, want := range []string{"## Review notes from the user", "1. src/a.go:4 — return 2", "> \treturn 1",
		"## Review notes from the lead", "1. src/a.go:3 — rename A", "Also run the tests."} {
		if !strings.Contains(msg, want) {
			t.Errorf("message lacks %q:\n%s", want, msg)
		}
	}
	if _, err := s.Send(ctx, "t1", api.NotesSendRequest{}); !errors.Is(err, ErrBadState) {
		t.Fatalf("send while running = %v", err)
	}
	list, err := s.List(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Status != api.NoteStatusSent || list[0].SentInTurn != 2 {
		t.Fatalf("while running: %+v", list[0])
	}
	// The worker moves line 4 down by two lines and fixes the lead's line.
	write(t, filepath.Join(repo, "src", "a.go"), "package a\n\n// B is A.\n\nfunc B() int {\n\treturn 1\n}\n")
	ft.endTurn()
	list, err = s.List(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]api.Note{}
	for _, n := range list {
		byID[n.ID] = n
	}
	if n := byID[n1.ID]; n.Status != api.NoteStatusUnaddressed || n.Line != 6 || n.CurrentLine != 6 {
		t.Errorf("unchanged line: %+v", n)
	}
	if n := byID[n2.ID]; n.Status != api.NoteStatusAddressed {
		t.Errorf("changed line: %+v", n)
	}
	// Only the unaddressed note goes out again.
	res, err = s.Send(ctx, "t1", api.NotesSendRequest{By: "lead"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Sent) != 1 || res.Sent[0] != n1.ID || !strings.Contains(ft.sent[1].Message, "(sent before") {
		t.Fatalf("second send = %+v %q", res, ft.sent[1].Message)
	}
	ft.endTurn()
	n, err := s.Update(ctx, "t1", n1.ID, api.NotePatch{State: api.NoteResolved})
	if err != nil || n.Status != api.NoteStatusResolved || n.ResolvedAt == "" {
		t.Fatalf("resolve = %+v %v", n, err)
	}
	if _, err := s.Send(ctx, "t1", api.NotesSendRequest{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nothing to send = %v", err)
	}
	if err := s.Delete(ctx, "t1", n2.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(ctx, "t1", n2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v", err)
	}
}

// A send whose notes were never marked (a lost save) is not sent twice; a resolve made during a send stays; an
// update never brings a deleted note back; a taken id never overwrites another note.
func TestSendRetryAndStaleWrites(t *testing.T) {
	s, ft, _ := setup(t)
	ctx := t.Context()
	n1, err := s.Add(ctx, "t1", api.NoteInput{File: "src/a.go", Line: 4, Body: "return 2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Send(ctx, "t1", api.NotesSendRequest{}); err != nil {
		t.Fatal(err)
	}
	// Undo the marking, as if the save after the followup had been lost.
	if err := s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		n, err := get(ctx, tx, "t1", n1.ID)
		if err != nil {
			return err
		}
		n.State, n.SentInTurn, n.SentAt, n.AnchorTurn = api.NoteOpen, 0, "", 1
		return put(ctx, tx, n)
	}); err != nil {
		t.Fatal(err)
	}
	ft.endTurn()
	res, err := s.Send(ctx, "t1", api.NotesSendRequest{})
	if err != nil || len(ft.sent) != 1 || len(res.Sent) != 1 || res.Task.TurnCount != 2 {
		t.Fatalf("retried send = %+v %v (followups %d)", res, err, len(ft.sent))
	}

	// A note resolved while the followup goes out stays resolved.
	n2, err := s.Add(ctx, "t1", api.NoteInput{File: "src/a.go", Line: 3, Body: "rename A"})
	if err != nil {
		t.Fatal(err)
	}
	ft.onFollowup = func() {
		if _, err := s.Update(ctx, "t1", n2.ID, api.NotePatch{State: api.NoteResolved}); err != nil {
			t.Error(err)
		}
	}
	if _, err := s.Send(ctx, "t1", api.NotesSendRequest{}); err != nil {
		t.Fatal(err)
	}
	ft.onFollowup = nil
	list, err := s.List(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range list {
		if n.ID == n2.ID && n.State != api.NoteResolved {
			t.Errorf("resolved during the send: %+v", n)
		}
	}

	// A stale copy of a deleted note is not written back; a taken id is refused.
	if err := s.Delete(ctx, "t1", n2.ID); err != nil {
		t.Fatal(err)
	}
	err = s.o.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := put(ctx, tx, n2); err != nil {
			return err
		}
		other := n1
		other.TaskID, other.Body = "t2", "someone else's"
		return insert(ctx, tx, other)
	})
	if !errors.Is(err, errIDTaken) {
		t.Fatalf("insert over a taken id = %v", err)
	}
	if list, _ := s.List(ctx, "t1"); len(list) != 1 || list[0].Body != "return 2" {
		t.Errorf("notes after stale writes = %+v", list)
	}
}

func TestFileInsideRepo(t *testing.T) {
	s, _, repo := setup(t)
	ctx := t.Context()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(repo, "out")); err == nil {
		write(t, filepath.Join(outside, "x.go"), "secret\n")
		if _, err := s.Add(ctx, "t1", api.NoteInput{File: "out/x.go", Line: 1, Body: "x"}); !errors.Is(err, ErrInvalid) {
			t.Errorf("a note through a link out of the repository = %v", err)
		}
	}
	if runtime.GOOS == "windows" {
		n, err := s.Add(ctx, "t1", api.NoteInput{File: strings.ToUpper(repo) + `\SRC\a.go`, Line: 1, Body: "x"})
		if err != nil || !strings.EqualFold(n.File, "src/a.go") {
			t.Errorf("another spelling of the repository = %+v %v", n, err)
		}
	}
}

func TestDraftOutdated(t *testing.T) {
	s, ft, repo := setup(t)
	ctx := t.Context()
	n, err := s.Add(ctx, "t1", api.NoteInput{File: "src/a.go", Line: 4, Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	write(t, filepath.Join(repo, "src", "a.go"), "package a\n")
	ft.d.Turns = append(ft.d.Turns, api.Turn{N: 2, EndedAt: "2026-11-07T11:00:00Z"})
	list, err := s.List(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if list[0].ID != n.ID || list[0].State != api.NoteOutdated || list[0].Status != api.NoteStatusOutdated {
		t.Errorf("draft = %+v", list[0])
	}
}

// A line longer than the anchor keeps (maxAnchor) is still the note's line: the anchor is its clipped start.
func TestLongLineAnchor(t *testing.T) {
	s, _, repo := setup(t)
	ctx := t.Context()
	long := "var rules = []string{" + strings.Repeat(`"rule-with-a-long-name", `, 40) + "}"
	write(t, filepath.Join(repo, "src", "a.go"), "package a\n\n"+long+"\n")
	n, err := s.Add(ctx, "t1", api.NoteInput{File: "src/a.go", Line: 3, Body: "split this list"})
	if err != nil {
		t.Fatal(err)
	}
	if got := []rune(n.AnchorText); len(got) != maxAnchor {
		t.Fatalf("anchor is %d characters", len(got))
	}
	list, err := s.List(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if list[0].Status != api.NoteStatusDraft || list[0].CurrentLine != 3 {
		t.Errorf("note on a long line = %s at %d", list[0].Status, list[0].CurrentLine)
	}
	if l, _, ok := Reanchor(n, []string{"package a", "", "", long}); !ok || l != 4 {
		t.Errorf("moved to %d %v", l, ok)
	}
}

func TestReanchorWindow(t *testing.T) {
	lines := make([]string, 120)
	lines[99] = "target"
	if _, _, ok := Reanchor(api.Note{Line: 49, AnchorText: "target"}, lines); ok {
		t.Error("found beyond the window")
	}
	if l, e, ok := Reanchor(api.Note{Line: 60, EndLine: 62, AnchorText: "target"}, lines); !ok || l != 100 || e != 102 {
		t.Errorf("moved to %d-%d %v", l, e, ok)
	}
}
