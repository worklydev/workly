# Workly

[![License: Apache-2.0](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

**The open-source Cloud Tasks.** A simple, reliable HTTP task queue and scheduler you can run locally, deploy as a pod in your cluster, or use as a managed service at [workly.run](https://workly.run).

```text
enqueue → queue → HTTP delivery → retry → success/failure
```

Next to queues, schedules call an endpoint on a cron timetable, with the same retries, signing and history: cron for HTTP endpoints, separate from queues.

> **Status:** early development. Nothing here is stable yet.

## Install

```sh
pip install "workly[cli]"   # the Python SDK plus the workly binary
# or just the binary:
curl -fsSL https://raw.githubusercontent.com/worklydev/workly/main/install.sh | sh
```

Binaries for Linux, macOS and Windows are also on the [releases page](https://github.com/worklydev/workly/releases).

With Docker (the image runs `workly serve` by default; `dev` needs no configuration):

```sh
docker run -p 7337:7337 ghcr.io/worklydev/workly dev
docker run -p 7337:7337 -e WORKLY_DATABASE_URL=postgres://... -e WORKLY_SIGNING_SECRET=... ghcr.io/worklydev/workly
```

## Try it

```sh
workly dev    # from a checkout: just dev
```

The dashboard is at http://localhost:7337.

Enqueue a task to the built-in echo target, which fails half the time:

```sh
curl -X POST localhost:7337/v1/queues/demo/tasks \
  -d '{"url": "http://localhost:7337/_dev/echo?fail_rate=0.5", "payload": {"hello": "world"}}'
```

Then follow its retries and final state in the dashboard, or:

```sh
curl localhost:7337/v1/tasks/<id>
curl localhost:7337/v1/tasks/<id>/attempts
```

Queues are created on first use. Configure them to limit how fast they deliver, then pause, resume or cancel their pending tasks:

```sh
curl -X PUT localhost:7337/v1/queues/demo -d '{"rate_per_second": 5, "max_concurrency": 2}'
curl -X POST localhost:7337/v1/queues/demo/pause
```

From Python, with the [SDK](sdks/python) (`pip install workly`):

```python
from workly import Workly

task = Workly().tasks.enqueue("demo", url="http://localhost:7337/_dev/echo", payload={"hello": "world"})
```

From TypeScript, with the [SDK](sdks/typescript) (`npm install @workly/sdk`):

```ts
import { Workly } from '@workly/sdk';

const task = await new Workly().tasks.enqueue('demo', { url: 'http://localhost:7337/_dev/echo', payload: { hello: 'world' } });
```

Call an endpoint on a cron timetable with a [schedule](docs/schedules.md). Each call is a run, retried and recorded like a task:

```sh
curl -X PUT localhost:7337/v1/schedules/nightly -d '{"cron": "0 3 * * *", "timezone": "Europe/Stockholm", "url": "http://localhost:7337/_dev/echo"}'
curl -X POST localhost:7337/v1/schedules/nightly/trigger   # run it now
```

Fan out with a [task group](docs/groups.md): enqueue tasks into a group, close it, and Workly enqueues one callback, with how they went, once they've all finished:

```python
workly = Workly()
group = workly.groups.create(name="daily-stock-prices", on_complete={"queue": "jobs", "url": "https://api.example.com/jobs/refresh-views"})
for company in companies:
    workly.tasks.enqueue("stock-prices", url="https://api.example.com/jobs/update-stock-price", payload={"symbol": company.symbol}, group_id=group.id)
workly.groups.close(group.id)
```

From your coding agent, over [MCP](docs/mcp.md): every server serves `/mcp`, so an agent can enqueue test tasks, see why deliveries fail and retry them once the handler is fixed. For Claude Code:

```sh
claude mcp add --transport http workly http://localhost:7337/mcp
```

Or with the CLI:

```sh
workly tasks enqueue demo "http://localhost:7337/_dev/echo?fail_rate=0.5" -d '{"hello": "world"}'
workly tasks get <id>
workly queues ls
workly queues failures demo       # the queue's failed tasks, grouped by error
workly queues retry-failed demo   # after an outage: replay the queue's failed tasks
workly schedules ls               # schedules with their next and last run
workly schedules runs nightly     # a schedule's runs, newest first
workly groups ls                  # task groups with their progress
```

Keep queues and schedules in a `workly.yaml` and deploy them with your app ([configuration as code](docs/deploy.md)):

```sh
workly plan                       # what would change
workly deploy --yes               # create and update until the server matches the file
```

Prometheus metrics are at `/metrics` (see [docs/metrics.md](docs/metrics.md)).

To run Workly in production with Postgres, on Docker, Compose or Kubernetes, see [docs/self-hosting.md](docs/self-hosting.md). Every setting is in [docs/configuration.md](docs/configuration.md).

Deliveries are signed with `Workly-Signature` (HMAC-SHA256 of `"<Workly-Timestamp>.<body>"`). In `workly dev` the secret is `workly-dev-secret`.

## Development

Requires Go 1.27+, Node 22+ (for the dashboard and the TypeScript SDK), [uv](https://docs.astral.sh/uv/) (for the Python SDK), [just](https://github.com/casey/just) and Docker (for Postgres tests).

```sh
just dev        # run a local server at http://localhost:7337
just build      # build bin/workly with the dashboard embedded
just dashboard-dev  # dashboard with hot reload on :5173, against `just dev`
just docs       # docs site with hot reload on :4321, from docs/
just test       # run tests
just test-all   # run tests against SQLite and Postgres
just loadtest   # load test a running server (see docs/performance.md)
just release-snapshot  # build release binaries and PyPI wheels locally
```

See [AGENTS.md](AGENTS.md) for repo conventions.

## License

Workly, its dashboard and its SDKs are licensed under the [Apache License 2.0](LICENSE).
