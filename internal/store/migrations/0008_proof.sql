-- The proof ledger (internal/proof, milestone 13). Every key is the stable project key of the repository root
-- (fsutil.ProjectKey) and, for features, the canonical key of the feature folder (fsutil.Key), so a short path or
-- another spelling of the same folder never splits the ledger (old bugs D12, A3).

-- The last gate result of each Spec Kit task (feature folder + T-id), from a worker's gate or a regression run.
-- cmds and history are JSON lists; last_green_at is '' until the task was green once.
CREATE TABLE proof_entries (
    project_key   TEXT NOT NULL,
    feature_key   TEXT NOT NULL,
    feature       TEXT NOT NULL,
    task_id       TEXT NOT NULL,
    state         TEXT NOT NULL,
    at            TEXT NOT NULL,
    source        TEXT NOT NULL,
    ref           TEXT NOT NULL DEFAULT '',
    git_head      TEXT NOT NULL DEFAULT '',
    cmds          TEXT NOT NULL DEFAULT '[]',
    last_green_at TEXT NOT NULL DEFAULT '',
    history       TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (project_key, feature_key, task_id)
) STRICT;

-- Regression runs; data is the JSON of api.ProofRun with its results.
CREATE TABLE proof_runs (
    project_key TEXT NOT NULL,
    id          TEXT NOT NULL,
    state       TEXT NOT NULL,
    started_at  TEXT NOT NULL,
    data        TEXT NOT NULL,
    PRIMARY KEY (project_key, id)
) STRICT;

CREATE INDEX proof_runs_started ON proof_runs (project_key, started_at DESC);

-- Counters of lane decisions per project (lane refusals, forced lanes) for the gate statistics.
CREATE TABLE proof_counters (
    project_key TEXT    NOT NULL,
    name        TEXT    NOT NULL,
    value       INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (project_key, name)
) STRICT;
