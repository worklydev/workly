"""Response types. They mirror the schemas in api/openapi.yaml.

Unknown fields are ignored, so newer servers don't break older SDKs.
"""

from __future__ import annotations

from collections.abc import Mapping
from dataclasses import dataclass, field
from datetime import datetime
from typing import Any, Literal

TaskState = Literal["pending", "running", "succeeded", "failed", "cancelled"]
TaskSort = Literal["created_at", "run_at", "last_attempt_at", "attempt_count"]
"""What tasks are listed by. `run_at` is when a task runs next: pending tasks run in that order."""
SortOrder = Literal["asc", "desc"]
HTTPMethod = Literal["GET", "POST", "PUT", "PATCH", "DELETE"]
AttemptOutcome = Literal["success", "http_error", "timeout", "network_error", "lost"]
RunState = Literal["pending", "running", "succeeded", "failed", "cancelled", "skipped"]
RunTrigger = Literal["schedule", "manual"]
SkipReason = Literal["overlap", "missed"]
OverlapPolicy = Literal["skip", "allow"]
MissedPolicy = Literal["skip", "catch_up"]
GroupStatus = Literal["open", "running", "succeeded", "failed"]
"""`open` accepts tasks; `running` is closed with tasks unfinished; the other two are final."""
WorkflowRunStatus = Literal["running", "succeeded", "failed", "cancelled"]
StepStatus = Literal["blocked", "pending", "running", "waiting", "succeeded", "failed", "skipped"]
"""`blocked` waits for its dependencies; `pending` has its task queued or retrying; `running` is
being delivered; `waiting` succeeded and waits for its `wait_for` group; `skipped` never ran,
or stopped waiting, because the run stopped."""


def _time(value: str) -> datetime:
    return datetime.fromisoformat(value)


def _list(value: Any) -> list[dict[str, Any]]:
    return value or []


def _opt_time(value: str | None) -> datetime | None:
    return None if value is None else _time(value)


@dataclass(frozen=True, slots=True)
class QueueStats:
    """A queue's current work. Counts stop at 10,000."""

    due: int
    scheduled: int
    running: int
    failed: int
    """Failed tasks still retained by the server."""
    oldest_due_at: datetime | None
    """When the longest-waiting due task became due; None when nothing is due."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> QueueStats:
        return cls(
            due=d["due"],
            scheduled=d["scheduled"],
            running=d["running"],
            failed=d.get("failed", 0),
            oldest_due_at=_opt_time(d.get("oldest_due_at")),
        )


@dataclass(frozen=True, slots=True)
class QueueActivity:
    """A queue's finished tasks since `since`, by final state. Counts stop at 10,000."""

    since: datetime
    succeeded: int
    failed: int
    cancelled: int
    retention_seconds: float | None
    """How long the server keeps finished tasks; None if forever. Older ones aren't counted."""
    p50_duration_ms: int | None = None
    """Median duration of the latest attempt of the succeeded and failed tasks counted."""
    p95_duration_ms: int | None = None
    """95th percentile of the same durations."""
    duration_samples: int | None = None
    """How many durations the percentiles are taken over; None from older servers."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> QueueActivity:
        return cls(
            since=_time(d["since"]),
            succeeded=d["succeeded"],
            failed=d["failed"],
            cancelled=d["cancelled"],
            retention_seconds=d.get("retention_seconds"),
            p50_duration_ms=d.get("p50_duration_ms"),
            p95_duration_ms=d.get("p95_duration_ms"),
            duration_samples=d.get("duration_samples"),
        )


@dataclass(frozen=True, slots=True)
class CronSpan:
    """`count` runs, `every_seconds` apart, the first at `start`."""

    start: datetime
    every_seconds: int
    """0 when `count` is 1."""
    count: int


@dataclass(frozen=True, slots=True)
class CronPreview:
    """When a cron timetable would run, from now."""

    next_runs: list[datetime]
    interval_seconds: int
    """The shortest time between two of its runs."""
    spans: list[CronSpan] | None = None
    """With a window: every run in it, earliest first, as runs at a fixed interval."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> CronPreview:
        spans = d.get("spans")
        return cls(
            next_runs=[_time(t) for t in d["next_runs"]],
            interval_seconds=d["interval_seconds"],
            spans=None
            if spans is None
            else [
                CronSpan(
                    start=_time(s["start"]), every_seconds=s["every_seconds"], count=s["count"]
                )
                for s in spans
            ],
        )


