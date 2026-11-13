-- Tasks finished over time across a workspace's queues (GET /v1/activity's
-- succeeded and failed): a range scan, like tasks_created_queue for created.
CREATE INDEX tasks_finished_ws ON tasks (workspace_id, finished_at, state) WHERE finished_at IS NOT NULL;
