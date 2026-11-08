-- Task list columns (M19, internal/task): the list (GET /tasks) and the first paint (GET /state) filter on kind and
-- show what the turns add up to, so they read these columns instead of parsing every task's data and loading its
-- turns and attempts. The engine writes them on every save, from the same turns it stores.
--   kind                  Task.Kind (hidden runs: verify, consult, meeting, judge are never listed)
--   turn_count            the number of turns; -1 until the engine computed the columns below (rows written before
--                         this migration: the engine backfills them when it starts, and lists load their turns until
--                         then)
--   tokens_used           the tokens of every attempt
--   files_changed         the files edited in any turn, without duplicates
--   gate_turn, gate_state the newest turn gate since the lead's latest instruction (gate_turn 0: none)
--   last_account_id,      the newest attempt of the latest turn that names an account ('' when none)
--   last_account_label
ALTER TABLE tasks ADD COLUMN kind TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN turn_count INTEGER NOT NULL DEFAULT -1;
ALTER TABLE tasks ADD COLUMN tokens_used INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN files_changed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN gate_turn INTEGER NOT NULL DEFAULT 0;
ALTER TABLE tasks ADD COLUMN gate_state TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN last_account_id TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN last_account_label TEXT NOT NULL DEFAULT '';

UPDATE tasks SET kind = CAST(coalesce(json_extract(data, '$.kind'), '') AS TEXT);

-- The list pages newest first and skips hidden runs: with kind in the index, a hidden run is skipped without reading
-- its row (whose data may run over several pages).
DROP INDEX tasks_created;
DROP INDEX tasks_project;
CREATE INDEX tasks_created ON tasks (created_at DESC, id DESC, kind);
CREATE INDEX tasks_project ON tasks (project_key, created_at DESC, id DESC, kind);
