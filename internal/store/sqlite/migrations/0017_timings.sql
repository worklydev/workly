-- When the first delivery attempt started, and how long the latest one
-- took, so lists can tell time spent waiting from time in the handler.
-- Not backfilled: older rows leave them NULL.
ALTER TABLE tasks ADD COLUMN started_at INTEGER;
ALTER TABLE tasks ADD COLUMN last_duration_ms INTEGER;
ALTER TABLE runs ADD COLUMN started_at INTEGER;
ALTER TABLE runs ADD COLUMN last_duration_ms INTEGER;

-- When a paused schedule was paused; NULL while it runs, and for schedules
-- paused before this migration.
ALTER TABLE schedules ADD COLUMN paused_at INTEGER;

-- Runs of every schedule, newest first.
CREATE INDEX runs_workspace ON runs (workspace_id, id);
