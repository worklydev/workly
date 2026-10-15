# Workly for Python

The Python SDK for [Workly](https://github.com/worklydev/workly), the open-source HTTP task queue. Enqueue a task, and Workly delivers it to your endpoint over HTTP, retrying with backoff until it succeeds.

```sh
pip install workly          # the SDK
pip install "workly[cli]"   # the SDK plus the workly binary, for `workly dev`
```

Requires Python 3.11+. The only dependency is [httpx](https://www.python-httpx.org). The `cli` extra adds [`workly-cli`](https://pypi.org/project/workly-cli/), the server binary (about 7 MB), which you usually want on dev machines and not in production images.

## Quickstart

Start a local server with `workly dev` (SQLite, no auth, on :7337), then:

```python
from workly import Workly

client = Workly()  # WORKLY_URL, else http://localhost:7337; WORKLY_API_KEY
task = client.tasks.enqueue(
    "emails",
    url="https://api.example.com/send-email",
    payload={"user_id": 123},
)
print(task.id, task.state)
```

Your endpoint receives a `POST` with the JSON payload. Any 2xx marks the task succeeded. A 5xx, 408, 429, timeout or connection error is retried with backoff. Delivery is at-least-once, so make handlers safe to run twice. `Workly-Task-Id` is stable across attempts and works as a deduplication key.

## Verifying deliveries

Every delivery is signed: with the server's `WORKLY_SIGNING_SECRET` when you run Workly yourself, or with your workspace's secret on [workly.run](#hosted-workly). Check the signature against the **raw** body:

```python
from workly import SignatureVerificationError, verify_signature


# Flask
@app.post("/send-email")
def send_email():
    try:
        verify_signature(request.get_data(), request.headers)  # secret from WORKLY_SIGNING_SECRET
    except SignatureVerificationError:
        abort(401)
    ...


# FastAPI / Starlette
@app.post("/send-email")
async def send_email(request: Request):
    verify_signature(await request.body(), request.headers, secret)
    ...
```

By default, signatures older than 5 minutes are rejected (`tolerance=`). `workly dev` signs with `workly-dev-secret`. To test your handler without a server, use `workly.sign(body, secret)`, which returns the headers Workly would send.

## Hosted Workly

On [workly.run](https://workly.run), point the client at the hosted API and use the keys from the [console](https://console.workly.run):

```sh
export WORKLY_URL=https://api.workly.run
export WORKLY_API_KEY=...                # API keys page
export WORKLY_SIGNING_SECRET=whsec_...   # same page; each workspace has its own
```

Nothing else changes. Hosted workspaces have a request rate limit and a monthly task quota: requests over the rate limit are retried for you, and an enqueue over the quota raises `PermissionDeniedError` with the code `quota_exceeded`.

## Tasks

```python
from datetime import datetime, timedelta, timezone

client.tasks.enqueue(
    "emails",
    url="https://api.example.com/send-email",
    payload={"user_id": 123},  # JSON; or body="raw text", content_type="text/csv"
    method="POST",  # default
    headers={"X-Tenant": "acme"},  # sent with every delivery
    delay_seconds=60,  # or run_at=datetime(..., tzinfo=timezone.utc)
    idempotency_key="welcome-123",  # an existing task with this key is returned instead
    max_attempts=5,  # overrides the queue's setting
    timeout_seconds=10,  # likewise
)

# Up to 100 at once, all or nothing.
client.tasks.enqueue_many(
    "emails",
    [{"url": "https://api.example.com/send-email", "payload": {"user_id": i}} for i in range(100)],
)

task = client.tasks.get("task_01J9...")
for attempt in client.tasks.attempts(task.id):
    print(attempt.number, attempt.outcome, attempt.status_code, attempt.duration_ms)

client.tasks.retry(task.id)  # run now; replays a finished task
delivery = client.tasks.delivery(task.id)  # the signed request, to replay by hand
client.tasks.cancel(task.id)

# Newest first, across pages. All filters are optional.
for task in client.tasks.list(
    queue="emails", state="failed", created_after=datetime.now(timezone.utc) - timedelta(days=1)
):
    ...
```

## Queues

Queues are created automatically on first enqueue. `put` sets a queue's configuration declaratively, which makes it easy to keep in code: settings you leave out are reset to their defaults.

```python
client.queues.put(
    "emails",
    max_attempts=10,
    min_backoff_seconds=1,
    max_backoff_seconds=3600,
    backoff_multiplier=2,
    timeout_seconds=30,
    rate_per_second=50,
    max_concurrency=20,
)

queue = client.queues.get("emails")
print(queue.stats.due, queue.stats.oldest_due_at)

client.queues.pause("emails")  # stop delivering; enqueueing still works
client.queues.resume("emails")
client.queues.cancel_pending("emails")  # cancel pending tasks
client.queues.retry_failed("emails")  # replay failed tasks, e.g. after an outage
for group in client.queues.failures("emails"):  # failed tasks grouped by error
    print(group.count, group.error)
client.queues.retry_failed("emails", last_error="HTTP 503 Service Unavailable")  # replay one group
client.queues.activity("emails")  # succeeded/failed/cancelled in the last 7 days
client.queues.delete("emails")  # delete the queue and all its tasks
```

## Schedules

A schedule calls a URL on a cron timetable; each call is a run, retried and recorded like a task. Schedules are separate from queues. `put` is declarative, like `queues.put`. See [Schedules](https://docs.workly.run/schedules/).

```python
schedule = client.schedules.put(
    "nightly-report",
    cron="0 3 * * *",
    timezone="Europe/Stockholm",
    url="https://api.example.com/reports/nightly",
    payload={"kind": "daily"},
    max_attempts=3,
    overlap="skip",  # skip a slot while the previous run is still going
)
print(schedule.next_runs)  # the next five times

run = client.schedules.trigger("nightly-report")  # run it now
for run in client.schedules.runs("nightly-report", state="failed"):
    print(run.id, run.scheduled_for, run.last_error)
client.runs.attempts(run.id)
client.runs.retry(run.id)  # deliver it again, e.g. a failed or skipped run
client.schedules.pause("nightly-report")
client.schedules.resume("nightly-report")  # from the next scheduled time
client.schedules.delete("nightly-report")  # with all its runs
```

Runs are delivered with `Workly-Run-Id`, `Workly-Schedule` and `Workly-Scheduled-For` headers and the same signature, so `verify_signature` works unchanged.

## Async

`AsyncWorkly` has the same methods, as coroutines:

```python
from workly import AsyncWorkly

async with AsyncWorkly() as client:
    task = await client.tasks.enqueue("emails", url="...", payload={"user_id": 123})
    async for task in client.tasks.list(queue="emails"):
        ...
```

## Errors

| Exception | When |
|---|---|
| `BadRequestError` | 400, invalid request |
| `AuthenticationError` | 401, missing or wrong API key |
| `PermissionDeniedError` | 403, not allowed, or a hosted limit was reached (e.g. `quota_exceeded`) |
| `NotFoundError` | 404, no such queue or task |
| `ConflictError` | 409, e.g. retrying a running task |
| `PayloadTooLargeError` | 413 |
| `RateLimitError` | 429, still rate limited after the retries |
| `APIError` | base class of the above; has `.status`, `.code` (stable) and `.message` |
| `APIConnectionError` | the server couldn't be reached |

Requests are retried up to `max_retries` times (2 by default), honouring `Retry-After`. A 429 is always retried, since the server refused the request without doing anything. Connection errors and 502–504 are retried for requests that are safe to repeat: an enqueue only when it has an `idempotency_key`, or when the connection failed before the request was sent.

## License

[Apache-2.0](https://github.com/worklydev/workly/blob/main/LICENSE)
