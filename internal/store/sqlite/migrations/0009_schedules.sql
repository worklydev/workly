-- Schedules call an endpoint on a cron schedule. They are separate from
-- queues: each firing is a run, with its own attempts.
CREATE TABLE schedules (
    workspace_id       TEXT NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name               TEXT NOT NULL,
    cron               TEXT NOT NULL,
    timezone           TEXT NOT NULL,
    method             TEXT NOT NULL,
    url                TEXT NOT NULL,
    headers            TEXT NOT NULL,
    body               BLOB NOT NULL,
    timeout_ms         INTEGER NOT NULL,
    max_attempts       INTEGER NOT NULL,
    min_backoff_ms     INTEGER NOT NULL,
    max_backoff_ms     INTEGER NOT NULL,
    backoff_multiplier REAL NOT NULL,
    retry_on_4xx       INTEGER NOT NULL,
    overlap            TEXT NOT NULL,
    missed             TEXT NOT NULL,
    paused             INTEGER NOT NULL DEFAULT 0,
    -- NULL while paused.
    next_run_at        INTEGER,
    created_at         INTEGER NOT NULL,
    updated_at         INTEGER NOT NULL,
    PRIMARY KEY (workspace_id, name)
) STRICT;

-- Due schedules and the next wake-up.
CREATE INDEX schedules_next_run ON schedules (next_run_at) WHERE next_run_at IS NOT NULL;

CREATE TABLE runs (
    id                TEXT PRIMARY KEY,
    workspace_id      TEXT NOT NULL,
    schedule          TEXT NOT NULL,
    -- schedule (a cron slot) or manual ("run now").
    trigger_type      TEXT NOT NULL,
    scheduled_for     INTEGER NOT NULL,
    state             TEXT NOT NULL,
    skip_reason       TEXT,
    method            TEXT NOT NULL,
    url               TEXT NOT NULL,
    headers           TEXT NOT NULL,
    body              BLOB NOT NULL,
    run_at            INTEGER NOT NULL,
    attempt_count     INTEGER NOT NULL DEFAULT 0,
    replayed_attempts INTEGER NOT NULL DEFAULT 0,
    lease_expires_at  INTEGER,
    last_status       INTEGER,
    last_outcome      TEXT,
    last_error        TEXT,
    created_at        INTEGER NOT NULL,
    updated_at        INTEGER NOT NULL,
    finished_at       INTEGER,
    last_attempt_at   INTEGER,
    retried_at        INTEGER,
    FOREIGN KEY (workspace_id, schedule) REFERENCES schedules (workspace_id, name) ON DELETE CASCADE
) STRICT;

-- One run per slot, even if a stale leader fires it again.
CREATE UNIQUE INDEX runs_slot ON runs (workspace_id, schedule, scheduled_for)
    WHERE trigger_type = 'schedule';
CREATE INDEX runs_due ON runs (run_at) WHERE state = 'pending';
CREATE INDEX runs_list ON runs (workspace_id, schedule, id);
CREATE INDEX runs_active ON runs (workspace_id, schedule) WHERE state IN ('pending', 'running');
CREATE INDEX runs_lease ON runs (lease_expires_at) WHERE lease_expires_at IS NOT NULL;
CREATE INDEX runs_finished ON runs (finished_at) WHERE finished_at IS NOT NULL;
CREATE INDEX runs_succeeded ON runs (workspace_id, schedule, finished_at) WHERE state = 'succeeded';

CREATE TABLE run_attempts (
    id               TEXT PRIMARY KEY,
    run_id           TEXT NOT NULL REFERENCES runs (id) ON DELETE CASCADE,
    workspace_id     TEXT NOT NULL,
    number           INTEGER NOT NULL,
    started_at       INTEGER NOT NULL,
    finished_at      INTEGER NOT NULL,
    status_code      INTEGER,
    outcome          TEXT NOT NULL,
    error            TEXT,
    response_body    TEXT,
    response_headers TEXT,
    retried_at       INTEGER,
    UNIQUE (run_id, number)
) STRICT;
