-- The test state of API-key accounts (internal/account, Account.TestState): test_result is the reason code of the
-- last key test ('ok', 'refused', ...) or 'ok' after a turn on the account ended well, '' when the account was never
-- tested and never ran well; tested_at is when. test_reset_at is when the key or the provider's endpoint last
-- changed (both clear the state), so a test or turn that started before it cannot vouch for the new ones.
ALTER TABLE accounts ADD COLUMN tested_at TEXT NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN test_result TEXT NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN test_reset_at TEXT NOT NULL DEFAULT '';
-- Accounts that already ran: an attempt with token usage that started after the key was stored means the provider
-- answered on that key, so the account starts as working. Times are stored in one fixed-width UTC format and
-- compare as text.
UPDATE accounts SET test_reset_at = key_set_at,
    tested_at = coalesce((SELECT max(u.ended_at) FROM usage_turns AS u
        WHERE u.account_id = accounts.id AND u.started_at >= accounts.key_set_at AND u.total_tokens > 0), '')
WHERE kind = 'api-key' AND key_hint <> '';
UPDATE accounts SET test_result = 'ok' WHERE tested_at <> '';

-- Spend and budget of API-key accounts (internal/account, spend.go). spent_* is a running total of what the
-- current key spent since key_set_at, kept by account.ApplySpendTx in the transaction that records each attempt
-- (usage_turns rows are pruned, so it is never summed from them after this back-fill). spent_usd counts the cost
-- the CLI reports (usage_turns.cost_usd, Claude Code's total_cost_usd) only for the official Anthropic API; the
-- other providers count tokens only. budget_usd is the user's budget for the key (NULL: none), budget_stop rests the
-- account once it is reached, budget_level is the state last judged ('', 'ok', 'warn', 'reached', 'unknown').
-- previous_key (JSON, '' when none) keeps what the key before the current one spent.
ALTER TABLE accounts ADD COLUMN budget_usd REAL;
ALTER TABLE accounts ADD COLUMN budget_stop INTEGER NOT NULL DEFAULT 1 CHECK (budget_stop IN (0, 1));
ALTER TABLE accounts ADD COLUMN budget_level TEXT NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN spent_usd REAL NOT NULL DEFAULT 0;
ALTER TABLE accounts ADD COLUMN spent_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE accounts ADD COLUMN spent_input_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE accounts ADD COLUMN spent_output_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE accounts ADD COLUMN previous_key TEXT NOT NULL DEFAULT '';
-- Back-fill, best effort: the attempts still in usage_turns that started on the current key (all of them when the
-- key time is unknown). Older attempts were pruned and are not counted.
UPDATE accounts SET
    spent_tokens = coalesce((SELECT sum(u.total_tokens) FROM usage_turns AS u
        WHERE u.account_id = accounts.id AND u.started_at >= accounts.key_set_at), 0),
    spent_input_tokens = coalesce((SELECT sum(u.input_tokens) FROM usage_turns AS u
        WHERE u.account_id = accounts.id AND u.started_at >= accounts.key_set_at), 0),
    spent_output_tokens = coalesce((SELECT sum(u.output_tokens) FROM usage_turns AS u
        WHERE u.account_id = accounts.id AND u.started_at >= accounts.key_set_at), 0),
    spent_usd = CASE WHEN EXISTS (SELECT 1 FROM providers AS p
            WHERE p.id = accounts.provider AND p.backend = 'claude' AND p.official = 1)
        THEN coalesce((SELECT round(sum(coalesce(u.cost_usd, 0)), 9) FROM usage_turns AS u
            WHERE u.account_id = accounts.id AND u.started_at >= accounts.key_set_at), 0)
        ELSE 0 END
WHERE kind = 'api-key';
