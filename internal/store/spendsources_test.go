package store

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// Migration 0014 adds the price table, the unpriced tokens and service reading of accounts, and how each attempt
// counted. On an existing database it marks the attempts that 0013 counted: the CLI's cost on the official
// Anthropic API, free on local services, tokens only elsewhere; attempts before the key and on sign-in accounts
// stay uncounted. Every token of a non-official, non-local API-key account was counted without a cost.
func TestSpendSourcesMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seelegent.db")
	ctx := t.Context()
	all, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var upTo []migration
	for _, m := range all {
		if m.version <= 13 {
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
	)
	if err := old.Write(ctx, func(tx *sql.Tx) error {
		for _, p := range []struct {
			id, backend, preset, url string
			official                 int
		}{
			{"anthropic", "claude", "anthropic", "", 1}, {"deepseek", "claude", "deepseek", "https://api.deepseek.com/anthropic", 0},
			{"ollama", "codex", "ollama", "http://localhost:11434/v1", 0},
			{"lo", "claude", "custom-anthropic", "http://127.0.0.1:4000", 0},
		} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO providers (id, name, backend, preset, base_url, wire_api,
				official, enabled, allowed_models, tier_fast, tier_standard, tier_strong, tier_max, created_at, updated_at)
				VALUES (?, 'P', ?, ?, ?, '', ?, 1, '', '', '', '', '', ?, ?)`, p.id, p.backend, p.preset, p.url,
				p.official, keySet, keySet); err != nil {
				return err
			}
		}
		for _, a := range []struct{ id, backend, kind, provider string }{
			{"paid", "claude", "api-key", "anthropic"}, {"ds", "claude", "api-key", "deepseek"},
			{"local", "codex", "api-key", "ollama"}, {"lo", "claude", "api-key", "lo"}, {"iso", "claude", "isolated", ""},
		} {
			var provider any
			if a.provider != "" {
				provider = a.provider
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO accounts (id, backend, kind, label, provider, key_hint,
				key_set_at, created_at, updated_at, spent_tokens) VALUES (?, ?, ?, '', ?, '…abcd', ?, ?, ?, 40)`,
				a.id, a.backend, a.kind, provider, keySet, keySet, keySet); err != nil {
				return err
			}
		}
		for i, u := range []struct {
			account, started string
			cost             any
		}{
			{"paid", after, 0.155}, {"paid", before, 5.0}, {"ds", after, 0.3}, {"local", after, nil},
			{"lo", after, nil}, {"iso", after, 1.0},
		} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO usage_turns (task_id, turn, attempt, account_id, backend,
				started_at, ended_at, total_tokens, cost_usd) VALUES (?, 1, 1, ?, 'claude', ?, ?, 10, ?)`,
				"t"+string(rune('a'+i)), u.account, u.started, u.started, u.cost); err != nil {
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
	type counted struct {
		source string
		usd    float64
	}
	want := map[string]counted{"ta": {"cli", 0.155}, "tb": {"", 0}, "tc": {"none", 0}, "td": {"free", 0},
		"te": {"free", 0}, "tf": {"", 0}}
	unpriced := map[string]int64{"paid": 0, "ds": 40, "local": 0, "lo": 0, "iso": 0}
	if err := s.Read(ctx, func(tx *sql.Tx) error {
		for task, w := range want {
			var got counted
			if err := tx.QueryRowContext(ctx, `SELECT spend_source, spend_usd FROM usage_turns WHERE task_id = ?`,
				task).Scan(&got.source, &got.usd); err != nil {
				return err
			}
			if got != w {
				t.Errorf("%s: %+v, want %+v", task, got, w)
			}
		}
		for id, w := range unpriced {
			var got int64
			var service string
			if err := tx.QueryRowContext(ctx, `SELECT spent_unpriced_tokens, service_spend FROM accounts WHERE id = ?`,
				id).Scan(&got, &service); err != nil {
				return err
			}
			if got != w || service != "" {
				t.Errorf("%s: unpriced %d (want %d), service %q", id, got, w, service)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	// The price table belongs to its provider.
	if err := s.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO provider_prices (provider, model, input_usd, output_usd,
			updated_at) VALUES ('deepseek', '*', 0.27, 1.1, ?)`, after); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO provider_prices (provider, model, input_usd, output_usd,
			updated_at) VALUES ('deepseek', 'x', -1, 1, ?)`, after); err == nil {
			t.Error("a negative price was stored")
		}
		// 0015: the cache-write price, NULL or not negative.
		if _, err := tx.ExecContext(ctx, `UPDATE provider_prices SET cache_write_usd = 0.3375 WHERE model = '*'`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE provider_prices SET cache_write_usd = -1 WHERE model = '*'`); err == nil {
			t.Error("a negative cache-write price was stored")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM accounts WHERE provider = 'deepseek'`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM providers WHERE id = 'deepseek'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT count(*) FROM provider_prices").Scan(&n)
	}); err != nil || n != 0 {
		t.Fatalf("prices of a deleted provider: %d, %v", n, err)
	}
}
