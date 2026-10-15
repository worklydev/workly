CREATE TABLE queues (
    workspace_id       TEXT NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    paused             BOOLEAN NOT NULL DEFAULT false,
    max_attempts       INTEGER NOT NULL,
    min_backoff_ms     BIGINT NOT NULL,
    max_backoff_ms     BIGINT NOT NULL,
    backoff_multiplier DOUBLE PRECISION NOT NULL,
    timeout_ms         BIGINT NOT NULL,
    retry_on_4xx       BOOLEAN NOT NULL,
    created_at         TIMESTAMPTZ NOT NULL,
    updated_at         TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (workspace_id, name)
);

CREATE TABLE tasks (
    id               TEXT PRIMARY KEY,
    workspace_id     TEXT NOT NULL,
    queue            TEXT NOT NULL,
    state            TEXT NOT NULL,
    method           TEXT NOT NULL,
    url              TEXT NOT NULL,
    headers          JSONB NOT NULL,
    body             BYTEA NOT NULL,
    run_at           TIMESTAMPTZ NOT NULL,
    attempt_count    INTEGER NOT NULL DEFAULT 0,
    max_attempts     INTEGER,
    timeout_ms       BIGINT,
    idempotency_key  TEXT,
    trace_context    TEXT,
    lease_expires_at TIMESTAMPTZ,
    last_status      INTEGER,
    last_error       TEXT,
    created_at       TIMESTAMPTZ NOT NULL,
    updated_at       TIMESTAMPTZ NOT NULL,
    finished_at      TIMESTAMPTZ,
    FOREIGN KEY (workspace_id, queue) REFERENCES queues (workspace_id, name) ON DELETE CASCADE
);

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
    started_at       TIMESTAMPTZ NOT NULL,
    finished_at      TIMESTAMPTZ NOT NULL,
    status_code      INTEGER,
    outcome          TEXT NOT NULL,
    error            TEXT,
    response_body    TEXT,
    response_headers JSONB,
    UNIQUE (task_id, number)
);
