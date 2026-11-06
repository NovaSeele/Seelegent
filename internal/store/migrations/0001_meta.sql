-- Key/value facts about the database itself (for example when it was created or imported).
CREATE TABLE meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
