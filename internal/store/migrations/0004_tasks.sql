-- Tasks of the task engine (internal/task). The columns are what lists filter and sort on; data is the JSON of
-- the rest of the task record (profile, session, spec, dependencies, review, attention, takeover, ...), written
-- together with the columns on every save. last_event_seq and last_event_at are kept by the event log only.
CREATE TABLE tasks (
    id             TEXT    PRIMARY KEY,
    title          TEXT    NOT NULL,
    status         TEXT    NOT NULL,
    project_key    TEXT    NOT NULL,
    cwd            TEXT    NOT NULL,
    backend        TEXT    NOT NULL,
    data           TEXT    NOT NULL,
    last_event_seq INTEGER NOT NULL DEFAULT 0,
    last_event_at  TEXT    NOT NULL DEFAULT '',
    created_at     TEXT    NOT NULL,
    updated_at     TEXT    NOT NULL
) STRICT;

-- The task list pages newest first with a keyset cursor over (created_at, id).
CREATE INDEX tasks_created ON tasks (created_at DESC, id DESC);
CREATE INDEX tasks_project ON tasks (project_key, created_at DESC, id DESC);
CREATE INDEX tasks_status ON tasks (status);

-- One row per turn (a lead or user message and the worker's answer); data is the JSON of the turn.
CREATE TABLE turns (
    task_id TEXT    NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    n       INTEGER NOT NULL,
    status  TEXT    NOT NULL,
    data    TEXT    NOT NULL,
    PRIMARY KEY (task_id, n)
) STRICT;

-- One row per attempt (one run of the worker CLI for a turn on one account). prompt is the full brief the worker
-- got, read only on request; data is the JSON of the rest (account, launch directory, committed stream offsets,
-- outcome).
CREATE TABLE attempts (
    task_id    TEXT    NOT NULL,
    turn       INTEGER NOT NULL,
    n          INTEGER NOT NULL,
    account_id TEXT    NOT NULL DEFAULT '',
    status     TEXT    NOT NULL,
    prompt     TEXT    NOT NULL DEFAULT '',
    data       TEXT    NOT NULL,
    PRIMARY KEY (task_id, turn, n),
    FOREIGN KEY (task_id, turn) REFERENCES turns (task_id, n) ON DELETE CASCADE
) STRICT;

-- The append-only event log of every task: seq starts at 1 and grows by one per task, assigned in the same
-- transaction that writes the event. data is the JSON of the event's data shape (internal/api).
CREATE TABLE task_events (
    task_id TEXT    NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    seq     INTEGER NOT NULL,
    ts      TEXT    NOT NULL,
    type    TEXT    NOT NULL,
    turn    INTEGER NOT NULL DEFAULT 0,
    attempt INTEGER NOT NULL DEFAULT 0,
    data    TEXT    NOT NULL,
    PRIMARY KEY (task_id, seq)
) STRICT, WITHOUT ROWID;
