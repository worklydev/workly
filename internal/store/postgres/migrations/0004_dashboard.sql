-- Queue stats: running tasks per queue.
CREATE INDEX tasks_running ON tasks (workspace_id, queue) WHERE state = 'running';
-- Task search across queues, newest first.
CREATE INDEX tasks_search ON tasks (workspace_id, id);
-- Task search by idempotency key without a queue.
CREATE INDEX tasks_idempotency_key ON tasks (workspace_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
