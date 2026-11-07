package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

func openTemp(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "seelegent.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return s
}

func pragma(ctx context.Context, t *testing.T, tx *sql.Tx, name string) string {
	t.Helper()
	var v string
	if err := tx.QueryRowContext(ctx, "PRAGMA "+name).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return v
}

func TestOpenSettings(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	want := map[string]string{"journal_mode": "wal", "foreign_keys": "1", "busy_timeout": "5000"}
	check := func(kind string, tx *sql.Tx) error {
		for name, w := range want {
			if got := pragma(ctx, t, tx, name); got != w {
				t.Errorf("%s connection: %s = %s, want %s", kind, name, got, w)
			}
		}
		return nil
	}
	if err := s.Read(ctx, func(tx *sql.Tx) error { return check("read", tx) }); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		if got := pragma(ctx, t, tx, "synchronous"); got != "1" { // NORMAL
			t.Errorf("synchronous = %s, want 1 (NORMAL)", got)
		}
		return check("write", tx)
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(s.Path(), "seelegent.db") {
		t.Errorf("Path = %q", s.Path())
	}
}

func TestMigrationsAppliedOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seelegent.db")
	ctx := t.Context()
	all, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	for i := range 2 {
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatalf("Open #%d: %v", i+1, err)
		}
		v, err := s.SchemaVersion(ctx)
		if err != nil || v != len(all) {
			t.Fatalf("SchemaVersion #%d = %d, %v; want %d", i+1, v, err, len(all))
		}
		var rows int
		if err := s.Read(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_version").Scan(&rows)
		}); err != nil || rows != len(all) {
			t.Fatalf("schema_version rows = %d, %v", rows, err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestMeta(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	if _, ok, err := s.GetMeta(ctx, "created"); ok || err != nil {
		t.Fatalf("GetMeta on empty table = %v, %v", ok, err)
	}
	for _, v := range []string{"one", "two"} {
		if err := s.SetMeta(ctx, "created", v); err != nil {
			t.Fatal(err)
		}
		got, ok, err := s.GetMeta(ctx, "created")
		if err != nil || !ok || got != v {
			t.Fatalf("GetMeta = %q, %v, %v; want %q", got, ok, err, v)
		}
	}
}

func TestReadIsReadOnly(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	err := s.Read(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO meta (key, value) VALUES ('k', 'v')")
		return err
	})
	if err == nil {
		t.Fatal("a write inside Read must fail")
	}
}

func TestWriteRollsBack(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	errStop := errors.New("stop")
	insert := func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO meta (key, value) VALUES ('k', 'v')")
		return err
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		if err := insert(tx); err != nil {
			return err
		}
		return errStop
	}); !errors.Is(err, errStop) {
		t.Fatalf("err = %v, want errStop", err)
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Fatal("the panic must propagate")
			}
		}()
		_ = s.Write(ctx, func(tx *sql.Tx) error {
			if err := insert(tx); err != nil {
				return err
			}
			panic("boom")
		})
	}()
	if _, ok, err := s.GetMeta(ctx, "k"); ok || err != nil {
		t.Fatalf("rolled-back row is visible: %v, %v", ok, err)
	}
	// The single writer connection is usable again after the panic.
	if err := s.SetMeta(ctx, "after", "x"); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentWritesAndReads(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	if err := s.SetMeta(ctx, "counter", "0"); err != nil {
		t.Fatal(err)
	}
	const writers = 20
	var wg sync.WaitGroup
	for range writers {
		wg.Go(func() {
			err := s.Write(ctx, func(tx *sql.Tx) error {
				var n int
				if err := tx.QueryRowContext(ctx, "SELECT CAST(value AS INTEGER) FROM meta WHERE key = 'counter'").Scan(&n); err != nil {
					return err
				}
				_, err := tx.ExecContext(ctx, "UPDATE meta SET value = ? WHERE key = 'counter'", fmt.Sprint(n+1))
				return err
			})
			if err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if _, _, err := s.GetMeta(ctx, "counter"); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	got, _, err := s.GetMeta(ctx, "counter")
	if err != nil || got != fmt.Sprint(writers) {
		t.Fatalf("counter = %q, %v; want %d (lost updates)", got, err, writers)
	}
}

// Readers see the last committed state while the writer holds an open transaction (WAL).
func TestReadDoesNotWaitForWriter(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	if err := s.SetMeta(ctx, "k", "old"); err != nil {
		t.Fatal(err)
	}
	inside := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error)
	go func() {
		done <- s.Write(ctx, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "UPDATE meta SET value = 'new' WHERE key = 'k'"); err != nil {
				return err
			}
			close(inside)
			<-release
			return nil
		})
	}()
	<-inside
	readCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	got, _, err := s.GetMeta(readCtx, "k")
	close(release)
	// Wait for the writer before any Fatal, so it never outlives the test's temp dir.
	werr := <-done
	if err != nil || got != "old" {
		t.Fatalf("read during write = %q, %v; want old", got, err)
	}
	if werr != nil {
		t.Fatal(werr)
	}
}

