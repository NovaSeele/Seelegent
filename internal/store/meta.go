package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// GetMeta returns the value stored under key in the meta table and whether it exists.
func (s *Store) GetMeta(ctx context.Context, key string) (value string, found bool, err error) {
	err = s.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", key).Scan(&value)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("get meta %q: %w", key, err)
	}
	return value, true, nil
}

// SetMeta stores value under key in the meta table, replacing any earlier value.
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	err := s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			"INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
			key, value)
		return err
	})
	if err != nil {
		return fmt.Errorf("set meta %q: %w", key, err)
	}
	return nil
}
