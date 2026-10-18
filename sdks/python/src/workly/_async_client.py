from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator, Iterable, Mapping
from datetime import datetime
from types import TracebackType
from typing import Any, Self

import httpx

from . import _requests as r
from . import _transport as t
from ._models import (
    Activity,
    ActivityRange,
    AlertChannel,
    AlertChannelList,
    AlertChannelType,
    Attempt,
    CronPreview,
    Delivery,
    EnqueueResult,
    FailureGroup,
    HTTPMethod,
    MissedPolicy,
    OverlapPolicy,
    Queue,
    QueueActivity,
    Run,
    RunPage,
    RunState,
    Schedule,
    SortOrder,
    Task,
    TaskPage,
    TaskSort,
    TaskState,
)


class AsyncWorkly:
    """Async client for the Workly API.

    `url` defaults to `WORKLY_URL`, then http://localhost:7337 (`workly dev`).
    `api_key` defaults to `WORKLY_API_KEY`; `workly dev` needs none.

    Use it as a context manager, or call `aclose()`, to release connections.
    """

    def __init__(
        self,
        url: str | None = None,
        api_key: str | None = None,
        *,
        timeout: float = 30.0,
        max_retries: int = 2,
        http_client: httpx.AsyncClient | None = None,
    ) -> None:
        self._config = r.client_config(url, api_key)
        self._http = http_client or httpx.AsyncClient(timeout=timeout)
        self._owns_http = http_client is None
        self._max_retries = max_retries
        self.queues = AsyncQueues(self)
        self.tasks = AsyncTasks(self)
        self.schedules = AsyncSchedules(self)
        self.runs = AsyncRuns(self)
        self.alerts = AsyncAlerts(self)

    @property
    def url(self) -> str:
        return self._config.base_url

    async def aclose(self) -> None:
        if self._owns_http:
            await self._http.aclose()

    async def __aenter__(self) -> Self:
        return self

    async def __aexit__(
        self,
        exc_type: type[BaseException] | None,
        exc: BaseException | None,
        tb: TracebackType | None,
    ) -> None:
        await self.aclose()

    async def _send(self, req: r.Request) -> Any:
        attempt = 0
        while True:
            try:
                response = await self._http.request(
                    req.method,
                    self._config.base_url + req.path,
                    params=req.params,
                    json=req.json,
                    headers={**self._config.headers, **req.headers},
                )
            except httpx.TransportError as exc:
                if attempt < self._max_retries and t.retry_after_error(req, exc):
                    await asyncio.sleep(t.delay(attempt))
                    attempt += 1
                    continue
                raise t.connection_error(req, exc) from exc
            if attempt < self._max_retries and t.retry_after_status(req, response):
                await asyncio.sleep(t.delay(attempt, response))
                attempt += 1
                continue
            return t.decode(response)


