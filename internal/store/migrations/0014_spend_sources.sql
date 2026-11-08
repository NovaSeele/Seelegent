-- Spend sources of API-key accounts (internal/account: prices.go, service.go, spend.go).
--
-- provider_prices is the user's price table per provider and model, in US dollars per million tokens: input,
-- cached input (NULL: the input price) and output. model '*' prices every model of the provider that has no row of
-- its own. Seelegent ships no prices; a row exists only when the user set it.
CREATE TABLE provider_prices (
    provider          TEXT NOT NULL REFERENCES providers (id) ON DELETE CASCADE,
    model             TEXT NOT NULL,
    input_usd         REAL NOT NULL CHECK (input_usd >= 0),
    cached_input_usd  REAL CHECK (cached_input_usd IS NULL OR cached_input_usd >= 0),
    output_usd        REAL NOT NULL CHECK (output_usd >= 0),
    updated_at        TEXT NOT NULL,
    PRIMARY KEY (provider, model)
) STRICT;

-- spent_unpriced_tokens: the part of spent_tokens counted without a cost (no price for the model on a provider whose
-- spend needs one). service_spend: the last read of the service's own spend endpoint (JSON, '' when none): OpenRouter
-- usage of the key, DeepSeek and Moonshot account balance, with the time of the read and the last failure.
ALTER TABLE accounts ADD COLUMN spent_unpriced_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE accounts ADD COLUMN service_spend TEXT NOT NULL DEFAULT '';

-- How each attempt was counted in its account's running spend, so a replaced attempt record takes back exactly what
-- it added, whatever the prices are by then: spend_source is 'cli', 'prices', 'free' or 'none' ('' when the attempt
-- was not counted: a sign-in account, or an attempt that started before the key was set), spend_usd the dollars
-- added.
ALTER TABLE usage_turns ADD COLUMN spend_source TEXT NOT NULL DEFAULT '';
ALTER TABLE usage_turns ADD COLUMN spend_usd REAL NOT NULL DEFAULT 0;

-- Back-fill what migration 0013 counted: the attempts on API-key accounts since the key was set; the CLI's cost on
-- the official Anthropic API, nothing on local services, tokens only elsewhere. Local is told apart here by the
-- Ollama preset or a loopback base URL (best effort; the daemon decides with account.Provider.SpendSource).
UPDATE usage_turns SET
    spend_source = (SELECT CASE
            WHEN p.backend = 'claude' AND p.official = 1 THEN 'cli'
            WHEN p.preset = 'ollama' OR p.base_url GLOB 'http*://localhost' OR p.base_url GLOB 'http*://localhost[:/]*'
                OR p.base_url GLOB 'http*://127.*' OR p.base_url GLOB 'http*://[[]::1]*' THEN 'free'
            ELSE 'none' END
        FROM accounts AS a JOIN providers AS p ON p.id = a.provider WHERE a.id = usage_turns.account_id),
    spend_usd = coalesce((SELECT round(coalesce(usage_turns.cost_usd, 0), 9)
        FROM accounts AS a JOIN providers AS p ON p.id = a.provider
        WHERE a.id = usage_turns.account_id AND p.backend = 'claude' AND p.official = 1), 0)
WHERE EXISTS (SELECT 1 FROM accounts AS a WHERE a.id = usage_turns.account_id AND a.kind = 'api-key'
    AND a.provider IS NOT NULL AND usage_turns.started_at >= a.key_set_at);

-- No price existed before this migration: every token of an API-key account whose provider is neither the official
-- Anthropic API nor local was counted without a cost.
UPDATE accounts SET spent_unpriced_tokens = spent_tokens
WHERE kind = 'api-key' AND EXISTS (SELECT 1 FROM providers AS p WHERE p.id = accounts.provider
    AND NOT (p.backend = 'claude' AND p.official = 1)
    AND NOT (p.preset = 'ollama' OR p.base_url GLOB 'http*://localhost' OR p.base_url GLOB 'http*://localhost[:/]*'
        OR p.base_url GLOB 'http*://127.*' OR p.base_url GLOB 'http*://[[]::1]*'));
