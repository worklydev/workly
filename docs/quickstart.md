# Quickstart

This runs Workly on your machine, enqueues a task and follows it to your own handler. It takes about five minutes.

## Install

```sh
pip install "workly[cli]"   # the Python SDK plus the workly binary
# or just the binary:
curl -fsSL https://raw.githubusercontent.com/worklydev/workly/main/install.sh | sh
```

Binaries for Linux, macOS and Windows are also on the [releases page](https://github.com/worklydev/workly/releases). With Docker, `docker run -p 7337:7337 ghcr.io/worklydev/workly dev`.

## Run it

```sh
workly dev
```

This starts a server on http://localhost:7337 with SQLite storage in `.workly/`, no API key, and the dashboard at the same address. It prints its settings, including the signing secret (`workly-dev-secret`).

## Enqueue a task

`workly dev` has a built-in echo target at `/_dev/echo`. With `fail_rate=0.5` it fails half the time, so you can watch retries:

```sh
curl -X POST localhost:7337/v1/queues/demo/tasks \
  -d '{"url": "http://localhost:7337/_dev/echo?fail_rate=0.5", "payload": {"hello": "world"}}'
```

The queue `demo` is created on first use. Queue names are lowercase letters, digits, `-` and `_`; names starting with `_` are reserved for Workly's own queues. Open the dashboard to see the task's attempts, or ask the API:

```sh
curl localhost:7337/v1/tasks/<id>
curl localhost:7337/v1/tasks/<id>/attempts
```

## Enqueue from your code

Python (`pip install workly`):

```python
from workly import Workly

client = Workly()  # WORKLY_URL, else http://localhost:7337; WORKLY_API_KEY
task = client.tasks.enqueue(
    "emails",
    url="http://localhost:8000/send-email",
    payload={"user_id": 123},
)
```

TypeScript (`npm install @worklydev/sdk`):

```ts
import { Workly } from '@worklydev/sdk';

const workly = new Workly(); // WORKLY_URL, else http://localhost:7337; WORKLY_API_KEY
const task = await workly.tasks.enqueue('emails', {
  url: 'http://localhost:8000/send-email',
  payload: { user_id: 123 },
});
```

Tasks can also be delayed, deduplicated with an idempotency key, and given their own retries and timeout. The SDK READMEs cover every option.

## Handle it

Your endpoint receives a `POST` with the JSON payload. Answer with any 2xx when the work is done; anything else is retried. Check the signature against the raw body first:

```python
from flask import Flask, abort, request
from workly import SignatureVerificationError, verify_signature

app = Flask(__name__)

@app.post("/send-email")
def send_email():
    try:
        verify_signature(request.get_data(), request.headers)  # secret from WORKLY_SIGNING_SECRET
    except SignatureVerificationError:
        abort(401)
    user_id = request.get_json()["user_id"]
    ...
    return "", 204
```

```ts
import { SignatureVerificationError, verifySignature } from '@worklydev/sdk';

export async function POST(request: Request) {
  const body = await request.text();
  try {
    await verifySignature(body, request.headers); // secret from WORKLY_SIGNING_SECRET
  } catch (err) {
    if (err instanceof SignatureVerificationError) return new Response(null, { status: 401 });
    throw err;
  }
  const { user_id } = JSON.parse(body);
  // ...
  return new Response(null, { status: 204 });
}
```

Run your handler with `WORKLY_SIGNING_SECRET=workly-dev-secret`. Delivery is at least once, so make the handler safe to run twice; `Workly-Task-Id` is the same on every attempt.

## Shape the queue

A queue delivers up to 3 tasks at once, with no rate limit, until you change it:

```sh
workly queues ls
curl -X PUT localhost:7337/v1/queues/emails -d '{"rate_per_second": 5, "max_concurrency": 2}'
workly queues pause emails
```

When a handler has been failing, see its failures grouped by error, fix it, and replay them:

```sh
workly queues failures emails
workly queues retry-failed emails
```

## Run something on a schedule

[Schedules](schedules.md) call an endpoint on a cron timetable, separately from queues. This one calls the echo target every minute:

```sh
workly schedules put heartbeat http://localhost:7337/_dev/echo --cron '* * * * *'
workly schedules trigger heartbeat   # run it now, too
workly schedules get heartbeat       # its next times and recent runs
```

## Next

- Connect your coding agent over [MCP](mcp.md), so it can enqueue test tasks and read failures while you work on a handler.
- Go to production with [self-hosting](self-hosting.md), or point `WORKLY_URL` at a hosted server.
