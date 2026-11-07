-- GitHub issues to pull requests and automations (milestone 15; docs/github.md, docs/automations.md). Every record
-- is the JSON of its Go type in data; the columns next to it are what lists and lookups filter on. Nothing here is a
-- JSON file rewritten whole (old bug D6): a row that cannot be read is an error, never an empty reset.

-- Connected repositories (internal/ticket). path_key is fsutil.Key of the repository root, so one folder is one repo.
CREATE TABLE github_repos (
    id         TEXT PRIMARY KEY,
    path_key   TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    data       TEXT NOT NULL
) STRICT;

-- The issue inbox of a repository: GitHub issues (key gh#<n>) and local ones (local#<n>).
CREATE TABLE github_issues (
    repo_id    TEXT NOT NULL,
    key        TEXT NOT NULL,
    state      TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    data       TEXT NOT NULL,
    PRIMARY KEY (repo_id, key)
) STRICT;

-- Tickets: one issue worked on in its own worktree and branch.
CREATE TABLE tickets (
    id         TEXT PRIMARY KEY,
    repo_id    TEXT NOT NULL,
    issue_key  TEXT NOT NULL,
    status     TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    data       TEXT NOT NULL
) STRICT;

CREATE INDEX tickets_repo ON tickets (repo_id, issue_key);
CREATE INDEX tickets_updated ON tickets (updated_at DESC);

-- The write-ahead outbox of every action that leaves the machine (commit, push, open_pr, comment, create_remote).
-- An entry is saved as running before the action starts; one still running at start is unknown and re-checked.
CREATE TABLE github_outbox (
    id       INTEGER PRIMARY KEY,
    scope    TEXT NOT NULL CHECK (scope IN ('repo', 'ticket')),
    ref      TEXT NOT NULL,
    kind     TEXT NOT NULL,
    status   TEXT NOT NULL,
    at       TEXT NOT NULL,
    done_at  TEXT,
    data     TEXT NOT NULL DEFAULT '{}'
) STRICT;

CREATE INDEX github_outbox_ref ON github_outbox (scope, ref, id);

-- The step log of a ticket, newest last (token-redacted before it is written).
CREATE TABLE ticket_log (
    id        INTEGER PRIMARY KEY,
    ticket_id TEXT NOT NULL,
    at        TEXT NOT NULL,
    type      TEXT NOT NULL,
    data      TEXT NOT NULL DEFAULT '{}'
) STRICT;

CREATE INDEX ticket_log_ticket ON ticket_log (ticket_id, id);

-- Automations (internal/automation) and their run history.
CREATE TABLE automations (
    id         TEXT PRIMARY KEY,
    enabled    INTEGER NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    data       TEXT NOT NULL
) STRICT;

CREATE TABLE automation_runs (
    id            INTEGER PRIMARY KEY,
    automation_id TEXT NOT NULL,
    at            TEXT NOT NULL,
    data          TEXT NOT NULL
) STRICT;

CREATE INDEX automation_runs_automation ON automation_runs (automation_id, id);
