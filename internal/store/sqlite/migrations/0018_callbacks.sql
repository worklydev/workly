-- Callbacks for tasks and schedules (docs/callbacks.md), as groups have.
-- Every callback now goes to the _callbacks queue, so groups no longer
-- keep one.
ALTER TABLE tasks ADD COLUMN callback_url TEXT;
ALTER TABLE tasks ADD COLUMN callback_headers TEXT;
ALTER TABLE tasks ADD COLUMN callback_task_id TEXT;
ALTER TABLE schedules ADD COLUMN callback_url TEXT;
ALTER TABLE schedules ADD COLUMN callback_headers TEXT;
ALTER TABLE runs ADD COLUMN callback_task_id TEXT;
ALTER TABLE task_groups DROP COLUMN callback_queue;
