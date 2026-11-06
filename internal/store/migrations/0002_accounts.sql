-- API-key providers: the endpoint an API-key account talks to (Anthropic-compatible for claude, OpenAI-compatible
-- for codex) and the models allowed there.
CREATE TABLE providers (
    id             TEXT    PRIMARY KEY,
    name           TEXT    NOT NULL,
    backend        TEXT    NOT NULL CHECK (backend IN ('claude', 'codex')),
    preset         TEXT    NOT NULL DEFAULT '',
    base_url       TEXT    NOT NULL DEFAULT '',
    wire_api       TEXT    NOT NULL DEFAULT '',
    official       INTEGER NOT NULL DEFAULT 0 CHECK (official IN (0, 1)),
    enabled        INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    allowed_models TEXT    NOT NULL DEFAULT '',
    tier_fast      TEXT    NOT NULL DEFAULT '',
    tier_standard  TEXT    NOT NULL DEFAULT '',
    tier_strong    TEXT    NOT NULL DEFAULT '',
    tier_max       TEXT    NOT NULL DEFAULT '',
    created_at     TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL
) STRICT;

-- Worker accounts. Ids are never reused (the next number lives in meta 'account.next_seq'), so an old task that
-- names an account can never resume on someone else's identity. Quota, cooldowns and usage belong to the pool.
CREATE TABLE accounts (
    id         TEXT    PRIMARY KEY,
    backend    TEXT    NOT NULL CHECK (backend IN ('agy', 'claude', 'codex')),
    kind       TEXT    NOT NULL CHECK (kind IN ('isolated', 'system', 'api-key')),
    label      TEXT    NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    priority   INTEGER NOT NULL DEFAULT 0,
    provider   TEXT    REFERENCES providers (id) ON DELETE RESTRICT,
    key_hint   TEXT    NOT NULL DEFAULT '',
    created_at TEXT    NOT NULL,
    updated_at TEXT    NOT NULL,
    CHECK ((kind = 'api-key') = (provider IS NOT NULL))
) STRICT;

CREATE INDEX accounts_backend ON accounts (backend);
CREATE INDEX accounts_provider ON accounts (provider);
