-- A failed or cancelled workflow run can be retried in place: attempt counts
-- its tries (sent to steps as Workly-Workflow-Run-Attempt), retried_at is
-- when it was last retried.
ALTER TABLE workflow_runs ADD COLUMN attempt INTEGER NOT NULL DEFAULT 1;
ALTER TABLE workflow_runs ADD COLUMN retried_at INTEGER;
