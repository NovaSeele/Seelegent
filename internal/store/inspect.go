package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"strings"
)

// Inspection is what Inspect read from a database without changing it.
type Inspection struct {
	// Version is the highest applied migration (0 for a database without schema_version); Known is the highest
	// this binary has.
	Version, Known int
	// Accounts and EnabledAccounts count the accounts (only when Version equals Known: an older or newer schema is
	// not read).
	Accounts, EnabledAccounts int
}

// Inspect opens the existing database at path read-only (query-only connections, no migrations, no change of
// journal mode) and reports its schema version and account counts. It never creates the file: a missing database
// is an error matching os.ErrNotExist. `seelegent doctor` uses it while the daemon may be running.
func Inspect(ctx context.Context, path string) (Inspection, error) {
	migrations, err := embeddedMigrations()
	if err != nil {
		return Inspection{}, err
	}
	out := Inspection{Known: len(migrations)}
	if path == "" || strings.ContainsAny(path, "?#") {
		return out, fmt.Errorf("inspect database: invalid path %q", path)
	}
	if _, err := os.Stat(path); err != nil {
		return out, fmt.Errorf("inspect database: %w", err)
	}
	db, err := openPool(ctx, path, true, 1)
	if err != nil {
		return out, err
	}
	defer func() { _ = db.Close() }()
	err = inTx(ctx, db, &sql.TxOptions{ReadOnly: true}, func(tx *sql.Tx) error {
		var tables int
		if err := tx.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_version'").Scan(&tables); err != nil {
			return err
		}
		if tables == 0 {
			return nil
		}
		if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_version").Scan(
			&out.Version); err != nil {
			return err
		}
		if out.Version != out.Known {
			return nil
		}
		return tx.QueryRowContext(ctx, "SELECT COUNT(*), COALESCE(SUM(enabled), 0) FROM accounts").Scan(&out.Accounts,
			&out.EnabledAccounts)
	})
	if err != nil {
		return out, fmt.Errorf("inspect database %s: %w", path, err)
	}
	if out.Version > out.Known {
		return out, fmt.Errorf("%w: database is at version %d, this binary knows %d", ErrSchemaTooNew, out.Version,
			out.Known)
	}
	return out, nil
}

// IsNotExist reports whether err from Inspect means there is no database yet.
func IsNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }
