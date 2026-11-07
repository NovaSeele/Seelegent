package notes

import (
	"context"
	"errors"
	"os"
	"path/filepath"
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
}

func (f *fakeTasks) Get(_ context.Context, id string) (api.TaskDetail, error) {
	if id != f.d.ID {
		return api.TaskDetail{}, task.ErrNotFound
	}
	return f.d, nil
}

func (f *fakeTasks) Followup(_ context.Context, _ string, req api.FollowupRequest) (api.TaskSummary, error) {
	f.sent = append(f.sent, req)
	f.d.Turns = append(f.d.Turns, api.Turn{N: len(f.d.Turns) + 1})
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
