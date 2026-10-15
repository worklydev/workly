-- Queues always have a concurrency limit now. Queues without one get 100,
-- the dispatcher's global in-flight cap, so they behave as before.
UPDATE queues SET max_concurrency = 100 WHERE max_concurrency IS NULL;
