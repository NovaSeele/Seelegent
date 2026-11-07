package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestInspect(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "seelegent.db")
	if _, err := Inspect(ctx, path); !IsNotExist(err) {
		t.Fatalf("missing database = %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Inspect created the database: %v", err)
	}
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	err = s.Write(ctx, func(tx *sql.Tx) error {
		for i, enabled := range []int{1, 0, 1} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO accounts (id, backend, kind, label, enabled, created_at,
				updated_at) VALUES (?, 'agy', 'isolated', 'x', ?, 'now', 'now')`, string(rune('a'+i)), enabled); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := Inspect(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != got.Known || got.Known == 0 || got.Accounts != 3 || got.EnabledAccounts != 2 {
		t.Fatalf("inspect = %+v", got)
	}
	// A newer schema is reported, never migrated or read further.
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO schema_version (version, name, applied_at) VALUES (999, 'future', 'now')")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	got, err = Inspect(ctx, path)
	if !errors.Is(err, ErrSchemaTooNew) || got.Version != 999 || got.Accounts != 0 {
		t.Fatalf("newer schema = %+v, %v", got, err)
	}
}
