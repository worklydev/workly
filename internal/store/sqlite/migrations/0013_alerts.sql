-- Where a workspace's alerts go: an email address or a Slack webhook.
CREATE TABLE alert_channels (
    id           TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    type         TEXT NOT NULL,
    target       TEXT NOT NULL,
    created_at   INTEGER NOT NULL,
    UNIQUE (workspace_id, type, target)
) STRICT;

-- When each queue or schedule was last alerted about, so the next alert
-- covers only failures since and waits out the cooldown.
CREATE TABLE alert_state (
    workspace_id TEXT NOT NULL REFERENCES workspaces (id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    subject      TEXT NOT NULL,
    alerted_at   INTEGER NOT NULL,
    PRIMARY KEY (workspace_id, kind, subject)
) STRICT;

-- Recent failures in a workspace, for alerts.
CREATE INDEX tasks_failed_at ON tasks (workspace_id, finished_at) WHERE state = 'failed';
CREATE INDEX runs_failed_at ON runs (workspace_id, finished_at) WHERE state = 'failed';
