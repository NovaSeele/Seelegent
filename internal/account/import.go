package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Import inserts an account under the id it had in the old version (`seelegent migrate`), keeping its creation
// time, and moves the id counter past it so no new account is given that id. The id must be "system" for a system
// account and "acc<N>" otherwise; an id that is taken fails with ErrConflict.
func (r *Repo) Import(ctx context.Context, id string, n New, createdAt time.Time) (Account, error) {
	if err := n.validate(); err != nil {
		return Account{}, err
	}
	seq, err := importSeq(id, n.Kind)
	if err != nil {
		return Account{}, err
	}
	now := r.stamp()
	if createdAt.IsZero() || createdAt.After(now) {
		createdAt = now
	}
	a := Account{ID: id, Backend: n.Backend, Kind: n.Kind, Label: n.Label, Enabled: n.Enabled, Priority: n.Priority,
		Provider: n.Provider, CreatedAt: createdAt.UTC().Round(0), UpdatedAt: now}
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		if a.Kind == KindAPIKey {
			p, err := getProvider(ctx, tx, a.Provider)
			if err != nil {
				return err
			}
			if p.Backend != a.Backend {
				return invalidf("provider %s serves %s, not %s", p.ID, p.Backend, a.Backend)
			}
		}
		if ok, err := accountExists(ctx, tx, id); err != nil || ok {
			if err == nil {
				err = fmt.Errorf("%w: account %s already exists", ErrConflict, id)
			}
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO accounts
			(id, backend, kind, label, enabled, priority, provider, key_hint, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?)`,
			a.ID, a.Backend, string(a.Kind), a.Label, a.Enabled, a.Priority, nullString(a.Provider),
			formatTime(a.CreatedAt), formatTime(a.UpdatedAt)); err != nil {
			return err
		}
		return bumpSeq(ctx, tx, seq)
	})
	if err != nil {
		return Account{}, wrap("import account", err)
	}
	return a, nil
}

// importSeq checks an imported id and returns the counter value that must come after it (0 for "system").
func importSeq(id string, kind Kind) (int, error) {
	if kind == KindSystem {
		if id != SystemID {
			return 0, invalidf("a system account has the id %q, not %q", SystemID, id)
		}
		return 0, nil
	}
	num, ok := strings.CutPrefix(id, "acc")
	n, err := strconv.Atoi(num)
	if !ok || err != nil || n < 1 || strconv.Itoa(n) != num {
		return 0, invalidf("account id %q (want acc<N>)", id)
	}
	return n + 1, nil
}

// bumpSeq raises the id counter to at least seq.
func bumpSeq(ctx context.Context, tx *sql.Tx, seq int) error {
	if seq == 0 {
		return nil
	}
	cur := 1
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", nextSeqKey).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return err
	default:
		if cur, err = strconv.Atoi(raw); err != nil || cur < 1 {
			return fmt.Errorf("meta %s holds %q, want a positive number", nextSeqKey, raw)
		}
	}
	if cur >= seq {
		return nil
	}
	_, err = tx.ExecContext(ctx,
		"INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
		nextSeqKey, strconv.Itoa(seq))
	return err
}
