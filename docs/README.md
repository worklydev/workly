# Workly documentation

Workly is an open-source HTTP task queue and scheduler. You enqueue a task with a URL and a payload, and Workly delivers it to your endpoint over HTTP, retrying with backoff until the endpoint answers with a 2xx. Your application only needs to expose that endpoint: there are no workers to run and no broker to operate.

```text
enqueue → queue → HTTP delivery → retry → success/failure
```

Next to queues, [schedules](schedules.md) call an endpoint on a cron timetable, with the same retries, signing and history, and [task groups](groups.md) call you back once a set of tasks has finished.

The same binary runs everywhere: `workly dev` on your laptop (SQLite, no configuration), `workly serve` with Postgres in your own infrastructure, or hosted at [workly.run](https://workly.run). Moving between them means changing environment variables, not code.

## Start here

- [Quickstart](quickstart.md): run Workly locally, enqueue a task and handle it.
- [Schedules](schedules.md): call an endpoint on a cron timetable.
- [Task groups](groups.md): fan out tasks and get one callback when they've all finished.
- [Alerts](alerts.md): hear about failed tasks and runs by email or in Slack.
- [Configuration as code](deploy.md): keep queues and schedules in a `workly.yaml` and deploy it with `workly deploy`.
- [Self-hosting](self-hosting.md): run it in production on Docker, Compose or Kubernetes.
- [MCP](mcp.md): let coding agents see inside your queues and act on them.

## How delivery works

- **Tasks** belong to a **queue**. Queues are created on first use and hold the delivery settings: retries, timeout, rate and concurrency limits. A task can override its own retries and timeout.
- Each delivery is an **attempt**. A 2xx response marks the task succeeded. A 5xx, 408 or 429 response, a timeout or a connection error is retried with exponential backoff and jitter, honouring `Retry-After`. Other 4xx responses fail the task straight away, unless the queue sets `retry_on_4xx`.
- By default a queue delivers up to 3 tasks at once, makes up to 5 attempts, waits between 2 seconds and 1 hour between them, and times attempts out after 30 seconds. Raise `max_concurrency` (up to 10,000) for endpoints that can take more.
- A task that runs out of attempts is **failed**. It stays in the history with every attempt's status, error and response body, and can be retried once the endpoint is fixed, one at a time or a whole queue's failures at once.
- Delivery is **at least once**: if the server stops mid-delivery, the attempt is recorded as `lost` and the task is delivered again. Make handlers safe to run twice; the `Workly-Task-Id` header is stable across attempts and works as a deduplication key.
- Every delivery is **signed** with `Workly-Signature`, so your endpoint can check it came from Workly. The SDKs verify it for you.

## Reference

- [Configuration](configuration.md): every `WORKLY_*` setting.
- [API reference](../api/openapi.yaml): the HTTP API, generated from its OpenAPI spec.
- [Metrics](metrics.md): Prometheus metrics and example alerts.
- [Performance](performance.md): load test results.
- SDKs: [Python](../sdks/python) and [TypeScript](../sdks/typescript).
