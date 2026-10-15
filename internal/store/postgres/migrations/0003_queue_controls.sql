-- Rate and concurrency limits. NULL means unlimited.
ALTER TABLE queues ADD COLUMN rate_per_second DOUBLE PRECISION;
ALTER TABLE queues ADD COLUMN burst INTEGER;
ALTER TABLE queues ADD COLUMN max_concurrency INTEGER;

-- Attempts made before the task was last replayed; the attempt budget
-- counts from here.
ALTER TABLE tasks ADD COLUMN replayed_attempts INTEGER NOT NULL DEFAULT 0;

-- A lease is set exactly while an attempt is in flight, including for a
-- task cancelled mid-attempt.
DROP INDEX tasks_lease;
CREATE INDEX tasks_lease ON tasks (lease_expires_at) WHERE lease_expires_at IS NOT NULL;
-- Retention sweeper.
CREATE INDEX tasks_finished ON tasks (finished_at) WHERE finished_at IS NOT NULL;
