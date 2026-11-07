-- The team features of milestone 14 (docs/team.md). Every record is the JSON of its Go type in data; the columns
-- next to it are what lists and lookups filter on. Nothing here is a JSON file rewritten whole (old bug D6).

-- Department members (internal/member): named worker personas bound to a backend, model and role.
CREATE TABLE members (
    id         TEXT PRIMARY KEY,
    data       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
) STRICT;

-- Review notes on a task's diff (internal/notes), one row per note.
CREATE TABLE review_notes (
    id         TEXT PRIMARY KEY,
    task_id    TEXT NOT NULL,
    created_at TEXT NOT NULL,
    data       TEXT NOT NULL
) STRICT;

CREATE INDEX review_notes_task ON review_notes (task_id, created_at);

-- Meetings (internal/meeting) and contests (internal/contest). status mirrors data so the engines can load the
-- records that still have work to do without reading every finished one.
CREATE TABLE meetings (
    id         TEXT PRIMARY KEY,
    status     TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    data       TEXT NOT NULL
) STRICT;

CREATE INDEX meetings_created ON meetings (created_at DESC);

CREATE TABLE contests (
    id         TEXT PRIMARY KEY,
    status     TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    data       TEXT NOT NULL
) STRICT;

CREATE INDEX contests_created ON contests (created_at DESC);

-- The step log of meetings and contests (scope 'meeting' or 'contest'), newest last.
CREATE TABLE team_log (
    id    INTEGER PRIMARY KEY,
    scope TEXT NOT NULL CHECK (scope IN ('meeting', 'contest')),
    ref   TEXT NOT NULL,
    at    TEXT NOT NULL,
    type  TEXT NOT NULL,
    data  TEXT NOT NULL DEFAULT '{}'
) STRICT;

CREATE INDEX team_log_ref ON team_log (scope, ref, id);

-- Event hooks (internal/hook): user commands run on task, account and daemon events. position keeps the list order.
CREATE TABLE hooks (
    id       TEXT PRIMARY KEY,
    position INTEGER NOT NULL,
    data     TEXT NOT NULL
) STRICT;
