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
