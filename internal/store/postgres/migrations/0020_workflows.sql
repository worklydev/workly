-- Workflows (docs/workflows.md): a DAG of HTTP steps, each delivered as a
-- task. Runs keep the definition they started with, and outlive it: there
-- is no foreign key to workflows.
CREATE TABLE workflows (
    workspace_id TEXT NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    config       JSONB NOT NULL,
    version      INTEGER NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (workspace_id, name)
);

CREATE TABLE workflow_runs (
    id              TEXT PRIMARY KEY,
    workspace_id    TEXT NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    workflow        TEXT NOT NULL,
    version         INTEGER NOT NULL,
    config          JSONB NOT NULL,
    input           JSONB,
    status          TEXT NOT NULL,
    error           TEXT,
    idempotency_key TEXT,
    schedule_run_id TEXT,
    deadline_at     TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL,
    finished_at     TIMESTAMPTZ
);

CREATE INDEX workflow_runs_list ON workflow_runs (workspace_id, workflow, id);
CREATE INDEX workflow_runs_ws ON workflow_runs (workspace_id, id);
CREATE UNIQUE INDEX workflow_runs_idempotency ON workflow_runs (workspace_id, workflow, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX workflow_runs_deadline ON workflow_runs (deadline_at) WHERE status = 'running';
CREATE INDEX workflow_runs_finished ON workflow_runs (finished_at) WHERE finished_at IS NOT NULL;

CREATE TABLE workflow_steps (
    id           TEXT PRIMARY KEY,
    run_id       TEXT NOT NULL REFERENCES workflow_runs (id) ON DELETE CASCADE,
    workspace_id TEXT NOT NULL,
    position     INTEGER NOT NULL,
    name         TEXT NOT NULL,
    depends_on   JSONB NOT NULL,
    status       TEXT NOT NULL,
    task_id      TEXT,
    wait_type    TEXT,
    wait_id      TEXT,
    error        TEXT,
    created_at   TIMESTAMPTZ NOT NULL,
    ready_at     TIMESTAMPTZ,
    http_done_at TIMESTAMPTZ,
    finished_at  TIMESTAMPTZ
);

CREATE UNIQUE INDEX workflow_steps_name ON workflow_steps (run_id, name);
CREATE INDEX workflow_steps_waiting ON workflow_steps (workspace_id, wait_type, wait_id)
    WHERE status = 'waiting';

ALTER TABLE tasks ADD COLUMN workflow_step_id TEXT;

-- A schedule can start a workflow run instead of calling a URL (its url is
-- then empty); its run links to the workflow run it started.
ALTER TABLE schedules ADD COLUMN workflow TEXT;
ALTER TABLE runs ADD COLUMN workflow_run_id TEXT;
