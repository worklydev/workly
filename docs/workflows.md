# Workflows

A workflow is a set of HTTP steps that run in dependency order: a step is sent once the steps it depends on have succeeded. Each step is an ordinary [task](concepts.md), delivered, retried and signed like any other, so a workflow adds ordering to what you already have, not a new kind of worker.

```text
prices ─┐
        ├─→ analytics ─┐
fx ─────┘              ├─→ notify
news ──────────────────┘
```

A step can also hand its completion to a [task group](groups.md): its endpoint starts the work, answers `202` with `Workly-Wait-For: group/<id>`, and the step finishes when the group does. That is how one step fans out over hundreds of tasks and the next step waits for all of them.

Workflows are deliberately static. A workflow is a DAG declared up front: there are no conditions, loops, outputs passed between steps or nested workflows, and Workly never runs your code. If a step needs data from an earlier one, read it from where that step stored it.

## Define a workflow

Declare workflows in [`workly.yaml`](deploy.md) and apply them with `workly deploy`:

```yaml
version: 1

workflows:
  daily-refresh:
    max_duration_seconds: 7200
    steps:
      - name: prices
        url: https://api.example.com/jobs/start-price-update
      - name: fx
        url: https://api.example.com/jobs/refresh-fx
      - name: news
        url: https://api.example.com/jobs/import-news
        queue: imports
      - name: analytics
        url: https://api.example.com/jobs/rebuild-analytics
        depends_on: [prices, fx]
      - name: notify
        url: https://api.example.com/jobs/notify
        depends_on: [analytics, news]
        payload: {channel: "#data"}

schedules:
  nightly-refresh:
    cron: "0 3 * * *"
    timezone: Europe/Stockholm
    workflow: daily-refresh
```

Or with `PUT /v1/workflows/{name}`, `workflows.put` in the SDKs. Steps without `depends_on` start as soon as a run does; here `prices`, `fx` and `news` run in parallel, `analytics` once `prices` and `fx` have both succeeded, and `notify` once `analytics` and `news` have.

| Step setting | Default | |
|---|---|---|
| `name` | required | Unique within the workflow: up to 64 lowercase letters, digits, `-` and `_`. |
| `url` | required | The endpoint to call. |
| `depends_on` | none | The steps that must succeed first. |
| `method`, `headers`, `payload` or `body`, `content_type` | `POST` | The request, as for [tasks](quickstart.md). Without a `payload` or `body`, the step is sent the run's context (below). |
| `queue` | `_workflows` | The queue its task goes to, for that queue's retries, timeout, rate and concurrency limits. |
| `max_attempts`, `timeout_seconds` | the queue's | Overrides for this step. |

A workflow has at most 100 steps, which must form a DAG: Workly rejects unknown or self dependencies and names the cycle in one (`steps form a cycle: a → b → a`). `max_duration_seconds` (default a day) is how long a run may take before it fails.

Steps go to the `_workflows` queue unless they name another. Like [`_callbacks`](callbacks.md), it is created when first needed and you can change its settings like any queue's, but you can't enqueue into it. A step that names one of your queues shares its limits with that queue's other tasks, which is the way to keep a workflow from overloading an endpoint other work also calls.

Changing a workflow doesn't touch runs already going: a run keeps the definition (and `version`) it started with.

## Run a workflow

```sh
workly workflows trigger daily-refresh --input '{"date": "2026-11-13"}'
```

```python
run = workly.workflows.trigger("daily-refresh", input={"date": "2026-11-13"}, idempotency_key="2026-11-13")
```

```typescript
const run = await workly.workflows.trigger('daily-refresh', { input: { date: '2026-11-13' }, idempotencyKey: '2026-11-13' });
```

Or from the dashboard's Run now, or a [schedule](schedules.md) with `workflow:` instead of `url:`, whose `payload` becomes each run's input. With an `idempotency_key` already used for a run of the workflow, that run is returned and nothing starts; a schedule uses its run's ID, so a retried schedule run never starts a second workflow run. With the schedule's `overlap: skip` (the default), a slot that comes up while the schedule's last workflow run is still going is skipped.

## What a step receives

Each step's task is delivered with the usual [headers](concepts.md) plus:

| Header | |
|---|---|
| `Workly-Workflow` | The workflow's name. |
| `Workly-Workflow-Run-Id` | The run, `wfr_…`. |
| `Workly-Workflow-Step` | The step's name. |
| `Workly-Workflow-Step-Id` | The step, `wfs_…`: the same on every delivery of the step, so use it as an idempotency key. |
| `Workly-Workflow-Run-Attempt` | `1`, then one more each time the run is [retried](#retry-a-run). |

A step without a body of its own is sent the run's context as JSON:

```json
{
  "workflow_run_id": "wfr_01M7AH8EP90ZQ4E9228337GGVR",
  "workflow": "daily-refresh",
  "step": "analytics",
  "step_id": "wfs_01M7AH8EPATE4EPK37FWTA5TBQ",
  "run_attempt": 1,
  "input": {"date": "2026-11-13"}
}
```

A 2xx response succeeds the step. Anything else is retried with its queue's policy, and a step whose task fails (out of attempts, or a 4xx that isn't retried) fails the run.

