-- Project memory (internal/memory): the notes the lead keeps for the workers of one project, keyed by the project
-- key of its folder (fsutil.ProjectKey). text is capped by the memory package.
CREATE TABLE project_memory (
    project_key TEXT PRIMARY KEY,
    cwd         TEXT NOT NULL,
    text        TEXT NOT NULL,
    updated_at  TEXT NOT NULL
) STRICT;

-- The lead's review verdicts per project; the newest few are shown in worker briefs.
CREATE TABLE review_history (
    id          INTEGER PRIMARY KEY,
    project_key TEXT NOT NULL,
    at          TEXT NOT NULL,
    task_id     TEXT NOT NULL,
    title       TEXT NOT NULL,
    verdict     TEXT NOT NULL,
    notes       TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE INDEX review_history_project ON review_history (project_key, id);

-- Worker roles (internal/role). Global roles have project_key ''; a project role overrides a global role with the
-- same id for tasks in that project. data is the JSON of the role.
CREATE TABLE roles (
    scope       TEXT NOT NULL CHECK (scope IN ('global', 'project')),
    project_key TEXT NOT NULL,
    id          TEXT NOT NULL,
    cwd         TEXT NOT NULL DEFAULT '',
    data        TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    PRIMARY KEY (scope, project_key, id)
) STRICT;
