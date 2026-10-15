-- Tasks created over time, per workspace (the activity chart).
CREATE INDEX tasks_created ON tasks (workspace_id, created_at);