class AsyncQueues:
    def __init__(self, client: AsyncWorkly) -> None:
        self._client = client

    async def list(self) -> list[Queue]:
        """All queues, by name, with their stats."""
        data = await self._client._send(r.list_queues())
        return [Queue._from_json(q) for q in data["queues"]]

    async def get(self, name: str) -> Queue:
        return Queue._from_json(await self._client._send(r.get_queue(name)))

    async def put(
        self,
        name: str,
        *,
        max_attempts: int | None = None,
        min_backoff_seconds: float | None = None,
        max_backoff_seconds: float | None = None,
        backoff_multiplier: float | None = None,
        timeout_seconds: float | None = None,
        retry_on_4xx: bool | None = None,
        rate_per_second: float | None = None,
        max_concurrency: int | None = None,
    ) -> Queue:
        """Create or update a queue.

        Declarative: the queue ends up with exactly this configuration, and
        omitted settings are reset to their defaults. It doesn't change
        whether the queue is paused.
        """
        config = dict(
            max_attempts=max_attempts,
            min_backoff_seconds=min_backoff_seconds,
            max_backoff_seconds=max_backoff_seconds,
            backoff_multiplier=backoff_multiplier,
            timeout_seconds=timeout_seconds,
            retry_on_4xx=retry_on_4xx,
            rate_per_second=rate_per_second,
            max_concurrency=max_concurrency,
        )
        return Queue._from_json(await self._client._send(r.put_queue(name, config)))

    async def delete(self, name: str) -> None:
        """Delete a queue and all its tasks."""
        await self._client._send(r.delete_queue(name))

    async def pause(self, name: str) -> Queue:
        """Stop dispatching. In-flight deliveries finish; enqueueing still works."""
        return Queue._from_json(await self._client._send(r.pause_queue(name)))

    async def resume(self, name: str) -> Queue:
        return Queue._from_json(await self._client._send(r.resume_queue(name)))

    async def cancel_pending(self, name: str) -> int:
        """Cancel the queue's pending tasks, including scheduled ones. Returns how many.

        Running tasks finish. Cancelled tasks stay in the history and can be retried.
        """
        data = await self._client._send(r.cancel_pending_tasks(name))
        return int(data["cancelled"])

    async def failures(self, name: str, *, limit: int | None = None) -> list[FailureGroup]:
        """The queue's failed tasks grouped by error, most common first (default: 10 groups)."""
        data = await self._client._send(r.get_queue_failures(name, limit))
        return [FailureGroup._from_json(g) for g in data["groups"]]

    async def activity(self, name: str, *, since: datetime | None = None) -> QueueActivity:
        """Count the queue's tasks that finished since `since` (default: 7 days ago), by state."""
        return QueueActivity._from_json(await self._client._send(r.get_queue_activity(name, since)))

    async def retry_failed(self, name: str, *, last_error: str | None = None) -> int:
        """Retry the failed tasks in the queue now, e.g. after an outage.

        Each task gets a fresh set of attempts. With `last_error`, only tasks
        that failed with exactly that error (a group from `failures`) are
        retried. Returns how many were retried.
        """
        data = await self._client._send(r.retry_failed_tasks(name, last_error))
        return int(data["retried"])


