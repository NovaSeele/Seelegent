package quota

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// timeFormat is how times are stored: UTC, sortable as text.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Store keeps the last snapshot of every account in the quota_cache table.
type Store struct {
	db DB
}

// NewStore returns the quota cache on db.
func NewStore(db DB) *Store { return &Store{db: db} }

// Get returns the snapshot of one account; found is false when it has none.
func (s *Store) Get(ctx context.Context, id string) (snap Snapshot, found bool, err error) {
	var data string
	err = s.db.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT data FROM quota_cache WHERE account_id = ?", id).Scan(&data)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return Snapshot{}, false, nil
	}
	if err != nil {
		return Snapshot{}, false, fmt.Errorf("read quota of %s: %w", id, err)
	}
	return decodeSnapshot(id, data), true, nil
}

// All returns the snapshots of every account, by account id.
func (s *Store) All(ctx context.Context) (map[string]Snapshot, error) {
	out := map[string]Snapshot{}
	err := s.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT account_id, data FROM quota_cache")
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var id, data string
			if err := rows.Scan(&id, &data); err != nil {
				return err
			}
			out[id] = decodeSnapshot(id, data)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, fmt.Errorf("read quota cache: %w", err)
	}
	return out, nil
}

// Put stores the snapshot of its account, replacing the previous one.
func (s *Store) Put(ctx context.Context, snap Snapshot, now time.Time) error {
	data, err := json.Marshal(snap)
	if err != nil {
		return fmt.Errorf("store quota of %s: %w", snap.AccountID, err)
	}
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO quota_cache (account_id, data, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (account_id) DO UPDATE SET data = excluded.data, updated_at = excluded.updated_at`,
			snap.AccountID, string(data), now.UTC().Format(timeFormat))
		return err
	})
	if err != nil {
		return fmt.Errorf("store quota of %s: %w", snap.AccountID, err)
	}
	return nil
}

// decodeSnapshot reads a stored snapshot. A row that cannot be read affects only its own account: it comes back
// as a snapshot without numbers whose error says the stored quota is unreadable, and the next good read replaces
// it. It is never silently treated as "full quota".
func decodeSnapshot(id, data string) Snapshot {
	var snap Snapshot
	if err := json.Unmarshal([]byte(data), &snap); err != nil {
		return Snapshot{AccountID: id, Source: SourceActive, Groups: []Group{},
			Error: &Error{Kind: KindFormat, Message: "the stored quota is unreadable: " + err.Error()}}
	}
	snap.AccountID = id
	return snap
}
