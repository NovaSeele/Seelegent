package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// DB runs transactions on seelegent.db; *store.Store satisfies it.
type DB interface {
	Read(ctx context.Context, fn func(*sql.Tx) error) error
	Write(ctx context.Context, fn func(*sql.Tx) error) error
}

// nextSeqKey is the meta key of the next account number. It only grows, so an id is never given out twice.
const nextSeqKey = "account.next_seq"

// timeFormat is how times are stored: UTC, sortable as text.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Repo stores accounts and providers in SQLite. It knows nothing about files; Manager adds profiles and keys.
type Repo struct {
	db  DB
	now func() time.Time
}

// NewRepo returns a repository on db. now is the clock (time.Now when nil).
func NewRepo(db DB, now func() time.Time) *Repo {
	if now == nil {
		now = time.Now
	}
	return &Repo{db: db, now: now}
}

func (r *Repo) stamp() time.Time { return r.now().UTC().Round(0) }

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("bad time %q: %w", s, err)
	}
	return t.UTC(), nil
}

// Create inserts a new account and returns it. Isolated and API-key accounts get the next free id "acc<N>"; taken
// reports ids that are in use outside the database (an existing profile directory) and may be nil. A system
// account gets the id "system" and fails with ErrConflict when one exists. An API-key account's provider must exist
// and serve the same backend.
func (r *Repo) Create(ctx context.Context, n New, taken func(id string) bool) (Account, error) {
	if err := n.validate(); err != nil {
		return Account{}, err
	}
	now := r.stamp()
	a := Account{
		Backend: n.Backend, Kind: n.Kind, Label: n.Label, Enabled: n.Enabled, Priority: n.Priority,
		Provider: n.Provider, CreatedAt: now, UpdatedAt: now,
	}
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		if a.Kind == KindAPIKey {
			p, err := getProvider(ctx, tx, a.Provider)
			if err != nil {
				return err
			}
			if p.Backend != a.Backend {
				return invalidf("provider %s serves %s, not %s", p.ID, p.Backend, a.Backend)
			}
		}
		if a.Kind == KindSystem {
			a.ID = SystemID
			if ok, err := accountExists(ctx, tx, a.ID); err != nil || ok {
				if err == nil {
					err = fmt.Errorf("%w: a system account already exists", ErrConflict)
				}
				return err
			}
		} else {
			id, err := allocateID(ctx, tx, taken)
			if err != nil {
				return err
			}
			a.ID = id
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO accounts
			(id, backend, kind, label, enabled, priority, provider, key_hint, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?)`,
			a.ID, a.Backend, string(a.Kind), a.Label, a.Enabled, a.Priority, nullString(a.Provider),
			formatTime(now), formatTime(now))
		return err
	})
	if err != nil {
		return Account{}, wrap("create account", err)
	}
	return a, nil
}

// allocateID takes the next account number that is free in the database and on disk, and advances the counter.
func allocateID(ctx context.Context, tx *sql.Tx, taken func(string) bool) (string, error) {
	seq := 1
	var raw string
	err := tx.QueryRowContext(ctx, "SELECT value FROM meta WHERE key = ?", nextSeqKey).Scan(&raw)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return "", err
	default:
		if seq, err = strconv.Atoi(raw); err != nil || seq < 1 {
			return "", fmt.Errorf("meta %s holds %q, want a positive number", nextSeqKey, raw)
		}
	}
	for tries := 0; ; tries++ {
		if tries > 10000 {
			return "", errors.New("no free account id")
		}
		id := "acc" + strconv.Itoa(seq)
		seq++
		exists, err := accountExists(ctx, tx, id)
		if err != nil {
			return "", err
		}
		if exists || (taken != nil && taken(id)) {
			continue
		}
		_, err = tx.ExecContext(ctx,
			"INSERT INTO meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value",
			nextSeqKey, strconv.Itoa(seq))
		return id, err
	}
}

func accountExists(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, "SELECT 1 FROM accounts WHERE id = ?", id).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

const accountColumns = `id, backend, kind, label, enabled, priority, provider, key_hint, key_set_at, created_at,
	updated_at`

// Get returns the account with the id, or an error matching ErrNotFound.
func (r *Repo) Get(ctx context.Context, id string) (Account, error) {
	var a Account
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		a, err = getAccount(ctx, tx, id)
		return err
	})
	if err != nil {
		return Account{}, wrap("get account", err)
	}
	return a, nil
}

func getAccount(ctx context.Context, tx *sql.Tx, id string) (Account, error) {
	row := tx.QueryRowContext(ctx, "SELECT "+accountColumns+" FROM accounts WHERE id = ?", id)
	a, err := scanAccount(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, fmt.Errorf("account %s: %w", id, ErrNotFound)
	}
	return a, err
}

// List returns every account, highest priority first, then oldest first.
func (r *Repo) List(ctx context.Context) ([]Account, error) {
	var out []Account
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx,
			"SELECT "+accountColumns+" FROM accounts ORDER BY priority DESC, created_at, id")
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			a, err := scanAccount(rows)
			if err != nil {
				return err
			}
			out = append(out, a)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, wrap("list accounts", err)
	}
	return out, nil
}

// Update applies the patch and returns the updated account.
func (r *Repo) Update(ctx context.Context, id string, p Patch) (Account, error) {
	if p.Label != nil {
		if err := validateLabel(*p.Label); err != nil {
			return Account{}, err
		}
	}
	if p.Priority != nil {
		if err := validatePriority(*p.Priority); err != nil {
			return Account{}, err
		}
	}
	var a Account
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if a, err = getAccount(ctx, tx, id); err != nil {
			return err
		}
		if p.Label != nil {
			a.Label = *p.Label
		}
		if p.Enabled != nil {
			a.Enabled = *p.Enabled
		}
		if p.Priority != nil {
			a.Priority = *p.Priority
		}
		a.UpdatedAt = r.stamp()
		_, err = tx.ExecContext(ctx,
			"UPDATE accounts SET label = ?, enabled = ?, priority = ?, updated_at = ? WHERE id = ?",
			a.Label, a.Enabled, a.Priority, formatTime(a.UpdatedAt), id)
		return err
	})
	if err != nil {
		return Account{}, wrap("update account", err)
	}
	return a, nil
}

// SetKeyHint records the hint of the stored key ("" when the key was removed) and when the key changed.
func (r *Repo) SetKeyHint(ctx context.Context, id, hint string) error {
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		now := formatTime(r.stamp())
		res, err := tx.ExecContext(ctx, "UPDATE accounts SET key_hint = ?, key_set_at = ?, updated_at = ? WHERE id = ?",
			hint, now, now, id)
		if err != nil {
			return err
		}
		return mustAffect(res, "account", id)
	})
	return wrap("set key hint", err)
}

// Delete removes the account record. The id is not given out again.
func (r *Repo) Delete(ctx context.Context, id string) error {
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM accounts WHERE id = ?", id)
		if err != nil {
			return err
		}
		return mustAffect(res, "account", id)
	})
	return wrap("delete account", err)
}

// scanner is *sql.Row or *sql.Rows.
type scanner interface{ Scan(dest ...any) error }

func scanAccount(s scanner) (Account, error) {
	var (
		a                        Account
		kind                     string
		provider                 sql.NullString
		keySet, created, updated string
		enabled, priority        int64
	)
	if err := s.Scan(&a.ID, &a.Backend, &kind, &a.Label, &enabled, &priority, &provider, &a.KeyHint,
		&keySet, &created, &updated); err != nil {
		return Account{}, err
	}
	a.Kind = Kind(kind)
	a.Enabled = enabled != 0
	a.Priority = int(priority)
	a.Provider = provider.String
	var err error
	if keySet != "" {
		if a.KeySetAt, err = parseTime(keySet); err != nil {
			return Account{}, fmt.Errorf("account %s: %w", a.ID, err)
		}
	}
	if a.CreatedAt, err = parseTime(created); err != nil {
		return Account{}, fmt.Errorf("account %s: %w", a.ID, err)
	}
	if a.UpdatedAt, err = parseTime(updated); err != nil {
		return Account{}, fmt.Errorf("account %s: %w", a.ID, err)
	}
	return a, nil
}

// CreateProvider validates and inserts a provider; an existing id fails with ErrConflict.
func (r *Repo) CreateProvider(ctx context.Context, p Provider) (Provider, error) {
	if err := p.Validate(); err != nil {
		return Provider{}, err
	}
	now := r.stamp()
	p.CreatedAt, p.UpdatedAt = now, now
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := getProvider(ctx, tx, p.ID); err == nil {
			return fmt.Errorf("%w: provider %s already exists", ErrConflict, p.ID)
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO providers
			(id, name, backend, preset, base_url, wire_api, official, enabled, allowed_models, models,
			 tier_fast, tier_standard, tier_strong, tier_max, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			p.ID, p.Name, p.Backend, p.Preset, p.BaseURL, p.WireAPI, p.Official, p.Enabled, p.AllowedModels,
			modelsJSON(p.Models), p.Tiers.Fast, p.Tiers.Standard, p.Tiers.Strong, p.Tiers.Max, formatTime(now), formatTime(now))
		return err
	})
	if err != nil {
		return Provider{}, wrap("create provider", err)
	}
	return p, nil
}

// UpdateProvider replaces the stored provider with p (matched by id). The backend cannot change while accounts use
// the provider.
func (r *Repo) UpdateProvider(ctx context.Context, p Provider) (Provider, error) {
	if err := p.Validate(); err != nil {
		return Provider{}, err
	}
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		old, err := getProvider(ctx, tx, p.ID)
		if err != nil {
			return err
		}
		if old.Backend != p.Backend {
			n, err := providerUsers(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			if n > 0 {
				return fmt.Errorf("%w: provider %s is used by %d account(s); its backend cannot change",
					ErrConflict, p.ID, n)
			}
		}
		p.CreatedAt = old.CreatedAt
		p.UpdatedAt = r.stamp()
		_, err = tx.ExecContext(ctx, `UPDATE providers SET name = ?, backend = ?, preset = ?, base_url = ?,
			wire_api = ?, official = ?, enabled = ?, allowed_models = ?, models = ?, tier_fast = ?,
			tier_standard = ?, tier_strong = ?, tier_max = ?, updated_at = ? WHERE id = ?`,
			p.Name, p.Backend, p.Preset, p.BaseURL, p.WireAPI, p.Official, p.Enabled, p.AllowedModels,
			modelsJSON(p.Models), p.Tiers.Fast, p.Tiers.Standard, p.Tiers.Strong, p.Tiers.Max, formatTime(p.UpdatedAt), p.ID)
		return err
	})
	if err != nil {
		return Provider{}, wrap("update provider", err)
	}
	return p, nil
}

// GetProvider returns the provider with the id, or an error matching ErrNotFound.
func (r *Repo) GetProvider(ctx context.Context, id string) (Provider, error) {
	var p Provider
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = getProvider(ctx, tx, id)
		return err
	})
	if err != nil {
		return Provider{}, wrap("get provider", err)
	}
	return p, nil
}

const providerColumns = `id, name, backend, preset, base_url, wire_api, official, enabled, allowed_models, models,
	tier_fast, tier_standard, tier_strong, tier_max, created_at, updated_at`

func getProvider(ctx context.Context, tx *sql.Tx, id string) (Provider, error) {
	p, err := scanProvider(tx.QueryRowContext(ctx, "SELECT "+providerColumns+" FROM providers WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Provider{}, fmt.Errorf("provider %s: %w", id, ErrNotFound)
	}
	return p, err
}

// ListProviders returns every provider ordered by id.
func (r *Repo) ListProviders(ctx context.Context) ([]Provider, error) {
	var out []Provider
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT "+providerColumns+" FROM providers ORDER BY id")
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			p, err := scanProvider(rows)
			if err != nil {
				return err
			}
			out = append(out, p)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, wrap("list providers", err)
	}
	return out, nil
}

// DeleteProvider removes a provider; one that accounts still use fails with ErrConflict.
func (r *Repo) DeleteProvider(ctx context.Context, id string) error {
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		n, err := providerUsers(ctx, tx, id)
		if err != nil {
			return err
		}
		if n > 0 {
			return fmt.Errorf("%w: provider %s is used by %d account(s)", ErrConflict, id, n)
		}
		res, err := tx.ExecContext(ctx, "DELETE FROM providers WHERE id = ?", id)
		if err != nil {
			return err
		}
		return mustAffect(res, "provider", id)
	})
	return wrap("delete provider", err)
}

func providerUsers(ctx context.Context, tx *sql.Tx, id string) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, "SELECT count(*) FROM accounts WHERE provider = ?", id).Scan(&n)
	return n, err
}

func scanProvider(s scanner) (Provider, error) {
	var (
		p                 Provider
		official, enabled int64
		created, updated  string
		models            sql.NullString
	)
	if err := s.Scan(&p.ID, &p.Name, &p.Backend, &p.Preset, &p.BaseURL, &p.WireAPI, &official, &enabled,
		&p.AllowedModels, &models, &p.Tiers.Fast, &p.Tiers.Standard, &p.Tiers.Strong, &p.Tiers.Max, &created,
		&updated); err != nil {
		return Provider{}, err
	}
	p.Official = official != 0
	p.Enabled = enabled != 0
	var err error
	if p.Models, err = parseModels(models, p.AllowedModels); err != nil {
		return Provider{}, fmt.Errorf("provider %s: %w", p.ID, err)
	}
	if p.CreatedAt, err = parseTime(created); err != nil {
		return Provider{}, fmt.Errorf("provider %s: %w", p.ID, err)
	}
	if p.UpdatedAt, err = parseTime(updated); err != nil {
		return Provider{}, fmt.Errorf("provider %s: %w", p.ID, err)
	}
	return p, nil
}

// mustAffect turns "no row changed" into ErrNotFound.
func mustAffect(res sql.Result, what, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%s %s: %w", what, id, ErrNotFound)
	}
	return nil
}

func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// wrap prefixes err with the operation, leaving nil alone and not repeating the prefix on our own errors.
func wrap(op string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", op, err)
}

// modelsJSON is the stored form of an allow-list: a JSON array, never null.
func modelsJSON(models []string) string {
	if models == nil {
		models = []string{}
	}
	data, err := json.Marshal(models)
	if err != nil { // a []string always marshals
		return "[]"
	}
	return string(data)
}

// parseModels reads the stored allow-list. A row from before the column existed (NULL) gets the list its pattern
// was built from, or an empty list for a hand-written pattern. A value that is not a JSON array is an error, never
// a silent empty list.
func parseModels(v sql.NullString, allowed string) ([]string, error) {
	if !v.Valid {
		return ModelsOf(allowed), nil
	}
	var out []string
	if err := json.Unmarshal([]byte(v.String), &out); err != nil {
		return nil, fmt.Errorf("models is not a JSON list: %w", err)
	}
	if out == nil {
		out = []string{}
	}
	return out, nil
}