ActivityRange = Literal["1h", "24h", "7d"]


@dataclass(frozen=True, slots=True)
class ActivityBucket:
    """Tasks created from `start` until the next bucket, by queue, and tasks that finished in it."""

    start: datetime
    counts: dict[str, int]
    """Tasks created, by queue. Queues with none are left out."""
    succeeded: int = 0
    """Tasks of every queue that finished in the bucket and succeeded."""
    failed: int = 0
    """Tasks of every queue that finished in the bucket and failed."""


@dataclass(frozen=True, slots=True)
class Activity:
    """Tasks created and finished over time: every bucket of the range, oldest first."""

    range: ActivityRange
    bucket_seconds: int
    buckets: list[ActivityBucket]
    retention_seconds: float | None
    """How long the server keeps finished tasks; None if forever. Older ones aren't counted."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> Activity:
        return cls(
            range=d["range"],
            bucket_seconds=d["bucket_seconds"],
            buckets=[
                ActivityBucket(
                    start=_time(b["start"]),
                    counts=dict(b["counts"]),
                    succeeded=b.get("succeeded", 0),
                    failed=b.get("failed", 0),
                )
                for b in d["buckets"]
            ],
            retention_seconds=d.get("retention_seconds"),
        )


@dataclass(frozen=True, slots=True)
class FailureGroup:
    """Failed tasks in a queue whose latest attempt ended with the same error."""

    error: str
    """The tasks' `last_error`. Pass it as `last_error` to list or retry them."""
    count: int
    outcome: AttemptOutcome | None
    status_code: int | None
    """None if there was no response."""
    last_failed_at: datetime
    latest_task_id: str
    """The group's most recently failed task."""
    response_body: str | None
    """The start of the latest task's last response body, if it had one."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> FailureGroup:
        return cls(
            error=d["error"],
            count=d["count"],
            outcome=d.get("outcome"),
            status_code=d.get("status_code"),
            last_failed_at=_time(d["last_failed_at"]),
            latest_task_id=d["latest_task_id"],
            response_body=d.get("response_body"),
        )


@dataclass(frozen=True, slots=True)
class Queue:
    name: str
    paused: bool
    max_attempts: int
    min_backoff_seconds: float
    max_backoff_seconds: float
    backoff_multiplier: float
    timeout_seconds: float
    retry_on_4xx: bool
    rate_per_second: float | None
    max_concurrency: int
    stats: QueueStats | None
    created_at: datetime
    updated_at: datetime

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> Queue:
        stats = d.get("stats")
        return cls(
            name=d["name"],
            paused=d["paused"],
            max_attempts=d["max_attempts"],
            min_backoff_seconds=d["min_backoff_seconds"],
            max_backoff_seconds=d["max_backoff_seconds"],
            backoff_multiplier=d["backoff_multiplier"],
            timeout_seconds=d["timeout_seconds"],
            retry_on_4xx=d["retry_on_4xx"],
            rate_per_second=d.get("rate_per_second"),
            max_concurrency=d["max_concurrency"],
            stats=None if stats is None else QueueStats._from_json(stats),
            created_at=_time(d["created_at"]),
            updated_at=_time(d["updated_at"]),
        )


@dataclass(frozen=True, slots=True)
class Task:
    id: str
    queue: str
    state: TaskState
    method: HTTPMethod
    url: str
    headers: dict[str, str]
    payload: Any
    """The JSON body, decoded. None when the task has a raw `body` (or no body)."""
    body: str | None
    run_at: datetime
    """When the task is next eligible for delivery."""
    attempt_count: int
    max_attempts: int | None
    """Per-task override, if set."""
    timeout_seconds: float | None
    """Per-task override, if set."""
    idempotency_key: str | None
    last_status: int | None
    last_outcome: AttemptOutcome | None
    """How the latest attempt ended; None before the first attempt."""
    last_error: str | None
    created_at: datetime
    updated_at: datetime
    finished_at: datetime | None
    """When the task succeeded, failed or was cancelled."""
    last_attempt_at: datetime | None
    """When the latest attempt finished."""
    retried_at: datetime | None
    """When the task was last retried by hand."""
    group_id: str | None = None
    """The group the task belongs to, if any."""
    started_at: datetime | None = None
    """When the first delivery attempt started; minus `created_at`, the time spent waiting."""
    last_duration_ms: int | None = None
    """How long the latest finished attempt took."""
    on_complete: Callback | None = None
    """Called each time the task finishes."""
    callback_task_id: str | None = None
    """The callback task enqueued when the task last finished, if it has `on_complete`."""
    workflow_step: TaskWorkflowStep | None = None
    """The workflow step the task delivers, if it is one."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> Task:
        cb = d.get("on_complete")
        step = d.get("workflow_step")
        return cls(
            id=d["id"],
            queue=d["queue"],
            state=d["state"],
            method=d["method"],
            url=d["url"],
            headers=d.get("headers") or {},
            payload=d.get("payload"),
            body=d.get("body"),
            run_at=_time(d["run_at"]),
            attempt_count=d["attempt_count"],
            max_attempts=d.get("max_attempts"),
            timeout_seconds=d.get("timeout_seconds"),
            idempotency_key=d.get("idempotency_key"),
            last_status=d.get("last_status"),
            last_outcome=d.get("last_outcome"),
            last_error=d.get("last_error"),
            created_at=_time(d["created_at"]),
            updated_at=_time(d["updated_at"]),
            finished_at=_opt_time(d.get("finished_at")),
            last_attempt_at=_opt_time(d.get("last_attempt_at")),
            retried_at=_opt_time(d.get("retried_at")),
            group_id=d.get("group_id"),
            started_at=_opt_time(d.get("started_at")),
            last_duration_ms=d.get("last_duration_ms"),
            on_complete=None if cb is None else Callback._from_json(cb),
            callback_task_id=d.get("callback_task_id"),
            workflow_step=None if step is None else TaskWorkflowStep._from_json(step),
        )


