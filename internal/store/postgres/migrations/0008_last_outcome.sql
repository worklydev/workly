-- How a task's latest attempt ended, next to last_status and last_error, so
-- task lists can say "timeout" without reading attempts, and failures can be
-- grouped by it.
ALTER TABLE tasks ADD COLUMN last_outcome TEXT;

UPDATE tasks SET last_outcome = (SELECT a.outcome FROM attempts a
        WHERE a.task_id = tasks.id AND a.number = tasks.attempt_count)
    WHERE attempt_count > 0;
