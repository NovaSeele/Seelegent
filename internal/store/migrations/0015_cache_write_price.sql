-- The price of prompt-cache writes per provider and model, in US dollars per million tokens (internal/account:
-- prices.go). NULL: Anthropic's rule, 1.25 x the input price for five-minute writes (2 x for one-hour writes).
ALTER TABLE provider_prices ADD COLUMN cache_write_usd REAL CHECK (cache_write_usd IS NULL OR cache_write_usd >= 0);