@dataclass(frozen=True, slots=True)
class TaskWorkflowStep:
    """The workflow step a task delivers. Its task can't be retried alone: retry its run."""

    id: str
    """The step, `wfs_…`."""
    name: str
    workflow: str
    run_id: str
    """The workflow run, `wfr_…`."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> TaskWorkflowStep:
        return cls(id=d["id"], name=d["name"], workflow=d["workflow"], run_id=d["run_id"])


@dataclass(frozen=True, slots=True)
class Delivery:
    """The HTTP request that delivers a task or run, freshly signed."""

    method: HTTPMethod
    url: str
    headers: dict[str, str]
    """Every header Workly sets, including the task's own and `Workly-Signature`."""
    body: str
    """The body exactly as sent; the signature covers it."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> Delivery:
        return cls(method=d["method"], url=d["url"], headers=d.get("headers") or {}, body=d["body"])


@dataclass(frozen=True, slots=True)
class Attempt:
    id: str
    number: int
    outcome: AttemptOutcome
    """`lost` means the result is unknown and the task is delivered again."""
    status_code: int | None
    error: str | None
    response_body: str | None
    """The first 4 KB of the response body."""
    response_headers: dict[str, str]
    started_at: datetime
    finished_at: datetime
    duration_ms: int
    retried_at: datetime | None
    """Set when a manual retry came just before this attempt: when it happened."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> Attempt:
        return cls(
            id=d["id"],
            number=d["number"],
            outcome=d["outcome"],
            status_code=d.get("status_code"),
            error=d.get("error"),
            response_body=d.get("response_body"),
            response_headers=d.get("response_headers") or {},
            started_at=_time(d["started_at"]),
            finished_at=_time(d["finished_at"]),
            duration_ms=d["duration_ms"],
            retried_at=_opt_time(d.get("retried_at")),
        )


