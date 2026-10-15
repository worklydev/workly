-- Manual retries (POST /v1/tasks/{id}/retry, retry-failed). A task keeps
-- its latest one; each attempt made after one records it, so the history
-- shows every manual retry.
ALTER TABLE tasks ADD COLUMN retried_at TIMESTAMPTZ;
ALTER TABLE attempts ADD COLUMN retried_at TIMESTAMPTZ;

-- Earlier replays weren't timed; the attempt that followed each task's
-- latest replay started right after it, which is close enough.
UPDATE attempts SET retried_at = started_at
    WHERE EXISTS (SELECT 1 FROM tasks t
        WHERE t.id = attempts.task_id AND t.replayed_attempts > 0 AND attempts.number = t.replayed_attempts + 1);
UPDATE tasks SET retried_at = (SELECT a.retried_at FROM attempts a
        WHERE a.task_id = tasks.id AND a.number = tasks.replayed_attempts + 1)
    WHERE replayed_attempts > 0;
