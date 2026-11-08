package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Migration 0013 adds the test state of API-key accounts: an account whose key already served a turn with tokens
// (after the key was stored) starts as ok; the others, and sign-in accounts, start empty (not tested).
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
		if _, err := tx.ExecContext(ctx, `INSERT INTO providers (id, name, backend, preset, base_url, wire_api,
			official, enabled, allowed_models, tier_fast, tier_standard, tier_strong, tier_max, created_at, updated_at)
			VALUES ('p', 'P', 'claude', '', 'https://x.example', '', 0, 1, '', '', '', '', '', ?, ?)`, keySet, keySet); err != nil {
			return err
		}
		for _, a := range []struct{ id, kind, provider, hint string }{
			{"ran", "api-key", "p", "…abcd"}, {"old", "api-key", "p", "…abcd"}, {"never", "api-key", "p", "…abcd"},
			{"nokey", "api-key", "p", ""}, {"iso", "isolated", "", ""},
		} {
			var provider any
			if a.provider != "" {
				provider = a.provider
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO accounts (id, backend, kind, label, provider, key_hint,
				key_set_at, created_at, updated_at) VALUES (?, 'claude', ?, '', ?, ?, ?, ?, ?)`,
				a.id, a.kind, provider, a.hint, keySet, keySet, keySet); err != nil {
				return err
			}
		}
		for _, u := range []struct {
			account, started string
			tokens           int
		}{{"ran", after, 10}, {"old", before, 10}, {"never", after, 0}, {"nokey", after, 10}, {"iso", after, 10}} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO usage_turns (task_id, turn, attempt, account_id, backend,
				started_at, ended_at, total_tokens) VALUES (?, 1, 1, ?, 'claude', ?, ?, ?)`,
				"t-"+u.account, u.account, u.started, ended, u.tokens); err != nil {
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
	want := map[string][2]string{"ran": {"ok", ended}, "old": {"", ""}, "never": {"", ""}, "nokey": {"", ""},
		"iso": {"", ""}}
	if err := s.Read(ctx, func(tx *sql.Tx) error {
		for id, w := range want {
			var result, tested, reset string
			if err := tx.QueryRowContext(ctx, "SELECT test_result, tested_at, test_reset_at FROM accounts WHERE id = ?",
				id).Scan(&result, &tested, &reset); err != nil {
				return err
			}
			if result != w[0] || tested != w[1] {
				t.Errorf("%s: test_result %q, tested_at %q; want %q, %q", id, result, tested, w[0], w[1])
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
