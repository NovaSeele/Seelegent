package memory

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NovaSeele/Seelegent/internal/store"
)

func newStore(t *testing.T) (s *Store, st *store.Store) {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "seelegent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	at := time.Date(2026, 11, 7, 9, 30, 0, 0, time.UTC)
	return New(st, func() time.Time { at = at.Add(time.Minute); return at }), st
}

func TestNotes(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	dir := t.TempDir()
	n, err := s.Get(ctx, dir)
	if err != nil || n.Text != "" || !n.UpdatedAt.IsZero() || n.Key == "" {
		t.Fatalf("Get = %+v, %v", n, err)
	}
	if n, err = s.Append(ctx, dir, "  Use pnpm.  "); err != nil || n.Text != "Use pnpm.\n" {
		t.Fatalf("Append = %+v, %v", n, err)
	}
	if n, err = s.Append(ctx, dir+string(filepath.Separator), "Tests: go test ./..."); err != nil ||
		n.Text != "Use pnpm.\nTests: go test ./...\n" {
		t.Fatalf("Append = %+v, %v", n, err)
	}
	if n, err = s.Append(ctx, dir, "   "); err != nil || n.Text != "Use pnpm.\nTests: go test ./...\n" {
		t.Fatalf("empty Append = %+v, %v", n, err)
	}
	got, err := s.Get(ctx, dir)
	if err != nil || got.Text != n.Text || got.UpdatedAt.IsZero() {
		t.Fatalf("Get = %+v, %v", got, err)
	}
	if n, err = s.Replace(ctx, dir, "Only this"); err != nil || n.Text != "Only this\n" {
		t.Fatalf("Replace = %+v, %v", n, err)
	}
	if n, err = s.Replace(ctx, dir, " \n"); err != nil || n.Text != "" {
		t.Fatalf("clearing Replace = %+v, %v", n, err)
	}
	// Another folder has its own notes.
	other := t.TempDir()
	if n, _ := s.Get(ctx, other); n.Text != "" {
		t.Fatalf("other folder sees %q", n.Text)
	}
}

func TestCapAndValidation(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	dir := t.TempDir()
	if _, err := s.Replace(ctx, dir, strings.Repeat("x", MaxBytes)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("over cap = %v", err)
	}
	if _, err := s.Replace(ctx, dir, strings.Repeat("x", MaxBytes-1)); err != nil {
		t.Fatalf("at cap = %v", err)
	}
	if _, err := s.Append(ctx, dir, "y"); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("append over cap = %v", err)
	}
	for _, bad := range []string{"", "relative/dir", filepath.Join(dir, "missing")} {
		if _, err := s.Get(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("Get(%q) = %v", bad, err)
		}
	}
	file := filepath.Join(dir, "f.txt")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Append(ctx, file, "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("Append(file) = %v", err)
	}
	if err := s.AddVerdict(ctx, Verdict{Cwd: "rel"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("AddVerdict(rel) = %v", err)
	}
	if _, err := s.History(ctx, "rel", 8); !errors.Is(err, ErrInvalid) {
		t.Errorf("History(rel) = %v", err)
	}
}

func TestHistory(t *testing.T) {
	ctx := context.Background()
	s, _ := newStore(t)
	dir := t.TempDir()
	for i := range 10 {
		notes := ""
		if i == 9 {
			notes = "  good\n work " + strings.Repeat("n", 300)
		}
		if err := s.AddVerdict(ctx, Verdict{Cwd: dir, TaskID: fmt.Sprintf("t%d", i), Title: "Fix \"it\"",
			Verdict: "accepted", Notes: notes}); err != nil {
			t.Fatal(err)
		}
	}
	lines, err := s.History(ctx, dir, HistoryShown)
	if err != nil || len(lines) != HistoryShown {
		t.Fatalf("History = %v, %v", lines, err)
	}
	if lines[0] != `- 2026-11-07T09:33 t2 "Fix \"it\"": accepted` {
		t.Errorf("first line %q", lines[0])
	}
	last := lines[len(lines)-1]
	if !strings.HasPrefix(last, `- 2026-11-07T09:40 t9 "Fix \"it\"": accepted - good work nnn`) ||
		len(last) > 300 {
		t.Errorf("last line %q", last)
	}
	if other, err := s.History(ctx, t.TempDir(), 8); err != nil || len(other) != 0 {
		t.Errorf("other project history = %v, %v", other, err)
	}
}

// Unreadable stored data is an error, never an empty memory.
func TestUnreadableIsError(t *testing.T) {
	ctx := context.Background()
	s, st := newStore(t)
	dir := t.TempDir()
	if _, err := s.Append(ctx, dir, "x"); err != nil {
		t.Fatal(err)
	}
	if err := s.AddVerdict(ctx, Verdict{Cwd: dir, TaskID: "t1", Title: "x", Verdict: "accepted"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE project_memory SET updated_at = 'bad'`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE review_history SET at = 'bad'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get(ctx, dir); err == nil {
		t.Error("unreadable memory accepted")
	}
	if _, err := s.Append(ctx, dir, "y"); err == nil {
		t.Error("append over unreadable memory accepted")
	}
	if _, err := s.History(ctx, dir, 8); err == nil {
		t.Error("unreadable history accepted")
	}
}

func TestNoteAPI(t *testing.T) {
	s, _ := newStore(t)
	cwd := t.TempDir()
	empty, err := s.Get(context.Background(), cwd)
	if err != nil {
		t.Fatal(err)
	}
	if m := empty.API(); m.Cwd == "" || m.Text != "" || m.UpdatedAt != "" {
		t.Fatalf("empty = %+v", m)
	}
	n, err := s.Replace(context.Background(), cwd, "Use tabs.")
	if err != nil {
		t.Fatal(err)
	}
	if m := n.API(); strings.TrimSpace(m.Text) != "Use tabs." || m.UpdatedAt != "2026-11-07T09:31:00.000Z" {
		t.Fatalf("note = %+v", m)
	}
}
