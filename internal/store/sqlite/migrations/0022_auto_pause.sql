-- A queue can pause itself after auto_pause_after failures in a row (NULL:
-- never). While auto-paused (auto_paused_at set), it delivers one probe task
-- at next_probe_at; auto_pause_probes counts the probes that failed.
ALTER TABLE queues ADD COLUMN auto_pause_after INTEGER;
ALTER TABLE queues ADD COLUMN auto_paused_at INTEGER;
ALTER TABLE queues ADD COLUMN auto_pause_error TEXT NOT NULL DEFAULT '';
ALTER TABLE queues ADD COLUMN auto_pause_probes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE queues ADD COLUMN next_probe_at INTEGER;
