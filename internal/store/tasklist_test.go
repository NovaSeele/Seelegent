package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Migration 0011 fills kind from the task records written before it, and leaves the digest to the engine
// (turn_count -1).
func TestTaskListMigrationBackfillsKind(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seelegent.db")
	ctx := t.Context()
	all, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var upTo []migration
	for _, m := range all {
		if m.version <= 10 {
			upTo = append(upTo, m)
		}
	}
	old, err := open(ctx, path, upTo)
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]string{
		"t1": `{"kind":"verify","title":"v"}`,
		"t2": `{"kind":"normal"}`,
		"t3": `{"title":"from before kinds"}`,
	}
	if err := old.Write(ctx, func(tx *sql.Tx) error {
		for id, data := range rows {
			if _, err := tx.ExecContext(ctx, `INSERT INTO tasks (id, title, status, project_key, cwd, backend, data,
				created_at, updated_at) VALUES (?, '', 'done', 'p', 'c', 'agy', ?, '2026', '2026')`, id, data); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	want := map[string]string{"t1": "verify", "t2": "normal", "t3": ""}
	if err := s.Read(ctx, func(tx *sql.Tx) error {
		for id, kind := range want {
			var got string
			var turns int
			if err := tx.QueryRowContext(ctx, `SELECT kind, turn_count FROM tasks WHERE id = ?`, id).
				Scan(&got, &turns); err != nil {
				return err
			}
			if got != kind || turns != -1 {
				t.Errorf("%s: kind %q turn_count %d, want %q and -1", id, got, turns, kind)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