class AsyncTasks:
    def __init__(self, client: AsyncWorkly) -> None:
        self._client = client

    async def enqueue(
        self,
        queue: str,
        *,
        url: str,
        payload: Any = None,
        body: str | None = None,
        content_type: str | None = None,
        method: HTTPMethod | None = None,
        headers: Mapping[str, str] | None = None,
        delay_seconds: float | None = None,
        run_at: datetime | None = None,
        idempotency_key: str | None = None,
        max_attempts: int | None = None,
        timeout_seconds: float | None = None,
        traceparent: str | None = None,
    ) -> Task:
        """Enqueue a task to deliver to `url`.

        `payload` is sent as JSON; `body` as raw text (use one). The queue is
        created with default settings if it doesn't exist. If
        `idempotency_key` matches a task already in the queue, that task is
        returned and nothing is enqueued.
        """
        spec = {
            "url": url,
            "payload": payload,
            "body": body,
            "content_type": content_type,
            "method": method,
            "headers": headers,
            "delay_seconds": delay_seconds,
            "run_at": run_at,
            "idempotency_key": idempotency_key,
            "max_attempts": max_attempts,
            "timeout_seconds": timeout_seconds,
        }
        return Task._from_json(await self._client._send(r.enqueue(queue, spec, traceparent)))

    async def enqueue_many(
        self, queue: str, tasks: Iterable[r.TaskSpec], *, traceparent: str | None = None
    ) -> list[EnqueueResult]:
        """Enqueue up to 100 tasks at once: all of them, or none if any is invalid."""
        data = await self._client._send(r.enqueue_many(queue, list(tasks), traceparent))
        return [
            EnqueueResult(created=res["created"], task=Task._from_json(res["task"]))
            for res in data["results"]
        ]

    async def get(self, task_id: str) -> Task:
        return Task._from_json(await self._client._send(r.get_task(task_id)))

    async def attempts(self, task_id: str) -> list[Attempt]:
        """The task's delivery attempts, oldest first."""
        data = await self._client._send(r.list_attempts(task_id))
        return [Attempt._from_json(a) for a in data["attempts"]]

    async def delivery(self, task_id: str) -> Delivery:
        """The request that delivers the task, as it would be sent now and freshly signed.

        Nothing is sent. Useful to replay a delivery by hand while fixing an endpoint.
        """
        return Delivery._from_json(await self._client._send(r.get_task_delivery(task_id)))

    async def cancel(self, task_id: str) -> Task:
        """Cancel a pending or running task. A running attempt finishes but isn't retried."""
        return Task._from_json(await self._client._send(r.cancel_task(task_id)))

    async def retry(self, task_id: str) -> Task:
        """Make the task due now. A finished task is replayed with fresh attempts."""
        return Task._from_json(await self._client._send(r.retry_task(task_id)))

    async def list(
        self,
        *,
        queue: str | None = None,
        state: TaskState | None = None,
        idempotency_key: str | None = None,
        last_error: str | None = None,
        created_after: datetime | None = None,
        created_before: datetime | None = None,
        sort: TaskSort | None = None,
        order: SortOrder | None = None,
        page_size: int | None = None,
    ) -> AsyncIterator[Task]:
        """Tasks matching every filter given, fetching pages as needed.

        Newest first, unless `sort` and `order` say otherwise: `order` defaults
        to "asc" (soonest first) for `sort="run_at"` and to "desc" for the rest.
        List pending tasks by `run_at` to see them in the order they run.
        """
        cursor: str | None = None
        while True:
            page = await self.list_page(
                queue=queue,
                state=state,
                idempotency_key=idempotency_key,
                last_error=last_error,
                created_after=created_after,
                created_before=created_before,
                sort=sort,
                order=order,
                cursor=cursor,
                limit=page_size,
            )
            for task in page.tasks:
                yield task
            if page.next_cursor is None:
                return
            cursor = page.next_cursor

    async def list_page(
        self,
        *,
        queue: str | None = None,
        state: TaskState | None = None,
        idempotency_key: str | None = None,
        last_error: str | None = None,
        created_after: datetime | None = None,
        created_before: datetime | None = None,
        sort: TaskSort | None = None,
        order: SortOrder | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> TaskPage:
        """One page of `list`. Pass the previous page's `next_cursor` as `cursor`."""
        req = r.search_tasks(
            queue=queue,
            state=state,
            idempotency_key=idempotency_key,
            last_error=last_error,
            created_after=created_after,
            created_before=created_before,
            sort=sort,
            order=order,
            cursor=cursor,
            limit=limit,
        )
        return TaskPage._from_json(await self._client._send(req))

    async def activity(self, *, range: ActivityRange | None = None) -> Activity:
        """Count the workspace's tasks created over time, by queue (default range: 24h).

        Buckets are 5 minutes for 1h, 1 hour for 24h and 6 hours for 7d, aligned to UTC.
        """
        return Activity._from_json(await self._client._send(r.get_activity(range)))


class AsyncSchedules:
    """Cron schedules that call an endpoint. Separate from queues: each call is a run."""

    def __init__(self, client: AsyncWorkly) -> None:
        self._client = client

    async def list(self) -> list[Schedule]:
        """All schedules, by name, with their next and last run."""
        data = await self._client._send(r.list_schedules())
        return [Schedule._from_json(s) for s in data["schedules"]]

    async def get(self, name: str) -> Schedule:
        return Schedule._from_json(await self._client._send(r.get_schedule(name)))

    async def put(
        self,
        name: str,
        *,
        cron: str,
        url: str,
        timezone: str | None = None,
        payload: Any = None,
        body: str | None = None,
        content_type: str | None = None,
        method: HTTPMethod | None = None,
        headers: Mapping[str, str] | None = None,
        timeout_seconds: float | None = None,
        max_attempts: int | None = None,
        min_backoff_seconds: float | None = None,
        max_backoff_seconds: float | None = None,
        backoff_multiplier: float | None = None,
        retry_on_4xx: bool | None = None,
        overlap: OverlapPolicy | None = None,
        missed: MissedPolicy | None = None,
    ) -> Schedule:
        """Create or update a schedule that calls `url` on `cron` (read in `timezone`, default UTC).

        Declarative: the schedule ends up with exactly these settings, and
        omitted ones are reset to their defaults. It doesn't change whether
        the schedule is paused. `next_runs` on the result shows when it runs.
        """
        config = dict(
            cron=cron,
            url=url,
            timezone=timezone,
            payload=payload,
            body=body,
            content_type=content_type,
            method=method,
            headers=headers,
            timeout_seconds=timeout_seconds,
            max_attempts=max_attempts,
            min_backoff_seconds=min_backoff_seconds,
            max_backoff_seconds=max_backoff_seconds,
            backoff_multiplier=backoff_multiplier,
            retry_on_4xx=retry_on_4xx,
            overlap=overlap,
            missed=missed,
        )
        return Schedule._from_json(await self._client._send(r.put_schedule(name, config)))

    async def delete(self, name: str) -> None:
        """Delete a schedule and all its runs."""
        await self._client._send(r.delete_schedule(name))

    async def pause(self, name: str) -> Schedule:
        """Stop the schedule from firing. Runs in flight finish; `trigger` still works."""
        return Schedule._from_json(await self._client._send(r.pause_schedule(name)))

    async def resume(self, name: str) -> Schedule:
        """Fire again from the next slot. Slots that passed while paused aren't run."""
        return Schedule._from_json(await self._client._send(r.resume_schedule(name)))

    async def preview_cron(self, cron: str, *, timezone: str | None = None) -> CronPreview:
        """The next 5 times a schedule with this cron would run, without creating it.

        `timezone` is an IANA name; the default is UTC.
        """
        return CronPreview._from_json(await self._client._send(r.preview_cron(cron, timezone)))

    async def trigger(self, name: str) -> Run:
        """Run the schedule now, even if paused. Doesn't change when it next fires."""
        return Run._from_json(await self._client._send(r.trigger_schedule(name)))

    async def runs(
        self, name: str, *, state: RunState | None = None, page_size: int | None = None
    ) -> AsyncIterator[Run]:
        """The schedule's runs, newest first, fetching pages as needed."""
        cursor: str | None = None
        while True:
            page = await self.runs_page(name, state=state, cursor=cursor, limit=page_size)
            for run in page.runs:
                yield run
            if page.next_cursor is None:
                return
            cursor = page.next_cursor

    async def runs_page(
        self,
        name: str,
        *,
        state: RunState | None = None,
        cursor: str | None = None,
        limit: int | None = None,
    ) -> RunPage:
        """One page of `runs`. Pass the previous page's `next_cursor` as `cursor`."""
        req = r.list_runs(name, state=state, cursor=cursor, limit=limit)
        return RunPage._from_json(await self._client._send(req))


class AsyncRuns:
    """Runs of schedules: one per firing, with its own attempts."""

    def __init__(self, client: AsyncWorkly) -> None:
        self._client = client

    async def get(self, run_id: str) -> Run:
        return Run._from_json(await self._client._send(r.get_run(run_id)))

    async def attempts(self, run_id: str) -> list[Attempt]:
        """The run's delivery attempts, oldest first."""
        data = await self._client._send(r.list_run_attempts(run_id))
        return [Attempt._from_json(a) for a in data["attempts"]]

    async def delivery(self, run_id: str) -> Delivery:
        """The request that delivers the run, as it would be sent now and freshly signed."""
        return Delivery._from_json(await self._client._send(r.get_run_delivery(run_id)))

    async def cancel(self, run_id: str) -> Run:
        """Cancel a pending or running run. A running attempt finishes but isn't retried."""
        return Run._from_json(await self._client._send(r.cancel_run(run_id)))

    async def retry(self, run_id: str) -> Run:
        """Deliver the run now. A finished or skipped run gets fresh attempts."""
        return Run._from_json(await self._client._send(r.retry_run(run_id)))


class AsyncAlerts:
    """Where alerts about failed tasks and schedule runs go: email or Slack.

    When tasks in a queue or runs of a schedule fail, every channel gets an
    alert within about a minute, then at most one every 30 minutes per queue
    or schedule.
    """

    def __init__(self, client: AsyncWorkly) -> None:
        self._client = client

    async def list(self) -> AlertChannelList:
        """The workspace's channels, oldest first, and whether email can be sent."""
        return AlertChannelList._from_json(await self._client._send(r.list_alert_channels()))

    async def create(self, type: AlertChannelType, target: str) -> AlertChannel:
        """Add a channel: an email address, or a Slack incoming webhook URL."""
        return AlertChannel._from_json(
            await self._client._send(r.create_alert_channel(type, target))
        )

    async def delete(self, channel_id: str) -> None:
        await self._client._send(r.delete_alert_channel(channel_id))

    async def test(self, channel_id: str) -> None:
        """Send a test alert to the channel now. Raises APIError (alert_failed) if it can't."""
        await self._client._send(r.test_alert_channel(channel_id))
