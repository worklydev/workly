-- The activity chart counts tasks per queue over time; with the queue in the
-- index it reads the index alone, not every task in the range.
CREATE INDEX tasks_created_queue ON tasks (workspace_id, created_at, queue);
DROP INDEX tasks_created;

-- Covered by tasks_queue_finished: a failed task always has finished_at, and
-- the queries that count or retry failed tasks now say so.
DROP INDEX tasks_failed;