@dataclass(frozen=True, slots=True)
class TaskPage:
    tasks: list[Task]
    next_cursor: str | None
    """Pass as `cursor` to get the next page. None on the last page."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> TaskPage:
        return cls(
            tasks=[Task._from_json(t) for t in d["tasks"]],
            next_cursor=d.get("next_cursor"),
        )


@dataclass(frozen=True, slots=True)
class EnqueueResult:
    created: bool
    """False if a task with the same idempotency key already existed."""
    task: Task


@dataclass(frozen=True, slots=True)
class LastRun:
    """A schedule's most recent run."""

    id: str
    state: RunState
    trigger: RunTrigger
    scheduled_for: datetime
    finished_at: datetime | None
    last_status: int | None
    last_error: str | None

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> LastRun:
        return cls(
            id=d["id"],
            state=d["state"],
            trigger=d["trigger"],
            scheduled_for=_time(d["scheduled_for"]),
            finished_at=_opt_time(d.get("finished_at")),
            last_status=d.get("last_status"),
            last_error=d.get("last_error"),
        )


@dataclass(frozen=True, slots=True)
class Schedule:
    """A cron schedule that calls `url`. Separate from queues: its calls are runs."""

    name: str
    cron: str
    timezone: str
    method: HTTPMethod
    url: str
    headers: dict[str, str]
    payload: Any
    """The JSON body, decoded. None when the schedule has a raw `body` (or no body)."""
    body: str | None
    timeout_seconds: float
    max_attempts: int
    min_backoff_seconds: float
    max_backoff_seconds: float
    backoff_multiplier: float
    retry_on_4xx: bool
    overlap: OverlapPolicy
    missed: MissedPolicy
    paused: bool
    next_run_at: datetime | None
    """When the schedule next fires; None while paused."""
    next_runs: list[datetime]
    """The next five slots. Empty while paused; see `next_runs_if_resumed`."""
    last_run: LastRun | None
    created_at: datetime
    updated_at: datetime
    paused_at: datetime | None = None
    """When a paused schedule was paused; None while it runs."""
    next_runs_if_resumed: list[datetime] | None = None
    """While paused, the five slots it would run at if resumed now; None while it runs."""
    on_complete: Callback | None = None
    """Called each time a run finishes, except skipped runs."""
    workflow: str | None = None
    """The workflow each run starts, for a schedule that starts one instead of calling `url`."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> Schedule:
        last = d.get("last_run")
        cb = d.get("on_complete")
        next_runs: list[str] = d.get("next_runs") or []
        return cls(
            name=d["name"],
            cron=d["cron"],
            timezone=d["timezone"],
            method=d["method"],
            url=d["url"],
            headers=d.get("headers") or {},
            payload=d.get("payload"),
            body=d.get("body"),
            timeout_seconds=d["timeout_seconds"],
            max_attempts=d["max_attempts"],
            min_backoff_seconds=d["min_backoff_seconds"],
            max_backoff_seconds=d["max_backoff_seconds"],
            backoff_multiplier=d["backoff_multiplier"],
            retry_on_4xx=d["retry_on_4xx"],
            overlap=d["overlap"],
            missed=d["missed"],
            paused=d["paused"],
            next_run_at=_opt_time(d.get("next_run_at")),
            next_runs=[_time(t) for t in next_runs],
            last_run=None if last is None else LastRun._from_json(last),
            created_at=_time(d["created_at"]),
            updated_at=_time(d["updated_at"]),
            paused_at=_opt_time(d.get("paused_at")),
            next_runs_if_resumed=(
                None
                if (resumed := d.get("next_runs_if_resumed")) is None
                else [_time(t) for t in resumed]
            ),
            on_complete=None if cb is None else Callback._from_json(cb),
            workflow=d.get("workflow"),
        )


@dataclass(frozen=True, slots=True)
class Run:
    """One firing of a schedule, delivered and retried like a task."""

    id: str
    schedule: str
    state: RunState
    trigger: RunTrigger
    scheduled_for: datetime
    """The cron slot, or when a manual run was triggered."""
    skip_reason: SkipReason | None
    """Why a `skipped` run wasn't delivered."""
    method: HTTPMethod
    url: str
    headers: dict[str, str]
    payload: Any
    body: str | None
    run_at: datetime
    attempt_count: int
    last_status: int | None
    last_outcome: AttemptOutcome | None
    last_error: str | None
    created_at: datetime
    updated_at: datetime
    finished_at: datetime | None
    last_attempt_at: datetime | None
    retried_at: datetime | None
    started_at: datetime | None = None
    """When the first delivery attempt started."""
    last_duration_ms: int | None = None
    """How long the latest finished attempt took."""
    callback_task_id: str | None = None
    """The callback task enqueued when the run last finished, if its schedule has `on_complete`."""
    workflow_run_id: str | None = None
    """The workflow run it started, for a schedule that starts a workflow."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> Run:
        return cls(
            id=d["id"],
            schedule=d["schedule"],
            state=d["state"],
            trigger=d["trigger"],
            scheduled_for=_time(d["scheduled_for"]),
            skip_reason=d.get("skip_reason"),
            method=d["method"],
            url=d["url"],
            headers=d.get("headers") or {},
            payload=d.get("payload"),
            body=d.get("body"),
            run_at=_time(d["run_at"]),
            attempt_count=d["attempt_count"],
            last_status=d.get("last_status"),
            last_outcome=d.get("last_outcome"),
            last_error=d.get("last_error"),
            created_at=_time(d["created_at"]),
            updated_at=_time(d["updated_at"]),
            finished_at=_opt_time(d.get("finished_at")),
            last_attempt_at=_opt_time(d.get("last_attempt_at")),
            retried_at=_opt_time(d.get("retried_at")),
            started_at=_opt_time(d.get("started_at")),
            last_duration_ms=d.get("last_duration_ms"),
            callback_task_id=d.get("callback_task_id"),
            workflow_run_id=d.get("workflow_run_id"),
        )


@dataclass(frozen=True, slots=True)
class RunPage:
    runs: list[Run]
    next_cursor: str | None
    """Pass as `cursor` to get the next page. None on the last page."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> RunPage:
        return cls(runs=[Run._from_json(r) for r in d["runs"]], next_cursor=d.get("next_cursor"))


