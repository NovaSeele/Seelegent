// Package store owns the SQLite database (seelegent.db): opening it with the right connection settings, applying
// the embedded schema migrations, and running reads and writes in transactions. Later milestones add their
// repositories (tasks, turns, events, usage, ...) as methods on Store and their tables as new migrations.
//
// Concurrency model: the daemon is the only long-lived writer. All writes go through one pooled connection, so
// they are serialized inside the process and every write transaction starts with BEGIN IMMEDIATE (it takes the
// write lock up front and never fails half-way on a lock upgrade). Reads use a separate pool of read-only
// connections and, thanks to WAL mode, never wait for a writer. Another process that opens the database (for
// example `seelegent migrate` or `doctor`) waits up to the busy timeout instead of failing with SQLITE_BUSY.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite" // registers the "sqlite" database/sql driver (pure Go, no cgo)
)

// busyTimeout is how long a connection waits for a lock held by another connection or process.
const busyTimeout = 5 * time.Second

// maxReaders caps the read-only connections.
const maxReaders = 4

// Store is an open seelegent.db. It is safe for concurrent use.
type Store struct {
	path  string
	write *sql.DB // exactly one connection: the single writer
	read  *sql.DB // read-only connections
}

// Open opens (creating if needed) the database at path, checks that it is in WAL mode and applies the pending
// migrations. The directory of path must exist. Close the store when done.
func Open(ctx context.Context, path string) (*Store, error) {
	migrations, err := embeddedMigrations()
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	return open(ctx, path, migrations)
}

// open is Open with the migrations passed in, so tests can use their own.
func open(ctx context.Context, path string, migrations []migration) (*Store, error) {
	if path == "" || strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("open database: invalid path %q", path)
	}
	write, err := openPool(ctx, path, false, 1)
	if err != nil {
		return nil, err
	}
	if err := checkWAL(ctx, write); err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	if err := migrate(ctx, write, migrations); err != nil {
		_ = write.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	read, err := openPool(ctx, path, true, maxReaders)
	if err != nil {
		_ = write.Close()
		return nil, err
	}
	return &Store{path: path, write: write, read: read}, nil
}

// openPool opens a connection pool with the shared connection settings and checks that it works.
func openPool(ctx context.Context, path string, readOnly bool, conns int) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn(path, readOnly))
	if err != nil {
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	db.SetMaxOpenConns(conns)
	db.SetMaxIdleConns(conns)
	db.SetConnMaxIdleTime(0)
	db.SetConnMaxLifetime(0)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("open database %s: %w", path, err)
	}
	return db, nil
}

// dsn builds the modernc.org/sqlite data source name. Every connection gets the busy timeout and foreign keys;
// the writer also sets WAL mode, NORMAL synchronous (safe with WAL) and BEGIN IMMEDIATE; readers are query-only.
func dsn(path string, readOnly bool) string {
	q := fmt.Sprintf("_busy_timeout=%d&_foreign_keys=1", busyTimeout.Milliseconds())
	if readOnly {
		q += "&_query_only=1"
	} else {
		q += "&_journal_mode=WAL&_synchronous=NORMAL&_txlock=immediate"
	}
	return path + "?" + q
}

// checkWAL fails when the database could not switch to WAL mode (for example on a network file system), because the
// reader/writer split relies on it.
func checkWAL(ctx context.Context, db *sql.DB) error {
	var mode string
	if err := db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&mode); err != nil {
		return fmt.Errorf("read journal mode: %w", err)
	}
	if !strings.EqualFold(mode, "wal") {
		return fmt.Errorf("journal mode is %q, want wal (is the data directory on a network drive?)", mode)
	}
	return nil
}

// Path returns the database file path.
func (s *Store) Path() string { return s.path }

// Close closes all connections. The last connection to close checkpoints the WAL into the database file.
func (s *Store) Close() error {
	return errors.Join(s.read.Close(), s.write.Close())
}

// Write runs fn in a write transaction (BEGIN IMMEDIATE) and commits it if fn returns nil. If fn fails or panics
// the transaction is rolled back. Write transactions run one at a time; keep them short.
func (s *Store) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	return inTx(ctx, s.write, nil, fn)
}

// Read runs fn in a read-only transaction that sees one consistent snapshot of the database. Reads run in
// parallel with each other and with the writer.
func (s *Store) Read(ctx context.Context, fn func(*sql.Tx) error) error {
	return inTx(ctx, s.read, &sql.TxOptions{ReadOnly: true}, fn)
}

// inTx runs fn in a transaction on db, committing on success and rolling back on error or panic.
func inTx(ctx context.Context, db *sql.DB, opts *sql.TxOptions, fn func(*sql.Tx) error) (err error) {
	tx, err := db.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	committed = true
	return nil
}
