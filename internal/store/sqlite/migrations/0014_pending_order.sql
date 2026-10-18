-- A workspace's pending tasks in the order they run, across queues (the
-- Tasks page's Pending filter). tasks_due covers one queue.
CREATE INDEX tasks_pending_order ON tasks (workspace_id, run_at, id) WHERE state = 'pending';