AlertChannelType = Literal["email", "slack"]


@dataclass(frozen=True, slots=True)
class AlertChannel:
    """Where the workspace's alerts about failed tasks and runs go."""

    id: str
    type: AlertChannelType
    target: str
    """The email address, or the Slack webhook URL with its secret part hidden."""
    created_at: datetime

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> AlertChannel:
        return cls(
            id=d["id"], type=d["type"], target=d["target"], created_at=_time(d["created_at"])
        )


@dataclass(frozen=True, slots=True)
class AlertChannelList:
    channels: list[AlertChannel]
    email_enabled: bool
    """Whether the server can send email, so email channels can be added."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> AlertChannelList:
        return cls(
            channels=[AlertChannel._from_json(c) for c in d["channels"]],
            email_enabled=d["email_enabled"],
        )


CALLBACK_QUEUE = "_callbacks"


@dataclass(frozen=True, slots=True, kw_only=True)
class Callback:
    """A task Workly enqueues when something completes: a POST to `url` saying what completed.

    Its JSON body is a `TaskSummary` for a task, a `RunSummary` for a
    schedule's run and a `GroupSummary` for a group. It is delivered as a task
    in the `_callbacks` queue, with that queue's retries: configure it like
    any queue to change them.
    """

    url: str
    headers: Mapping[str, str] = field(default_factory=dict[str, str])
    """Headers sent with the callback. `Workly-*` headers are reserved."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> Callback:
        return cls(url=d["url"], headers=d.get("headers") or {})


GroupCallback = Callback
"""Deprecated: use `Callback`."""


