package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Migration 0012 moves allow_git from the task to the task's latest turn (the command in flight) and drops it from
// the task; tasks without it, and their turns, are left alone.
func TestTurnAllowGitMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seelegent.db")
	ctx := t.Context()
	all, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var upTo []migration
	for _, m := range all {
		if m.version <= 11 {
			upTo = append(upTo, m)
		}
	}
	old, err := open(ctx, path, upTo)
	if err != nil {
		t.Fatal(err)
	}
	tasks := map[string]string{"t1": `{"kind":"normal","allowGit":true}`, "t2": `{"kind":"normal"}`}
	turns := []struct {
		id string
		n  int
	}{{"t1", 1}, {"t1", 2}, {"t1", 3}, {"t2", 1}, {"t2", 2}}
	if err := old.Write(ctx, func(tx *sql.Tx) error {
		for id, data := range tasks {
			if _, err := tx.ExecContext(ctx, `INSERT INTO tasks (id, title, status, project_key, cwd, backend, data,
				created_at, updated_at) VALUES (?, '', 'running', 'p', 'c', 'agy', ?, '2026', '2026')`, id, data); err != nil {
				return err
			}
		}
		for _, tu := range turns {
			if _, err := tx.ExecContext(ctx, `INSERT INTO turns (task_id, n, status, data) VALUES (?, ?, 'done', ?)`,
				tu.id, tu.n, `{"n":1,"by":"lead"}`); err != nil {
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
	if err := s.Read(ctx, func(tx *sql.Tx) error {
		var onTask int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM tasks WHERE json_extract(data, '$.allowGit') IS NOT NULL`).
			Scan(&onTask); err != nil {
			return err
		}
		if onTask != 0 {
			t.Errorf("%d tasks still carry allowGit", onTask)
		}
		for _, tu := range turns {
			var allow sql.NullInt64
			if err := tx.QueryRowContext(ctx, `SELECT json_extract(data, '$.allowGit') FROM turns
				WHERE task_id = ? AND n = ?`, tu.id, tu.n).Scan(&allow); err != nil {
				return err
			}
			want := tu.id == "t1" && tu.n == 3
			if got := allow.Valid && allow.Int64 == 1; got != want {
				t.Errorf("turn %s/%d allowGit = %v, want %v", tu.id, tu.n, got, want)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