// A second process (simulated by a separate connection pool) holding the write lock makes our write wait, not fail.
func TestWriteWaitsForOtherProcess(t *testing.T) {
	s := openTemp(t)
	ctx := t.Context()
	other, err := sql.Open("sqlite", dsn(s.Path(), false))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Close() }()
	tx, err := other.BeginTx(ctx, nil) // BEGIN IMMEDIATE: holds the write lock
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, "INSERT INTO meta (key, value) VALUES ('other', '1')"); err != nil {
		t.Fatal(err)
	}
	go func() {
		time.Sleep(200 * time.Millisecond)
		_ = tx.Commit()
	}()
	start := time.Now()
	if err := s.SetMeta(ctx, "mine", "1"); err != nil {
		t.Fatalf("SetMeta while another connection writes: %v", err)
	}
	if time.Since(start) < 100*time.Millisecond {
		t.Error("the write did not wait for the other transaction")
	}
	if _, ok, _ := s.GetMeta(ctx, "other"); !ok {
		t.Error("the other process's row is missing")
	}
}

func TestSchemaTooNew(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seelegent.db")
	ctx := t.Context()
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "INSERT INTO schema_version (version, name, applied_at) VALUES (99, 'future', 'x')")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(ctx, path); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("err = %v, want ErrSchemaTooNew", err)
	}
}

func TestFailedMigrationRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seelegent.db")
	ctx := t.Context()
	good := migration{version: 1, name: "a", sql: "CREATE TABLE a (x INTEGER); CREATE TABLE b (y INTEGER);"}
	bad := migration{version: 2, name: "bad", sql: "CREATE TABLE c (z INTEGER); SELEC broken;"}
	if _, err := open(ctx, path, []migration{good, bad}); err == nil || !strings.Contains(err.Error(), "0002_bad") {
		t.Fatalf("err = %v, want a failure naming 0002_bad", err)
	}
	s, err := open(ctx, path, []migration{good})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	if v, err := s.SchemaVersion(ctx); err != nil || v != 1 {
		t.Fatalf("SchemaVersion = %d, %v; want 1", v, err)
	}
	var tables string
	if err := s.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx,
			"SELECT group_concat(name, ',') FROM (SELECT name FROM sqlite_schema WHERE type = 'table' AND name IN ('a','b','c') ORDER BY name)").Scan(&tables)
	}); err != nil {
		t.Fatal(err)
	}
	if tables != "a,b" {
		t.Fatalf("tables = %q, want a,b (both statements of 0001 applied, nothing of 0002)", tables)
	}
}

func TestOpenErrors(t *testing.T) {
	dir := t.TempDir()
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	tests := []struct {
		name string
		ctx  context.Context
		path string
	}{
		{"empty path", t.Context(), ""},
		{"question mark", t.Context(), filepath.Join(dir, "a?b.db")},
		{"missing directory", t.Context(), filepath.Join(dir, "missing", "x.db")},
		{"directory", t.Context(), dir},
		{"canceled", canceled, filepath.Join(dir, "c.db")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Open(tt.ctx, tt.path)
			if err == nil {
				_ = s.Close()
				t.Fatal("want an error")
			}
		})
	}
}

func TestLoadMigrations(t *testing.T) {
	file := func(body string) *fstest.MapFile { return &fstest.MapFile{Data: []byte(body)} }
	tests := []struct {
		name    string
		fsys    fstest.MapFS
		want    []int
		wantErr string
	}{
		{"ordered", fstest.MapFS{"0002_b.sql": file("B"), "0001_a.sql": file("A"), "README.md": file("")}, []int{1, 2}, ""},
		{"empty", fstest.MapFS{}, nil, ""},
		{"gap", fstest.MapFS{"0001_a.sql": file(""), "0003_c.sql": file("")}, nil, "expected version 0002"},
		{"duplicate", fstest.MapFS{"0001_a.sql": file(""), "0001_b.sql": file("")}, nil, "expected version 0002"},
		{"starts at two", fstest.MapFS{"0002_a.sql": file("")}, nil, "expected version 0001"},
		{"bad name", fstest.MapFS{"1_a.sql": file("")}, nil, "name must look like"},
		{"upper case", fstest.MapFS{"0001_Meta.sql": file("")}, nil, "name must look like"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := loadMigrations(tt.fsys)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			versions := make([]int, 0, len(got))
			for _, m := range got {
				versions = append(versions, m.version)
			}
			if fmt.Sprint(versions) != fmt.Sprint(tt.want) && len(tt.want) > 0 {
				t.Fatalf("versions = %v, want %v", versions, tt.want)
			}
		})
	}
}

func TestEmbeddedMigrations(t *testing.T) {
	ms, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 || ms[0].name != "meta" {
		t.Fatalf("embedded migrations = %+v", ms)
	}
}
