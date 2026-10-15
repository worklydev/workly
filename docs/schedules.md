# Schedules

A schedule calls an HTTP endpoint on a cron timetable: a nightly report, a sync every 15 minutes, a weekly cleanup. Each call is a **run**, delivered, signed and retried like a task, with every attempt recorded. It's cron for HTTP endpoints, with a history and a "run now" button.

Schedules are separate from queues. A schedule has its own settings (retries, timeout, what to do when runs overlap) and its own history, and it never creates tasks or uses a queue's limits.

```text
Queue    → Task → Attempt
Schedule → Run  → Attempt
```

## Create a schedule

```sh
curl -X PUT localhost:7337/v1/schedules/nightly-report -d '{
  "cron": "0 3 * * *",
  "timezone": "Europe/Stockholm",
  "url": "https://api.example.com/reports/nightly",
  "payload": {"kind": "daily"}
}'
```

`PUT` is declarative, like queues: the schedule ends up with exactly the settings given, and omitted ones go back to their defaults. The response includes `next_runs`, the next five times it will run, so you can check the expression before relying on it.

From code:

```python
client.schedules.put(
    "nightly-report",
    cron="0 3 * * *",
    timezone="Europe/Stockholm",
    url="https://api.example.com/reports/nightly",
    payload={"kind": "daily"},
)
```

```ts
await workly.schedules.put('nightly-report', {
  cron: '0 3 * * *',
  timezone: 'Europe/Stockholm',
  url: 'https://api.example.com/reports/nightly',
  payload: { kind: 'daily' },
});
```

Or from the CLI:

```sh
workly schedules put nightly-report https://api.example.com/reports/nightly \
  --cron '0 3 * * *' --timezone Europe/Stockholm -d '{"kind": "daily"}'
```

## Cron expressions

Five fields: minute, hour, day of month, month and day of week.

| Expression | Runs |
|---|---|
| `*/15 * * * *` | every 15 minutes |
| `0 * * * *` | every hour, on the hour |
| `0 3 * * *` | every day at 03:00 |
| `0 9 * * mon-fri` | weekdays at 09:00 |
| `30 4 * * sun` | Sundays at 04:30 |
| `0 0 1 * *` | the first of every month at midnight |

Fields take `*`, numbers, lists (`1,15`), ranges (`1-5`), steps (`*/10`, `0-30/5`) and three-letter names for months and days (`jan`, `mon`). Day of week runs from 0 (Sunday) to 6, and 7 is Sunday too. The macros `@hourly`, `@daily`, `@weekly`, `@monthly` and `@yearly` work as usual. When both day fields are restricted, as in `0 0 13 * fri`, a day matches if either does (the 13th, or any Friday), as in classic cron. Seconds aren't supported; the shortest interval is a minute.

The expression is read in the schedule's `timezone`, an IANA name such as `Europe/Stockholm` (default `UTC`). Across daylight saving changes, a time in the skipped hour runs right after it (02:30 runs at 03:00), and a time in the repeated hour runs once.

## Settings

| Setting | Default | |
|---|---|---|
| `cron` | required | When to run. |
| `timezone` | `UTC` | |
| `url` | required | The endpoint to call. |
| `method`, `headers`, `payload` or `body`, `content_type` | `POST` | The request, as for [tasks](quickstart.md). |
| `timeout_seconds` | `30` | How long to wait for a response. |
| `max_attempts` | `3` | Attempts per run before it fails. |
| `min_backoff_seconds`, `max_backoff_seconds`, `backoff_multiplier` | `5`, `300`, `2` | Backoff between retries, as for queues. |
| `retry_on_4xx` | `false` | Retry 4xx responses other than 408 and 429. |
| `overlap` | `skip` | What to do when a run is due while the previous one is still going. |
| `missed` | `skip` | What to do about times missed while the server was down. |

Retries are fewer and shorter than a queue's by default, so a failing run gives up well before a frequent schedule's next one.

**Overlap.** With `skip`, a run that comes up while the previous run is still pending or running is recorded as `skipped`, with `skip_reason: overlap`, and not delivered. With `allow`, it runs alongside. Runs you start by hand always run.

**Missed runs.** If no server was running at a scheduled time (a run is missed if it can't start within a minute), `skip` records the most recent missed time as a `skipped` run with `skip_reason: missed` and carries on; `catch_up` runs it once. Either way, a server that was down for a day doesn't make up every run it missed.

## Runs

Each run has a state: `pending`, `running`, `succeeded`, `failed`, `cancelled` or `skipped`. A run is created once per scheduled time, even if the server fails over at that moment; like tasks, its delivery is at least once.

```sh
workly schedules runs nightly-report            # newest first
workly schedules runs nightly-report --state failed
workly runs get <run id>                        # the run and its attempts
```

The run keeps the request as the schedule had it when the run was created, so its page shows exactly what was sent even after the schedule changes. Retries use the schedule's current retry settings.

- **Run now:** `POST /v1/schedules/{name}/trigger` (or `workly schedules trigger`, or the dashboard's Run now) creates a run with `trigger: manual` and delivers it at once, even while the schedule is paused. It doesn't change when the schedule next runs.
- **Retry a run:** `POST /v1/runs/{id}/retry` delivers a failed, cancelled, succeeded or skipped run again, with a fresh set of attempts. Retrying a skipped run delivers the time it skipped.
- **Pause:** `POST /v1/schedules/{name}/pause` stops it from running; runs already pending finish. Resuming starts again from the next scheduled time. Times that pass while it's paused aren't run.
- **Delete:** deleting a schedule deletes its runs.

Finished runs are kept for `WORKLY_RUN_RETENTION` (by default the same as `WORKLY_RETENTION`, 7 days), except that each schedule's latest run is always kept, so a monthly job still shows how it last went.

## Handling a run

Your endpoint gets the request with these headers:

| Header | |
|---|---|
| `Workly-Run-Id` | The run, the same on every attempt. Use it to deduplicate. |
| `Workly-Schedule` | The schedule's name. |
| `Workly-Scheduled-For` | The scheduled time (RFC 3339). Use it, not the clock, to decide which day or hour a late or retried run is for. |
| `Workly-Attempt` | The attempt number. |
| `Workly-Signature`, `Workly-Timestamp` | The signature, as for tasks: verify it with the SDKs' `verify_signature` / `verifySignature`. |

Runs don't carry `Workly-Task-Id` or `Workly-Queue`, so one handler can tell a scheduled call from a task if it receives both.

## Monitoring

The dashboard's Schedules page shows each schedule's next run, its last run and a strip of recent runs; a schedule's page shows its upcoming times and every run with its attempts. Over [MCP](mcp.md), `get_schedule` answers "why didn't the 03:00 report run?" from the skipped and failed runs.

For alerts, `workly_schedule_last_success_timestamp_seconds` tells you when each schedule last succeeded; alert when it's older than the schedule's period. See [metrics](metrics.md).