## Waiting for a group

A step that starts work Workly should track answers with `Workly-Wait-For: group/<id>` and a 2xx, typically `202`. The step then stays `waiting` until the group completes: it succeeds if every task in the group succeeded, and fails, failing the run, if any failed or was cancelled. Close the group once its tasks are enqueued, or it never completes.

```python
@app.post("/jobs/start-price-update")
def start_price_update():
    verify_signature(request.get_data(), request.headers)
    # The same on every delivery of this step in this run attempt: a redelivered
    # request reuses its group, and a retried run starts a fresh one.
    key = f'{request.headers["Workly-Workflow-Step-Id"]}-{request.headers["Workly-Workflow-Run-Attempt"]}'
    group = workly.groups.create(name="daily-stock-prices", idempotency_key=key)
    if group.status == "open":
        workly.tasks.enqueue_many(
            "stock-prices",
            [
                {
                    "url": f"{API}/jobs/update-stock-price",
                    "payload": {"symbol": s},
                    "idempotency_key": f"{key}-{s}",
                    "group_id": group.id,
                }
                for s in SYMBOLS
            ],
        )
        workly.groups.close(group.id)
    return "", 202, {"Workly-Wait-For": f"group/{group.id}"}
```

The header names exactly one group in the workspace. A header Workly can't use (another resource type, two groups, a group that doesn't exist) fails the step at once, with the reason, rather than retrying the request. A group that had already completed when the response arrived finishes the step straight away.

Handing over is safe across restarts and replicas: the step's completion is recorded in the same database transaction as the group's, so a step can't miss its group, and it can't finish twice.

## When a step fails

A run stops at its first failed step:

- steps still blocked are `skipped`,
- step tasks not yet delivered are cancelled and their steps `skipped`,
- steps waiting for a group are `skipped`; the group itself goes on, and its tasks still run,
- requests already in flight finish, and their steps record how they went, but start nothing.

The run is `failed`, with the step's error. A run that is still going after `max_duration_seconds` fails the same way, except that its waiting steps are `failed`, since they are what didn't finish in time; cancelling a run (`workly workflows cancel`, or the dashboard) skips what hasn't started. Step tasks can't be retried one at a time: [retry the run](#retry-a-run) instead. A step's task says which step and run it delivers (`workflow_step` on the task in the API, and a link to the run on its dashboard page), so a failure found in a queue leads back to its run.

## Retry a run

Once you've fixed what failed, retry the run rather than starting over: the dashboard's Retry run, `workly workflows retry ID`, `workflow_runs.retry(id)` in the SDKs or `retry_workflow_run` over [MCP](mcp.md). A failed or cancelled run then goes back to `running`, under the definition and input it started with, even if the workflow has changed since:

- steps that succeeded stay succeeded, and aren't sent again,
- failed and skipped steps run again once their dependencies have succeeded, each on the task it had, with a fresh set of attempts,
- a step that was waiting for a group follows the group: it succeeds if the group has succeeded since, waits again if it is still going, and runs again if it failed.

The run's `attempt` goes up by one, and its `max_duration_seconds` starts over. Steps are sent the attempt as `Workly-Workflow-Run-Attempt`, so work keyed on the step ID and the attempt, like the group in the example above, is redone on a retry instead of reusing what failed. To run a changed definition, start a new run.

## Statuses

| Step | |
|---|---|
| `blocked` | Waiting for the steps it depends on. |
| `pending` | Its task is queued, or waiting to retry. |
| `running` | Its request is being delivered. |
| `waiting` | Its request succeeded, and it waits for the group it named. |
| `succeeded`, `failed` | Finished. |
| `skipped` | Never ran, or was waiting for a group: the run stopped first. |

A run is `running`, then `succeeded`, `failed` or `cancelled`. `GET /v1/workflow-runs/{id}` (`workly workflows run ID`, `get_workflow_run` over [MCP](mcp.md)) shows each step with its task, its last status and error, what it is blocked by or waiting for, and how long each phase took.

## Why hasn't my run finished?

Open the run in the dashboard, or `workly workflows run ID`. Each step says why it is where it is:

- **Blocked:** the steps named in "waiting for" haven't succeeded yet; look at those.
- **Pending with attempts:** its endpoint is failing and the task is retrying; the task page has every attempt's response.
- **Pending without attempts:** its queue is paused, at its concurrency limit or behind; look at the queue.
- **Waiting for a group:** the group's tasks are still going, or the group was never closed. The group page shows which.

## Delivery is at least once

Steps are tasks, so a step's request can arrive more than once, for example when a server stops mid-delivery. Workly starts each step's task once per run however many times its dependencies finish, but your endpoints should still treat `Workly-Workflow-Step-Id` as an idempotency key, as the example above does with its group.

## Retention

Finished runs are deleted with schedule runs, after the server's run retention (`WORKLY_RUN_RETENTION`, see [Configuration](configuration.md)); a workflow's latest run is always kept. Step tasks are deleted with other tasks, so an old run's steps may no longer link to their task.
