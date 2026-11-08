package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Migration 0013 adds the test state, spend and budget of API-key accounts. An account whose key already served a
// turn with tokens (after the key was stored) starts as ok; the others, and sign-in accounts, start empty (not
// tested). The spend is back-filled from the attempts still stored since the key was set (all of them when that
// time is unknown): tokens for every API-key account, the CLI's cost only on the official Anthropic API.
func TestAccountTestStateMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seelegent.db")
	ctx := t.Context()
	all, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var upTo []migration
	for _, m := range all {
		if m.version <= 12 {
			upTo = append(upTo, m)
		}
	}
	old, err := open(ctx, path, upTo)
	if err != nil {
		t.Fatal(err)
	}
	const (
		keySet = "2026-11-05T10:00:00.000000000Z"
		before = "2026-11-04T10:00:00.000000000Z"
		after  = "2026-11-06T10:00:00.000000000Z"
		ended  = "2026-11-06T10:05:00.000000000Z"
	)
	if err := old.Write(ctx, func(tx *sql.Tx) error {
		for _, p := range []struct {
			id, url  string
			official int
		}{{"p", "https://x.example", 0}, {"anthropic", "", 1}} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO providers (id, name, backend, preset, base_url, wire_api,
				official, enabled, allowed_models, tier_fast, tier_standard, tier_strong, tier_max, created_at, updated_at)
				VALUES (?, 'P', 'claude', '', ?, '', ?, 1, '', '', '', '', '', ?, ?)`, p.id, p.url, p.official, keySet,
				keySet); err != nil {
				return err
			}
		}
		for _, a := range []struct{ id, kind, provider, hint, keySet string }{
			{"ran", "api-key", "p", "…abcd", keySet}, {"old", "api-key", "p", "…abcd", keySet},
			{"never", "api-key", "p", "…abcd", keySet}, {"nokey", "api-key", "p", "", keySet},
			{"iso", "isolated", "", "", keySet}, {"paid", "api-key", "anthropic", "…wxyz", keySet},
			{"notime", "api-key", "anthropic", "…wxyz", ""},
		} {
			var provider any
			if a.provider != "" {
				provider = a.provider
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO accounts (id, backend, kind, label, provider, key_hint,
				key_set_at, created_at, updated_at) VALUES (?, 'claude', ?, '', ?, ?, ?, ?, ?)`,
				a.id, a.kind, provider, a.hint, a.keySet, keySet, keySet); err != nil {
				return err
			}
		}
		for i, u := range []struct {
			account, started string
			tokens           int
			cost             any
		}{
			{"ran", after, 10, 0.5}, {"old", before, 10, nil}, {"never", after, 0, nil}, {"nokey", after, 10, nil},
			{"iso", after, 10, 1.0}, {"paid", after, 100, 0.155}, {"paid", before, 50, 5.0}, {"notime", before, 7, 0.01},
		} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO usage_turns (task_id, turn, attempt, account_id, backend,
				started_at, ended_at, input_tokens, output_tokens, total_tokens, cost_usd)
				VALUES (?, 1, ?, ?, 'claude', ?, ?, ?, ?, ?, ?)`,
				"t-"+u.account, i+1, u.account, u.started, ended, u.tokens-u.tokens/4, u.tokens/4, u.tokens,
				u.cost); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	type state struct {
		result, tested string
		usd            float64
		tokens, out    int64
	}
	want := map[string]state{"ran": {"ok", ended, 0, 10, 2}, "old": {"", "", 0, 0, 0}, "never": {"", "", 0, 0, 0},
		"nokey": {"", "", 0, 10, 2}, "iso": {"", "", 0, 0, 0}, "paid": {"ok", ended, 0.155, 100, 25},
		"notime": {"ok", ended, 0.01, 7, 1}}
	if err := s.Read(ctx, func(tx *sql.Tx) error {
		for id, w := range want {
			var got state
			var stop int
			var budget sql.NullFloat64
			if err := tx.QueryRowContext(ctx, `SELECT test_result, tested_at, spent_usd, spent_tokens,
				spent_output_tokens, budget_stop, budget_usd FROM accounts WHERE id = ?`, id).Scan(&got.result,
				&got.tested, &got.usd, &got.tokens, &got.out, &stop, &budget); err != nil {
				return err
			}
			if got != w || stop != 1 || budget.Valid {
				t.Errorf("%s: %+v (stop %d, budget %v); want %+v", id, got, stop, budget, w)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
