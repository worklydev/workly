# Metrics

Workly serves Prometheus metrics at `/metrics`. If the server requires an API key (`WORKLY_API_KEY`), so does `/metrics`: give Prometheus the key as a bearer token.

Alternatively, set `WORKLY_METRICS_PORT` (e.g. `9090`) to serve `/metrics` on a separate port without the key, and not on the main port. Keep that port internal; the [Kubernetes manifests](../deploy/kubernetes/workly.yaml) do this and add `prometheus.io/*` scrape annotations.

```yaml
scrape_configs:
  - job_name: workly
    authorization:
      credentials: <WORKLY_API_KEY>
    static_configs:
      - targets: ["workly:7337"]
```

Metric names and labels are stable. Every per-queue metric has `workspace` and `queue` labels; `workspace` is `default` on a self-hosted server.

## Reference

| Metric | Type | Description |
|---|---|---|
| `workly_tasks_enqueued_total` | counter | Tasks enqueued. Idempotent repeats aren't counted. |
| `workly_attempts_total{outcome}` | counter | Delivery attempts by outcome: `success`, `http_error`, `timeout`, `network_error` or `lost`. |
| `workly_tasks_completed_total{state}` | counter | Tasks that finished delivery: `succeeded` or `failed`. |
| `workly_attempt_duration_seconds` | histogram | Time from request to response. |
| `workly_dispatch_lag_seconds` | histogram | Time from when a task was due to when its delivery started, including waits for rate and concurrency limits. |
| `workly_queue_due` | gauge | Pending tasks that are due now. |
| `workly_queue_scheduled` | gauge | Pending tasks due later, including ones waiting to retry. |
| `workly_queue_running` | gauge | Tasks with a delivery in flight. |
| `workly_queue_failed` | gauge | Failed tasks still retained (`WORKLY_RETENTION`). A rise means an endpoint is rejecting deliveries. |
| `workly_queue_paused` | gauge | 1 if the queue is paused. |
| `workly_queue_oldest_due_age_seconds` | gauge | How long the longest-waiting due task has been due. **The metric to alert on.** |
| `workly_dispatcher_is_leader` | gauge | 1 on the replica that delivers tasks, 0 on standbys. No labels. |

[Schedules](schedules.md) have their own metrics, with `workspace` and `schedule` labels:

| Metric | Type | Description |
|---|---|---|
| `workly_schedule_runs_total{state}` | counter | Runs that finished: `succeeded`, `failed` or `skipped`. |
| `workly_schedule_attempts_total{outcome}` | counter | Delivery attempts of runs, by outcome. |
| `workly_schedule_attempt_duration_seconds` | histogram | Time from request to response. |
| `workly_schedule_fire_lag_seconds` | histogram | Time from a scheduled time to when its run was created. |
| `workly_schedule_paused` | gauge | 1 if the schedule is paused. |
| `workly_schedule_last_success_timestamp_seconds` | gauge | When the latest successful run finished (Unix time). Absent until one succeeds. **The metric to alert on** for schedules. |

The queue gauges are read from the database when Prometheus scrapes. They stop counting at 10,000, to keep scrapes cheap with a large backlog; the age of the oldest due task has no such limit.

With several replicas, only the leader delivers, so the attempt and queue metrics come from the leader. `workly_tasks_enqueued_total` is counted by whichever replica served the request: sum it across replicas.

## Example alerts

```yaml
groups:
  - name: workly
    rules:
      - alert: WorklyQueueBehind
        expr: workly_queue_oldest_due_age_seconds > 300
        for: 5m
        annotations:
          summary: "Queue {{ $labels.queue }} is {{ $value | humanizeDuration }} behind"
      - alert: WorklyTasksFailing
        expr: sum by (queue) (rate(workly_tasks_completed_total{state="failed"}[10m])) > 0
        annotations:
          summary: "Tasks in {{ $labels.queue }} are failing after all retries"
      - alert: WorklyScheduleStale
        # A daily schedule that hasn't succeeded for over a day.
        expr: time() - workly_schedule_last_success_timestamp_seconds{schedule="nightly-report"} > 26 * 3600
        annotations:
          summary: "Schedule {{ $labels.schedule }} hasn't succeeded in over a day"
      - alert: WorklyNoLeader
        expr: max(workly_dispatcher_is_leader) == 0
        for: 1m
        annotations:
          summary: "No Workly replica is delivering tasks"
```

A paused queue falls behind by design; add `unless on (workspace, queue) workly_queue_paused == 1` to the first alert to ignore paused queues.

Each delivery attempt is also logged as one structured line (`task attempt`, or `task failed` when a task gives up) with the task ID, queue, attempt number, outcome, status, duration and next retry. Schedule runs log `schedule fired`, `schedule run skipped`, `schedule run attempt` and `schedule run failed` the same way.
