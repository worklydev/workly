-- Queue activity: finished tasks per queue and state, by finish time.
CREATE INDEX tasks_queue_finished ON tasks (workspace_id, queue, state, finished_at)
    WHERE finished_at IS NOT NULL;
