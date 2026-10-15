-- Timestamps are stored as Unix milliseconds (UTC).
CREATE TABLE workspaces (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at INTEGER NOT NULL
) STRICT;
