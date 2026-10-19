-- Task groups (docs/groups.md). The counts are kept up to date by every
-- change to a member task's state, so completing a group never scans its
-- tasks. tasks.group_id has no foreign key: retention may delete a group
-- before a task of it that was retried after the group completed.
CREATE TABLE task_groups (
    id               TEXT PRIMARY KEY,
    workspace_id     TEXT NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    name             TEXT,
    status           TEXT NOT NULL,
    idempotency_key  TEXT,
    total            INTEGER NOT NULL DEFAULT 0,
    succeeded        INTEGER NOT NULL DEFAULT 0,
    failed           INTEGER NOT NULL DEFAULT 0,
    cancelled        INTEGER NOT NULL DEFAULT 0,
    callback_queue   TEXT,
    callback_url     TEXT,
    callback_headers JSONB,
    callback_task_id TEXT,
    created_at       TIMESTAMPTZ NOT NULL,
    closed_at        TIMESTAMPTZ,
    finished_at      TIMESTAMPTZ
);

CREATE INDEX task_groups_list ON task_groups (workspace_id, id);
CREATE UNIQUE INDEX task_groups_idempotency ON task_groups (workspace_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX task_groups_finished ON task_groups (finished_at) WHERE finished_at IS NOT NULL;

ALTER TABLE tasks ADD COLUMN group_id TEXT;
CREATE INDEX tasks_group ON tasks (workspace_id, group_id, id) WHERE group_id IS NOT NULL;
