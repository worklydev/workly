"""Response types. They mirror the schemas in api/openapi.yaml.

Unknown fields are ignored, so newer servers don't break older SDKs.
"""

from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from typing import Any, Literal

TaskState = Literal["pending", "running", "succeeded", "failed", "cancelled"]
HTTPMethod = Literal["GET", "POST", "PUT", "PATCH", "DELETE"]
AttemptOutcome = Literal["success", "http_error", "timeout", "network_error", "lost"]
RunState = Literal["pending", "running", "succeeded", "failed", "cancelled", "skipped"]
RunTrigger = Literal["schedule", "manual"]
SkipReason = Literal["overlap", "missed"]
OverlapPolicy = Literal["skip", "allow"]
MissedPolicy = Literal["skip", "catch_up"]


def _time(value: str) -> datetime:
    return datetime.fromisoformat(value)


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

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> QueueActivity:
        return cls(
            since=_time(d["since"]),
            succeeded=d["succeeded"],
            failed=d["failed"],
            cancelled=d["cancelled"],
            retention_seconds=d.get("retention_seconds"),
        )


@dataclass(frozen=True, slots=True)
class CronPreview:
    """When a cron timetable would run, from now."""

    next_runs: list[datetime]
    interval_seconds: int
    """The shortest time between two of its runs."""

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> CronPreview:
        return cls(
            next_runs=[_time(t) for t in d["next_runs"]], interval_seconds=d["interval_seconds"]
        )


ActivityRange = Literal["1h", "24h", "7d"]


@dataclass(frozen=True, slots=True)
class ActivityBucket:
    """Tasks created from `start` until the next bucket, by queue."""

    start: datetime
    counts: dict[str, int]
    """Queues with none are left out."""


@dataclass(frozen=True, slots=True)
class Activity:
    """The workspace's tasks created over time: every bucket of the range, oldest first."""

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
                ActivityBucket(start=_time(b["start"]), counts=dict(b["counts"]))
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

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> Task:
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
        )


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
    """The next five slots (as if resumed now, while paused)."""
    last_run: LastRun | None
    created_at: datetime
    updated_at: datetime

    @classmethod
    def _from_json(cls, d: dict[str, Any]) -> Schedule:
        last = d.get("last_run")
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
