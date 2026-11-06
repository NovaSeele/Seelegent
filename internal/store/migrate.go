package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"time"
)

// ErrSchemaTooNew is returned by Open when the database was migrated by a newer Seelegent than this binary: running
// on it could corrupt data the older code does not understand.
var ErrSchemaTooNew = errors.New("database schema is newer than this version of Seelegent")

// migrationsFS holds the schema migrations: migrations/NNNN_name.sql, numbered from 0001 without gaps. A migration
// is never edited after it is merged; changes go into a new file.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// migration is one numbered SQL script.
type migration struct {
	version int
	name    string
	sql     string
}

// migrationName matches migration file names such as 0001_meta.sql.
var migrationName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

// embeddedMigrations returns the migrations compiled into the binary.
func embeddedMigrations() ([]migration, error) {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, err
	}
	return loadMigrations(sub)
}

// loadMigrations reads every .sql file at the root of fsys and checks the numbering.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, n := range names {
		m := migrationName.FindStringSubmatch(n)
		if m == nil {
			return nil, fmt.Errorf("migration %s: name must look like 0001_name.sql", n)
		}
		version, err := strconv.Atoi(m[1])
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", n, err)
		}
		body, err := fs.ReadFile(fsys, n)
		if err != nil {
			return nil, fmt.Errorf("migration %s: %w", n, err)
		}
		out = append(out, migration{version: version, name: m[2], sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("migration %04d_%s: expected version %04d (versions start at 0001 without gaps or duplicates)",
				m.version, m.name, i+1)
		}
	}
	return out, nil
}

// createSchemaVersion creates the table that records applied migrations.
const createSchemaVersion = `CREATE TABLE IF NOT EXISTS schema_version (
	version    INTEGER PRIMARY KEY,
	name       TEXT    NOT NULL,
	applied_at TEXT    NOT NULL
) STRICT`

// migrate applies the migrations that are not recorded in schema_version, each in its own transaction together with
// its schema_version row, so a failing migration leaves the database at the previous version.
func migrate(ctx context.Context, db *sql.DB, migrations []migration) error {
	current, err := ensureSchemaVersion(ctx, db)
	if err != nil {
		return err
	}
	if current > len(migrations) {
		return fmt.Errorf("%w: database is at version %d, this binary knows %d", ErrSchemaTooNew, current, len(migrations))
	}
	for _, m := range migrations[current:] {
		if err := apply(ctx, db, m); err != nil {
			return fmt.Errorf("migration %04d_%s: %w", m.version, m.name, err)
		}
	}
	return nil
}

// ensureSchemaVersion creates schema_version if needed and returns the highest applied version.
func ensureSchemaVersion(ctx context.Context, db *sql.DB) (int, error) {
	var current int
	err := inTx(ctx, db, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, createSchemaVersion); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&current)
	})
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return current, nil
}

// apply runs one migration unless another process applied it in the meantime.
func apply(ctx context.Context, db *sql.DB, m migration) error {
	return inTx(ctx, db, nil, func(tx *sql.Tx) error {
		var done int
		if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_version WHERE version = ?", m.version).Scan(&done); err != nil {
			return err
		}
		if done > 0 {
			return nil
		}
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "INSERT INTO schema_version (version, name, applied_at) VALUES (?, ?, ?)",
			m.version, m.name, time.Now().UTC().Format(time.RFC3339))
		return err
	})
}

// SchemaVersion returns the highest migration applied to the database.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(&v)
	})
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	return v, nil
}
