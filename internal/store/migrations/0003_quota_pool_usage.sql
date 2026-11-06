-- When the API key of an account was last set or removed (internal/pool): a refused key is lifted only by a newer
-- key, not by other edits that bump updated_at. Empty when no key was ever set.
ALTER TABLE accounts ADD COLUMN key_set_at TEXT NOT NULL DEFAULT '';

-- Last quota read of each account (internal/quota). data is the JSON quota snapshot: windows, plan, entitlement
-- and the last read error. It never holds tokens or email addresses (only a hash of the signed-in identity).
CREATE TABLE quota_cache (
    account_id TEXT PRIMARY KEY REFERENCES accounts (id) ON DELETE CASCADE,
    data       TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

-- Rest periods per account and quota group (internal/pool): set when a turn runs out of quota, lifted when the
-- time passes or when a later quota read shows the group usable again.
CREATE TABLE cooldowns (
    account_id  TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    quota_group TEXT NOT NULL,
    until       TEXT NOT NULL,
    reason      TEXT NOT NULL DEFAULT '',
    passive     INTEGER NOT NULL DEFAULT 0 CHECK (passive IN (0, 1)),
    created_at  TEXT NOT NULL,
    PRIMARY KEY (account_id, quota_group)
) STRICT;

-- One snapshot per account per local clock hour (internal/usage), kept 30 days. No foreign key: the history of a
-- removed account stays readable until it ages out. quota is a JSON list of windows; labels that look like email
-- addresses are replaced by the account id before they are written.
CREATE TABLE usage_snapshots (
    account_id   TEXT    NOT NULL,
    hour         TEXT    NOT NULL,
    at           TEXT    NOT NULL,
    backend      TEXT    NOT NULL,
    label        TEXT    NOT NULL DEFAULT '',
    enabled      INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    status       TEXT    NOT NULL DEFAULT '',
    plan         TEXT    NOT NULL DEFAULT '',
    quota        TEXT    NOT NULL DEFAULT '[]',
    tokens_today INTEGER NOT NULL DEFAULT 0,
    cost_today   REAL    NOT NULL DEFAULT 0,
    PRIMARY KEY (account_id, hour)
) STRICT;

CREATE INDEX usage_snapshots_at ON usage_snapshots (at);

-- Token usage of every worker attempt (written by the task engine when an attempt ends), the source of the usage
-- summary and of the per-day counters in the snapshots. cost_usd is NULL when the CLI reports no cost.
CREATE TABLE usage_turns (
    task_id       TEXT    NOT NULL,
    turn          INTEGER NOT NULL,
    attempt       INTEGER NOT NULL,
    account_id    TEXT    NOT NULL DEFAULT '',
    backend       TEXT    NOT NULL,
    model         TEXT    NOT NULL DEFAULT '',
    project_key   TEXT    NOT NULL DEFAULT '',
    project_name  TEXT    NOT NULL DEFAULT '',
    started_at    TEXT    NOT NULL,
    ended_at      TEXT    NOT NULL,
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cached_tokens INTEGER NOT NULL DEFAULT 0,
    total_tokens  INTEGER NOT NULL DEFAULT 0,
    cost_usd      REAL,
    PRIMARY KEY (task_id, turn, attempt)
) STRICT;

CREATE INDEX usage_turns_ended ON usage_turns (ended_at);
CREATE INDEX usage_turns_account ON usage_turns (account_id, ended_at);
