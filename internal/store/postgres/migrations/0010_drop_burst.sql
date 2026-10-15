-- Queues no longer take a burst: the dispatcher derives it from the rate.
ALTER TABLE queues DROP COLUMN burst;
