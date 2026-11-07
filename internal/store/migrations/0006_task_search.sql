-- Task search (internal/task): title_fold is the title lower-cased with its diacritics removed (task.foldText), so
-- a search for "tieng viet" finds "Tiếng Việt" (SQLite's LIKE folds ASCII only). The engine fills it on every save
-- and backfills rows written before this column existed when it starts.
ALTER TABLE tasks ADD COLUMN title_fold TEXT NOT NULL DEFAULT '';
