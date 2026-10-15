-- When the latest attempt finished: a task's "last run", which finished_at
-- doesn't cover for a task waiting to retry or replayed.
ALTER TABLE tasks ADD COLUMN last_attempt_at TIMESTAMPTZ;
UPDATE tasks SET last_attempt_at = (SELECT max(a.finished_at) FROM attempts a WHERE a.task_id = tasks.id)
    WHERE attempt_count > 0;

-- Queue stats: failed tasks per queue.
CREATE INDEX tasks_failed ON tasks (workspace_id, queue) WHERE state = 'failed';
