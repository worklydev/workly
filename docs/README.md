# Workly documentation

Workly is an open-source HTTP task queue and scheduler. You enqueue a task with a URL and a payload; Workly delivers it to your endpoint and retries with backoff until the endpoint answers 2xx. There are no workers to run and no broker to operate.

```text
enqueue → queue → HTTP delivery → retry → success/failure
```

It also runs [schedules](schedules.md), which call an endpoint on a cron timetable; [task groups](groups.md), which call you back once a set of tasks has finished; and [workflows](workflows.md), which run HTTP steps in dependency order.

The same binary runs everywhere: `workly dev` on your laptop (SQLite, no configuration), `workly serve` with Postgres in your own infrastructure, or hosted at [workly.run](https://workly.run). Moving between them means changing environment variables, not code.

## Start here

- [Quickstart](quickstart.md): run Workly locally, enqueue a task and handle it.
- [Concepts](concepts.md): queues, tasks, attempts, retries, delivery semantics and signing, including verifying signatures without an SDK.
- [Schedules](schedules.md): call an endpoint on a cron timetable.
- [Task groups](groups.md): fan out tasks and get one callback when they've all finished.
- [Workflows](workflows.md): run HTTP steps in dependency order, each delivered as a task, waiting for groups where they fan out.
- [Callbacks](callbacks.md): be called with the outcome each time a task or a schedule's run finishes.
- [Alerts](alerts.md): hear about failed tasks and runs by email or in Slack.
- [Configuration as code](deploy.md): keep queues, workflows and schedules in a `workly.yaml` and deploy it with `workly deploy`.
- [Self-hosting](self-hosting.md): run it in production on Docker, Compose or Kubernetes.
- [MCP](mcp.md): let coding agents see inside your queues and act on them.

## How delivery works

- **Tasks** belong to a **queue**, created on first use. The queue holds the delivery settings: retries, timeout, rate and concurrency limits. A task can override its retries and timeout.
- Each delivery is an **attempt**. A 2xx response marks the task succeeded. A 5xx, 408 or 429 response, a timeout or a connection error is retried with exponential backoff and jitter, honouring `Retry-After`. Other 4xx responses fail the task straight away, unless the queue sets `retry_on_4xx`.
- By default a queue delivers up to 3 tasks at once, makes up to 5 attempts, waits between 2 seconds and 1 hour between them, and times attempts out after 30 seconds. Raise `max_concurrency` (up to 10,000) for endpoints that can take more.
- A task that runs out of attempts is **failed**. It keeps every attempt's status, error and response body, and once the endpoint is fixed you can retry it, or all of a queue's failures at once.
- Delivery is **at least once**: if the server stops mid-delivery, the attempt is recorded as `lost` and the task is delivered again. Make handlers safe to run twice, using the `Workly-Task-Id` header, which is the same on every attempt, as a deduplication key.
- Every delivery is **signed** with `Workly-Signature`. The SDKs check it in one call, and [Concepts](concepts.md#verifying-a-signature-without-an-sdk) shows how in any language.

## Reference

- [Configuration](configuration.md): every `WORKLY_*` setting.
- [API reference](../api/openapi.yaml): the HTTP API, generated from its OpenAPI spec.
- [Metrics](metrics.md): Prometheus metrics and example alerts.
- [Performance](performance.md): load test results.
- SDKs: [Python](../sdks/python) and [TypeScript](../sdks/typescript).

For AI agents, [workly.run/docs/llms-full.txt](https://workly.run/docs/llms-full.txt) has these docs in one file, and each page is Markdown at its address plus `.md`, such as [workly.run/docs/quickstart.md](https://workly.run/docs/quickstart.md).