@dataclass(frozen=True, slots=True)
class Group:
    """A set of tasks tracked together, with a callback once all have finished."""

    id: str
    name: str | None
    status: GroupStatus
    idempotency_key: str | None
    total: int
    succeeded: int
    failed: int
    cancelled: int
    remaining: int
    """Tasks still pending or running."""
    on_complete: Callback | None
    callback_task_id: str | None
    """The callback task, once the group has completed."""
    created_at: datetime
    closed_at: datetime | None
    finished_at: datetime | None
    """When the group completed."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> Group:
        cb = d.get("on_complete")
        return cls(
            id=d["id"],
            name=d.get("name"),
            status=d["status"],
            idempotency_key=d.get("idempotency_key"),
            total=d["total"],
            succeeded=d["succeeded"],
            failed=d["failed"],
            cancelled=d["cancelled"],
            remaining=d["remaining"],
            on_complete=None if cb is None else Callback._from_json(cb),
            callback_task_id=d.get("callback_task_id"),
            created_at=_time(d["created_at"]),
            closed_at=_opt_time(d.get("closed_at")),
            finished_at=_opt_time(d.get("finished_at")),
        )


@dataclass(frozen=True, slots=True)
class GroupPage:
    groups: list[Group]
    next_cursor: str | None
    """Pass as `cursor` to get the next page. None on the last page."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> GroupPage:
        return cls(
            groups=[Group._from_json(g) for g in d["groups"]],
            next_cursor=d.get("next_cursor"),
        )


WORKFLOW_QUEUE = "_workflows"


