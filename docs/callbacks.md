# Callbacks

A callback tells your app how something went once it has finished: a task, a schedule's run, or a [task group](groups.md). Give it an `on_complete` URL and, each time it finishes, Workly sends a `POST` there with a JSON summary: whether it succeeded, how many attempts it took, and the last status code and error.

```text
task or run finishes → callback task in _callbacks → POST to your on_complete URL
```

Use callbacks to react to a result without polling: mark an export as done, email the user that their report failed, or start the next step once this one has finished. To be told when something needs a human, use [alerts](alerts.md) instead.

## On a task

```python
from workly import Callback, Workly

client = Workly()
client.tasks.enqueue(
    "exports",
    url="https://api.example.com/jobs/export",
    payload={"export_id": 42},
    idempotency_key="export-42",
    on_complete=Callback(url="https://api.example.com/jobs/export-done"),
)
```

```ts
await workly.tasks.enqueue('exports', {
  url: 'https://api.example.com/jobs/export',
  payload: { exportId: 42 },
  idempotencyKey: 'export-42',
  onComplete: { url: 'https://api.example.com/jobs/export-done' },
});
```

```sh
curl -X POST localhost:7337/v1/queues/exports/tasks -d '{
  "url": "https://api.example.com/jobs/export",
  "payload": {"export_id": 42},
  "idempotency_key": "export-42",
  "on_complete": {"url": "https://api.example.com/jobs/export-done"}
}'
```

The CLI takes `--on-complete URL`, and `enqueue_task` over [MCP](mcp.md) takes `on_complete`. When the task succeeds, fails for good or is cancelled, the callback gets:

```json
{
  "task_id": "task_01J9ZK3M5Q8R2T4V6W8X0Y2Z4A",
  "queue": "exports",
  "state": "failed",
  "attempt_count": 5,
  "last_status": 500,
  "last_outcome": "http_error",
  "last_error": "HTTP 500 Internal Server Error",
  "idempotency_key": "export-42",
  "finished_at": "2026-11-12T10:15:02.114Z"
}
```

`last_status`, `last_outcome` and `last_error` are absent when there was no attempt (a task cancelled before it ran), and `idempotency_key` and `group_id` when the task has none. The body carries the outcome, not the response: to see what the endpoint answered, `GET /v1/tasks/{task_id}/attempts`.

## On a schedule

A schedule's `on_complete` is called for each of its runs:

```yaml
# workly.yaml
version: 1
schedules:
  nightly-report:
    cron: "0 3 * * *"
    timezone: Europe/Stockholm
    url: https://api.example.com/reports/nightly
    on_complete:
      url: https://api.example.com/reports/nightly-done
```

It can also be set with `PUT /v1/schedules/{name}`, `client.schedules.put(…, on_complete=…)`, `workly schedules put --on-complete`, `update_schedule` over MCP, or on the schedule's page in the dashboard. The body:

```json
{
  "run_id": "run_01J9ZK3M5Q8R2T4V6W8X0Y2Z4A",
  "schedule": "nightly-report",
  "state": "succeeded",
  "trigger": "schedule",
  "scheduled_for": "2026-11-12T02:00:00Z",
  "attempt_count": 1,
  "last_status": 200,
  "last_outcome": "success",
  "finished_at": "2026-11-12T02:00:01.873Z"
}
```

Skipped runs (an overlap, or a slot missed while the server was down) were never delivered and don't call back. The schedule's current `on_complete` applies when a run finishes, so a change takes effect for runs already in progress.

## When a callback fires

- **Once each time it finishes.** A task or run calls back when it succeeds, when its last attempt fails or it gets a response Workly doesn't retry, and when it is cancelled. Attempts that will be retried don't.
- **Again after a retry by hand.** Retrying a failed task, or a run, gives it a fresh set of attempts, and when it finishes again it calls back again, with the new outcome. A fix-and-retry tells your app it finally worked.
- **Not for deleted tasks.** Deleting a queue deletes its tasks without calling back.
- **Groups call back once.** A [group's](groups.md) callback fires when the group completes, with its counts, and never again.

The callback is enqueued in the same database transaction that finishes the task or run, so a crash can't lose one, and a task cancelled mid-attempt calls back once, when it's cancelled.

## The `_callbacks` queue

Every callback is a task in `_callbacks`, a queue Workly creates when it first needs one. Only Workly enqueues into it, so everything in it is a callback, and callbacks never have callbacks of their own. From there a callback is an ordinary task: signed, retried with the queue's backoff, and recorded with every attempt. The task, run or group links to it (`callback_task_id`, the latest one), and the dashboard shows it next to them. A schedule's runs also carry that task's state (`callback_state`), so a list of runs says which callbacks were delivered without fetching each one.

`_callbacks` has the default settings. To give callbacks more retries or a longer timeout, configure it like any queue, e.g. in [`workly.yaml`](deploy.md):

```yaml
queues:
  _callbacks:
    max_attempts: 10
    timeout_seconds: 300
```

If it has been deleted, it is created again with default settings rather than a callback being dropped.

## Handling a callback

A callback is delivered like any task, so the handler is an ordinary endpoint: [verify the signature](concepts.md#signing), read the summary, and answer 2xx. Headers in `on_complete` are sent with it, e.g. to route it or authenticate it:

```json
"on_complete": {"url": "https://api.example.com/jobs/done", "headers": {"X-Job": "export"}}
```

Delivery is at least once, so make the handler safe to run twice. The callback's `Workly-Task-Id` stays the same across its retries; the summary's `task_id` (or `run_id`) with `finished_at` identifies which finish it is about, since a task retried by hand calls back once per finish.
