-- Timestamps are Unix milliseconds (UTC); booleans are 0/1.
CREATE TABLE queues (
    workspace_id       TEXT NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    paused             INTEGER NOT NULL DEFAULT 0,
    max_attempts       INTEGER NOT NULL,
    min_backoff_ms     INTEGER NOT NULL,
    max_backoff_ms     INTEGER NOT NULL,
    backoff_multiplier REAL NOT NULL,
    timeout_ms         INTEGER NOT NULL,
    retry_on_4xx       INTEGER NOT NULL,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    PRIMARY KEY (workspace_id, name)
) STRICT;

CREATE TABLE tasks (
    id               TEXT PRIMARY KEY,
    workspace_id     TEXT NOT NULL,
    queue            TEXT NOT NULL,
    state            TEXT NOT NULL,
    method           TEXT NOT NULL,
    url              TEXT NOT NULL,
    headers          TEXT NOT NULL,
    body             BLOB NOT NULL,
    run_at           INTEGER NOT NULL,
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER,
    timeout_ms       INTEGER,
    idempotency_key  TEXT,
    trace_context    TEXT,
    lease_expires_at INTEGER,
    last_status      INTEGER,
    last_error       TEXT,
    created_at       INTEGER NOT NULL,
    updated_at       INTEGER NOT NULL,
    finished_at      INTEGER,
    FOREIGN KEY (workspace_id, queue) REFERENCES queues (workspace_id, name) ON DELETE CASCADE
) STRICT;

-- Dispatcher hot path: due pending tasks per queue.
CREATE INDEX tasks_due ON tasks (workspace_id, queue, run_at) WHERE state = 'pending';
-- Earliest pending run_at, for the dispatcher's wake-up timer.
CREATE INDEX tasks_pending_run_at ON tasks (run_at) WHERE state = 'pending';
CREATE INDEX tasks_lease ON tasks (lease_expires_at) WHERE state = 'running';
CREATE INDEX tasks_list ON tasks (workspace_id, queue, id);
CREATE UNIQUE INDEX tasks_idempotency ON tasks (workspace_id, queue, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

CREATE TABLE attempts (
    id               TEXT PRIMARY KEY,
    task_id          TEXT NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    workspace_id     TEXT NOT NULL,
    number           INTEGER NOT NULL,
    started_at       INTEGER NOT NULL,
    finished_at      INTEGER NOT NULL,
    status_code      INTEGER,
    outcome          TEXT NOT NULL,
    error            TEXT,
    response_body    TEXT,
    response_headers TEXT,
    UNIQUE (task_id, number)
) STRICT;