@dataclass(frozen=True, slots=True)
class WorkflowStepConfig:
    """A step of a workflow's definition: the request its task sends."""

    name: str
    url: str
    depends_on: list[str]
    method: HTTPMethod
    queue: str
    headers: dict[str, str]
    payload: Any
    """The JSON body, decoded. None for a raw `body`, or none: then the run's context is sent."""
    body: str | None
    timeout_seconds: float | None
    """Overrides the queue's timeout."""
    max_attempts: int | None
    """Overrides the queue's max attempts."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> WorkflowStepConfig:
        return cls(
            name=d["name"],
            url=d["url"],
            depends_on=d.get("depends_on") or [],
            method=d.get("method") or "POST",
            queue=d.get("queue") or WORKFLOW_QUEUE,
            headers=d.get("headers") or {},
            payload=d.get("payload"),
            body=d.get("body"),
            timeout_seconds=d.get("timeout_seconds"),
            max_attempts=d.get("max_attempts"),
        )


@dataclass(frozen=True, slots=True)
class WorkflowRunSummary:
    """A workflow run without its steps, as lists show it."""

    id: str
    workflow: str
    version: int
    """The definition's version the run started with."""
    status: WorkflowRunStatus
    error: str | None
    """Why the run failed or was cancelled."""
    idempotency_key: str | None
    schedule_run_id: str | None
    """The schedule run that started it, if any."""
    created_at: datetime
    deadline_at: datetime | None
    """When a run still going fails (`max_duration_seconds`)."""
    finished_at: datetime | None
    duration_ms: int | None
    """From start to finish, or to now while running."""
    attempt: int = 1
    """1, plus one for each time the run was retried."""
    retried_at: datetime | None = None
    """When the run was last retried."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> WorkflowRunSummary:
        return cls(**_run_summary_fields(d))


def _run_summary_fields(d: dict[str, Any]) -> dict[str, Any]:
    return dict(
        id=d["id"],
        workflow=d["workflow"],
        version=d.get("version", 0),
        status=d["status"],
        error=d.get("error"),
        idempotency_key=d.get("idempotency_key"),
        schedule_run_id=d.get("schedule_run_id"),
        created_at=_time(d["created_at"]),
        deadline_at=_opt_time(d.get("deadline_at")),
        finished_at=_opt_time(d.get("finished_at")),
        duration_ms=d.get("duration_ms"),
        attempt=d.get("attempt", 1),
        retried_at=_opt_time(d.get("retried_at")),
    )


@dataclass(frozen=True, slots=True)
class Workflow:
    """A DAG of HTTP steps, each delivered as a task once its dependencies have succeeded."""

    name: str
    steps: list[WorkflowStepConfig]
    max_duration_seconds: float
    version: int
    """Counts changes to the definition, from 1."""
    last_run: WorkflowRunSummary | None
    created_at: datetime
    updated_at: datetime

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> Workflow:
        last = d.get("last_run")
        return cls(
            name=d["name"],
            steps=[WorkflowStepConfig._from_json(s) for s in d["steps"]],
            max_duration_seconds=d["max_duration_seconds"],
            version=d["version"],
            last_run=None if last is None else WorkflowRunSummary._from_json(last),
            created_at=_time(d["created_at"]),
            updated_at=_time(d["updated_at"]),
        )


@dataclass(frozen=True, slots=True)
class WaitFor:
    """The resource a step handed its completion to with `Workly-Wait-For`."""

    type: Literal["group"]
    id: str

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> WaitFor:
        return cls(type=d["type"], id=d["id"])


@dataclass(frozen=True, slots=True)
class WorkflowStepTask:
    """A step's task, which records its delivery attempts (`tasks.attempts`)."""

    id: str
    queue: str
    state: TaskState
    attempt_count: int
    last_status: int | None
    last_outcome: AttemptOutcome | None
    last_error: str | None
    run_at: datetime | None
    """When a pending task is next delivered."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> WorkflowStepTask:
        return cls(
            id=d["id"],
            queue=d["queue"],
            state=d["state"],
            attempt_count=d["attempt_count"],
            last_status=d.get("last_status"),
            last_outcome=d.get("last_outcome"),
            last_error=d.get("last_error"),
            run_at=_opt_time(d.get("run_at")),
        )


@dataclass(frozen=True, slots=True)
class WorkflowStep:
    """A step of a run: where it is, and why."""

    id: str
    """Stable across retries of its request: use it as an idempotency key."""
    name: str
    status: StepStatus
    depends_on: list[str]
    blocked_by: list[str]
    """Dependencies that haven't succeeded yet."""
    task_id: str | None
    task: WorkflowStepTask | None
    wait_for: WaitFor | None
    error: str | None
    created_at: datetime | None
    ready_at: datetime | None
    """When its task was enqueued."""
    http_done_at: datetime | None
    """When its request succeeded."""
    finished_at: datetime | None
    blocked_ms: int | None
    waiting_ms: int | None
    duration_ms: int | None

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> WorkflowStep:
        task, wait = d.get("task"), d.get("wait_for")
        return cls(
            id=d["id"],
            name=d["name"],
            status=d["status"],
            depends_on=d.get("depends_on") or [],
            blocked_by=d.get("blocked_by") or [],
            task_id=d.get("task_id"),
            task=None if task is None else WorkflowStepTask._from_json(task),
            wait_for=None if wait is None else WaitFor._from_json(wait),
            error=d.get("error"),
            created_at=_opt_time(d.get("created_at")),
            ready_at=_opt_time(d.get("ready_at")),
            http_done_at=_opt_time(d.get("http_done_at")),
            finished_at=_opt_time(d.get("finished_at")),
            blocked_ms=d.get("blocked_ms"),
            waiting_ms=d.get("waiting_ms"),
            duration_ms=d.get("duration_ms"),
        )


@dataclass(frozen=True, slots=True)
class WorkflowRun:
    """One execution of a workflow, under the definition it started with."""

    id: str
    workflow: str
    version: int
    status: WorkflowRunStatus
    error: str | None
    idempotency_key: str | None
    schedule_run_id: str | None
    created_at: datetime
    deadline_at: datetime | None
    finished_at: datetime | None
    duration_ms: int | None
    input: Any
    steps: list[WorkflowStep]
    """In definition order."""
    attempt: int = 1
    """1, plus one for each time the run was retried."""
    retried_at: datetime | None = None
    """When the run was last retried."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> WorkflowRun:
        return cls(
            **_run_summary_fields(d),
            input=d.get("input"),
            steps=[WorkflowStep._from_json(s) for s in _list(d.get("steps"))],
        )


@dataclass(frozen=True, slots=True)
class WorkflowRunPage:
    workflow_runs: list[WorkflowRunSummary]
    next_cursor: str | None
    """Pass as `cursor` to get the next page. None on the last page."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> WorkflowRunPage:
        return cls(
            workflow_runs=[WorkflowRunSummary._from_json(r) for r in d["workflow_runs"]],
            next_cursor=d.get("next_cursor"),
        )
