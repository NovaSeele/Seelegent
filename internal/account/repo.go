package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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
	var a Account
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		a, err = r.createTx(ctx, tx, n, taken)
		return err
	})
	if err != nil {
		return Account{}, wrap("create account", err)
	}
	return a, nil
}

// createTx inserts a validated new account in tx (see Create).
func (r *Repo) createTx(ctx context.Context, tx *sql.Tx, n New, taken func(id string) bool) (Account, error) {
	now := r.stamp()
	a := Account{
		Backend: n.Backend, Kind: n.Kind, Label: n.Label, Enabled: n.Enabled, Priority: n.Priority,
		Provider: n.Provider, CreatedAt: now, UpdatedAt: now,
	}
	err := func() error {
		if a.Kind == KindAPIKey {
			p, err := getProvider(ctx, tx, a.Provider)
			if err != nil {
				return err
			}
			if p.Backend != a.Backend {
				return withField("backend", invalidf("provider %s serves %s, not %s", p.ID, p.Backend, a.Backend))
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
			(id, backend, kind, label, enabled, priority, provider, key_hint, test_reset_at, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, '', ?, ?, ?)`,
			a.ID, a.Backend, string(a.Kind), a.Label, a.Enabled, a.Priority, nullString(a.Provider),
			formatTime(now), formatTime(now), formatTime(now))
		return err
	}()
	if err != nil {
		return Account{}, err
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

const accountColumns = `id, backend, kind, label, enabled, priority, provider, key_hint, key_set_at, test_result,
	tested_at, budget_usd, budget_stop, budget_level, spent_usd, spent_tokens, spent_input_tokens, spent_output_tokens,
	spent_unpriced_tokens, previous_key, service_spend, created_at, updated_at`

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
	if p.BudgetUSD != nil && *p.BudgetUSD != 0 {
		if err := validateBudget(*p.BudgetUSD); err != nil {
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
		if (p.BudgetUSD != nil || p.BudgetStop != nil) && a.Kind != KindAPIKey {
			field := "budgetUsd"
			if p.BudgetUSD == nil {
				field = "budgetStop"
			}
			return withField(field, invalidf("only an API-key account has a budget"))
		}
		if p.BudgetUSD != nil {
			a.BudgetUSD = nil
			if v := *p.BudgetUSD; v != 0 {
				a.BudgetUSD = &v
			}
		}
		if p.BudgetStop != nil {
			a.BudgetStop = *p.BudgetStop
		}
		var budget any
		if a.BudgetUSD != nil {
			budget = *a.BudgetUSD
		}
		a.UpdatedAt = r.stamp()
		if _, err = tx.ExecContext(ctx, `UPDATE accounts SET label = ?, enabled = ?, priority = ?, budget_usd = ?,
			budget_stop = ?, updated_at = ? WHERE id = ?`,
			a.Label, a.Enabled, a.Priority, budget, a.BudgetStop, formatTime(a.UpdatedAt), id); err != nil {
			return err
		}
		if err := refreshBudgetTx(ctx, tx, id); err != nil {
			return err
		}
		a, err = getAccount(ctx, tx, id)
		return err
	})
	if err != nil {
		return Account{}, wrap("update account", err)
	}
	return a, nil
}

// SetKeyHint records the hint of the stored key ("" when the key was removed) and when the key changed. The
// account's test state is cleared: it is "not tested" until a test or a turn on the new key succeeds.
func (r *Repo) SetKeyHint(ctx context.Context, id, hint string) error {
	err := r.db.Write(ctx, func(tx *sql.Tx) error { return r.setKeyHintTx(ctx, tx, id, hint) })
	return wrap("set key hint", err)
}

func (r *Repo) setKeyHintTx(ctx context.Context, tx *sql.Tx, id, hint string) error {
	_, err := r.setKeyTx(ctx, tx, id, hint)
	return err
}

// setKeyTx records a new key hint (see SetKeyHint) and starts the spend count again: what the old key spent is
// kept as the account's previous key, which it returns (nil when the old key spent nothing).
func (r *Repo) setKeyTx(ctx context.Context, tx *sql.Tx, id, hint string) (*PreviousKey, error) {
	stamp := r.stamp()
	now := formatTime(stamp)
	var oldHint string
	err := tx.QueryRowContext(ctx, "SELECT key_hint FROM accounts WHERE id = ?", id).Scan(&oldHint)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("account %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	prev, err := archiveSpendTx(ctx, tx, id, oldHint, stamp)
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `UPDATE accounts SET key_hint = ?, key_set_at = ?, test_result = '', tested_at = '',
		test_reset_at = ?, updated_at = ? WHERE id = ?`, hint, now, now, now, id)
	if err != nil {
		return nil, err
	}
	if err := mustAffect(res, "account", id); err != nil {
		return nil, err
	}
	return prev, refreshBudgetTx(ctx, tx, id)
}

// testResultRe matches a stored test result: a short reason code.
var testResultRe = regexp.MustCompile(`^[a-z][a-z_]{0,31}$`)

// RecordTest stores the result of a key test that started at started (TestOK or a reason code). A test that
// started before the key or the provider's endpoint last changed tested the old ones and is not stored (changed
// false); so is a test of an account that is not an API-key account or has no key. A missing account is not an
// error.
func (r *Repo) RecordTest(ctx context.Context, id, result string, started time.Time) (changed bool, err error) {
	if !testResultRe.MatchString(result) {
		return false, invalidf("test result %q is not a reason code", result)
	}
	return r.recordTest(ctx, id, result, started, false)
}

// MarkRunOK records that a turn that started at started ended well on the API-key account: its test state becomes
// TestOK, unless it is already, or the turn started before the key or the endpoint last changed. changed reports
// whether the state moved. A missing account is not an error (it may have been removed while the turn ran).
func (r *Repo) MarkRunOK(ctx context.Context, id string, started time.Time) (changed bool, err error) {
	return r.recordTest(ctx, id, TestOK, started, true)
}

func (r *Repo) recordTest(ctx context.Context, id, result string, started time.Time, onlyIfNotOK bool) (bool, error) {
	changed := false
	const match = ` WHERE id = ? AND kind = 'api-key' AND key_hint <> '' AND test_reset_at <= ?`
	query := `UPDATE accounts SET test_result = ?, tested_at = ?` + match
	if onlyIfNotOK {
		query += " AND test_result <> 'ok'"
		// After every good turn: a read settles the usual cases (a sign-in account, a key already marked ok)
		// without taking the write lock.
		var n int
		err := r.db.Read(ctx, func(tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, `SELECT count(*) FROM accounts`+match+` AND test_result <> 'ok'`, id,
				formatTime(started)).Scan(&n)
		})
		if err != nil || n == 0 {
			return false, wrap("record key test", err)
		}
	}
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, query, result, formatTime(r.stamp()), id, formatTime(started))
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		changed = n > 0
		return err
	})
	return changed, wrap("record key test", err)
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
		a                                Account
		kind                             string
		provider                         sql.NullString
		keySet, tested, created, updated string
		enabled, priority, stop          int64
		budget                           sql.NullFloat64
		previous, service                string
	)
	if err := s.Scan(&a.ID, &a.Backend, &kind, &a.Label, &enabled, &priority, &provider, &a.KeyHint,
		&keySet, &a.TestResult, &tested, &budget, &stop, &a.budgetLevel, &a.Spent.USD, &a.Spent.Tokens,
		&a.Spent.Input, &a.Spent.Output, &a.Spent.Unpriced, &previous, &service, &created, &updated); err != nil {
		return Account{}, err
	}
	var err error
	if a.Service, err = parseServiceSpend(service); err != nil {
		return Account{}, fmt.Errorf("account %s: %w", a.ID, err)
	}
	if budget.Valid {
		v := budget.Float64
		a.BudgetUSD = &v
	}
	a.BudgetStop = stop != 0
	if previous != "" {
		var pk PreviousKey
		if err := json.Unmarshal([]byte(previous), &pk); err != nil {
			return Account{}, fmt.Errorf("account %s: previous key: %w", a.ID, err)
		}
		a.PreviousKey = &pk
	}
	a.Kind = Kind(kind)
	a.Enabled = enabled != 0
	a.Priority = int(priority)
	a.Provider = provider.String
	if keySet != "" {
		if a.KeySetAt, err = parseTime(keySet); err != nil {
			return Account{}, fmt.Errorf("account %s: %w", a.ID, err)
		}
	}
	if tested != "" {
		if a.TestedAt, err = parseTime(tested); err != nil {
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
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		p, err = r.insertProviderTx(ctx, tx, p)
		return err
	})
	if err != nil {
		return Provider{}, wrap("create provider", err)
	}
	return p, nil
}

// insertProviderTx inserts a validated provider in tx; an existing id fails with ErrConflict.
func (r *Repo) insertProviderTx(ctx context.Context, tx *sql.Tx, p Provider) (Provider, error) {
	now := r.stamp()
	p.CreatedAt, p.UpdatedAt = now, now
	p.Prices = nil // a new provider has no prices: SetPrices sets them
	if _, err := getProvider(ctx, tx, p.ID); err == nil {
		return Provider{}, withField("id", fmt.Errorf("%w: provider %s already exists", ErrConflict, p.ID))
	} else if !errors.Is(err, ErrNotFound) {
		return Provider{}, err
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO providers
		(id, name, backend, preset, base_url, wire_api, official, enabled, allowed_models, models,
		 tier_fast, tier_standard, tier_strong, tier_max, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Backend, p.Preset, p.BaseURL, p.WireAPI, p.Official, p.Enabled, p.AllowedModels,
		modelsJSON(p.Models), p.Tiers.Fast, p.Tiers.Standard, p.Tiers.Strong, p.Tiers.Max, formatTime(now), formatTime(now))
	if err != nil {
		return Provider{}, err
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
		p.Prices = old.Prices // the price table is kept apart (SetPrices)
		if old.endpoint() != p.endpoint() {
			// Its accounts send their keys elsewhere, or another way, now: a test of the old endpoint says nothing,
			// and neither does what the old endpoint's service read.
			if _, err := tx.ExecContext(ctx, `UPDATE accounts SET test_result = '', tested_at = '', test_reset_at = ?,
				service_spend = '' WHERE provider = ?`, formatTime(p.UpdatedAt), p.ID); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE providers SET name = ?, backend = ?, preset = ?, base_url = ?,
			wire_api = ?, official = ?, enabled = ?, allowed_models = ?, models = ?, tier_fast = ?,
			tier_standard = ?, tier_strong = ?, tier_max = ?, updated_at = ? WHERE id = ?`,
			p.Name, p.Backend, p.Preset, p.BaseURL, p.WireAPI, p.Official, p.Enabled, p.AllowedModels,
			modelsJSON(p.Models), p.Tiers.Fast, p.Tiers.Standard, p.Tiers.Strong, p.Tiers.Max, formatTime(p.UpdatedAt), p.ID)
		if err != nil || (old.SpendSource() == p.SpendSource() && old.endpoint() == p.endpoint()) {
			return err
		}
		// The spend source or the service changed: the budgets of its accounts are judged again (a cost may no
		// longer count).
		return refreshProviderBudgetsTx(ctx, tx, p.ID)
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

// getProvider returns the provider with its prices.
func getProvider(ctx context.Context, tx *sql.Tx, id string) (Provider, error) {
	p, err := scanProvider(tx.QueryRowContext(ctx, "SELECT "+providerColumns+" FROM providers WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return Provider{}, fmt.Errorf("provider %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return Provider{}, err
	}
	prices, err := loadPricesTx(ctx, tx, id)
	if err != nil {
		return Provider{}, fmt.Errorf("provider %s: prices: %w", id, err)
	}
	p.Prices = prices[id]
	return p, nil
}

// ListProviders returns every provider ordered by id.
func (r *Repo) ListProviders(ctx context.Context) ([]Provider, error) {
	var out []Provider
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = listProvidersTx(ctx, tx)
		return err
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
