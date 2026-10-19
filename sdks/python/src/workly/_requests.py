"""Request building shared by the sync and async clients.

Each API operation is a function returning a `Request`; the clients only
send it and decode the response, so the two stay in step.
"""

from __future__ import annotations

import os
from collections.abc import Mapping
from dataclasses import dataclass, field
from datetime import datetime
from importlib.metadata import PackageNotFoundError, version
from typing import Any, Required, TypedDict
from urllib.parse import quote

from ._models import (
    CALLBACK_QUEUE,
    AlertChannelType,
    Callback,
    GroupStatus,
    HTTPMethod,
    RunState,
    SortOrder,
    TaskSort,
    TaskState,
)

DEFAULT_URL = "http://localhost:7337"

try:
    __version__ = version("workly")
except PackageNotFoundError:  # running from a source tree
    __version__ = "0.0.0"


@dataclass(frozen=True, slots=True)
class Request:
    method: str
    path: str
    params: dict[str, str] = field(default_factory=dict[str, str])
    json: Any = None
    headers: dict[str, str] = field(default_factory=dict[str, str])
    idempotent: bool = False
    """Safe to resend even if the server may have processed it."""


@dataclass(frozen=True, slots=True)
class ClientConfig:
    base_url: str
    headers: dict[str, str]


def client_config(url: str | None, api_key: str | None) -> ClientConfig:
    base_url = (url or os.environ.get("WORKLY_URL") or DEFAULT_URL).rstrip("/")
    api_key = api_key if api_key is not None else os.environ.get("WORKLY_API_KEY")
    headers = {"User-Agent": f"workly-python/{__version__}", "Accept": "application/json"}
    if api_key:
        headers["Authorization"] = f"Bearer {api_key}"
    return ClientConfig(base_url, headers)


class TaskSpec(TypedDict, total=False):
    """One task for `tasks.enqueue_many`. Same fields as `tasks.enqueue`."""

    url: Required[str]
    payload: Any
    body: str
    content_type: str
    method: HTTPMethod
    headers: Mapping[str, str]
    delay_seconds: float
    run_at: datetime
    idempotency_key: str
    max_attempts: int
    timeout_seconds: float
    group_id: str


class CallbackDict(TypedDict, total=False):
    """A `Callback` as a dict, with the same fields."""

    url: Required[str]
    queue: str
    headers: Mapping[str, str]


GroupCallbackSpec = CallbackDict
"""Deprecated: use `Callback` or `CallbackDict`."""


def callback_body(cb: Callback | CallbackDict) -> dict[str, Any]:
    if isinstance(cb, Callback):
        url, queue, headers = cb.url, cb.queue, cb.headers
    else:
        url, queue, headers = cb["url"], cb.get("queue", CALLBACK_QUEUE), cb.get("headers", {})
    body: dict[str, Any] = {"url": url}
    if queue != CALLBACK_QUEUE:
        body["queue"] = queue
    if headers:
        body["headers"] = dict(headers)
    return body


def task_body(spec: Mapping[str, Any]) -> dict[str, Any]:
    body: dict[str, Any] = {}
    for key, value in spec.items():
        if value is None:
            continue
        if key == "run_at":
            value = _timestamp(value, "run_at")
        elif key == "headers":
            value = dict(value)
        body[key] = value
    if "payload" in body and "body" in body:
        raise ValueError("pass payload or body, not both")
    if "delay_seconds" in body and "run_at" in body:
        raise ValueError("pass delay_seconds or run_at, not both")
    return body


def _timestamp(value: datetime, name: str) -> str:
    if value.tzinfo is None:
        raise ValueError(f"{name} must be timezone-aware, e.g. datetime.now(timezone.utc)")
    return value.isoformat()


def _q(segment: str) -> str:
    return quote(segment, safe="")


def _traceparent(traceparent: str | None) -> dict[str, str]:
    return {"traceparent": traceparent} if traceparent else {}


# Queues


def list_queues() -> Request:
    return Request("GET", "/v1/queues", idempotent=True)


def get_queue(name: str) -> Request:
    return Request("GET", f"/v1/queues/{_q(name)}", idempotent=True)


def put_queue(name: str, config: dict[str, Any]) -> Request:
    body = {k: v for k, v in config.items() if v is not None}
    return Request("PUT", f"/v1/queues/{_q(name)}", json=body, idempotent=True)


def delete_queue(name: str) -> Request:
    return Request("DELETE", f"/v1/queues/{_q(name)}", idempotent=True)


def pause_queue(name: str) -> Request:
    return Request("POST", f"/v1/queues/{_q(name)}/pause", idempotent=True)


def resume_queue(name: str) -> Request:
    return Request("POST", f"/v1/queues/{_q(name)}/resume", idempotent=True)


def cancel_pending_tasks(name: str) -> Request:
    return Request("POST", f"/v1/queues/{_q(name)}/cancel-pending", idempotent=True)


def retry_failed_tasks(name: str, last_error: str | None) -> Request:
    params = {} if last_error is None else {"last_error": last_error}
    return Request("POST", f"/v1/queues/{_q(name)}/retry-failed", params=params, idempotent=True)


def get_queue_failures(name: str, limit: int | None) -> Request:
    params = {} if limit is None else {"limit": str(limit)}
    return Request("GET", f"/v1/queues/{_q(name)}/failures", params=params, idempotent=True)


def get_queue_activity(name: str, since: datetime | None) -> Request:
    params = {} if since is None else {"since": _timestamp(since, "since")}
    return Request("GET", f"/v1/queues/{_q(name)}/activity", params=params, idempotent=True)


def get_activity(range: str | None) -> Request:
    params = {} if range is None else {"range": range}
    return Request("GET", "/v1/activity", params=params, idempotent=True)


# Tasks


def enqueue(queue: str, spec: Mapping[str, Any], traceparent: str | None) -> Request:
    body = task_body(spec)
    return Request(
        "POST",
        f"/v1/queues/{_q(queue)}/tasks",
        json=body,
        headers=_traceparent(traceparent),
        idempotent="idempotency_key" in body,
    )


def enqueue_many(queue: str, specs: list[Mapping[str, Any]], traceparent: str | None) -> Request:
    tasks = [task_body(s) for s in specs]
    return Request(
        "POST",
        f"/v1/queues/{_q(queue)}/tasks:batch",
        json={"tasks": tasks},
        headers=_traceparent(traceparent),
        idempotent=all("idempotency_key" in t for t in tasks),
    )


def search_tasks(
    *,
    queue: str | None,
    state: TaskState | None,
    group_id: str | None,
    idempotency_key: str | None,
    last_error: str | None,
    created_after: datetime | None,
    created_before: datetime | None,
    sort: TaskSort | None,
    order: SortOrder | None,
    cursor: str | None,
    limit: int | None,
) -> Request:
    params: dict[str, str] = {}
    if queue is not None:
        params["queue"] = queue
    if state is not None:
        params["state"] = state
    if group_id is not None:
        params["group_id"] = group_id
    if idempotency_key is not None:
        params["idempotency_key"] = idempotency_key
    if last_error is not None:
        params["last_error"] = last_error
    if created_after is not None:
        params["created_after"] = _timestamp(created_after, "created_after")
    if created_before is not None:
        params["created_before"] = _timestamp(created_before, "created_before")
    if sort is not None:
        params["sort"] = sort
    if order is not None:
        params["order"] = order
    if cursor is not None:
        params["cursor"] = cursor
    if limit is not None:
        params["limit"] = str(limit)
    return Request("GET", "/v1/tasks", params=params, idempotent=True)


def get_task(task_id: str) -> Request:
    return Request("GET", f"/v1/tasks/{_q(task_id)}", idempotent=True)


def list_attempts(task_id: str) -> Request:
    return Request("GET", f"/v1/tasks/{_q(task_id)}/attempts", idempotent=True)


def get_task_delivery(task_id: str) -> Request:
    return Request("GET", f"/v1/tasks/{_q(task_id)}/delivery", idempotent=True)


def cancel_task(task_id: str) -> Request:
    return Request("POST", f"/v1/tasks/{_q(task_id)}/cancel", idempotent=True)


def retry_task(task_id: str) -> Request:
    # Not idempotent: resending could replay a task that already finished again.
    return Request("POST", f"/v1/tasks/{_q(task_id)}/retry")


# Groups


def create_group(
    name: str | None, idempotency_key: str | None, on_complete: Callback | CallbackDict | None
) -> Request:
    body: dict[str, Any] = {}
    if name is not None:
        body["name"] = name
    if idempotency_key is not None:
        body["idempotency_key"] = idempotency_key
    if on_complete is not None:
        body["on_complete"] = callback_body(on_complete)
    return Request("POST", "/v1/groups", json=body, idempotent=idempotency_key is not None)


def get_group(group_id: str) -> Request:
    return Request("GET", f"/v1/groups/{_q(group_id)}", idempotent=True)


def list_groups(
    *, status: GroupStatus | None, name: str | None, cursor: str | None, limit: int | None
) -> Request:
    params: dict[str, str] = {}
    if status is not None:
        params["status"] = status
    if name is not None:
        params["name"] = name
    if cursor is not None:
        params["cursor"] = cursor
    if limit is not None:
        params["limit"] = str(limit)
    return Request("GET", "/v1/groups", params=params, idempotent=True)


def close_group(group_id: str) -> Request:
    return Request("POST", f"/v1/groups/{_q(group_id)}/close", idempotent=True)


# Schedules and runs


def list_schedules() -> Request:
    return Request("GET", "/v1/schedules", idempotent=True)


def get_schedule(name: str) -> Request:
    return Request("GET", f"/v1/schedules/{_q(name)}", idempotent=True)


def put_schedule(name: str, config: Mapping[str, Any]) -> Request:
    body: dict[str, Any] = {}
    for key, value in config.items():
        if value is None:
            continue
        body[key] = dict(value) if key == "headers" else value
    if "payload" in body and "body" in body:
        raise ValueError("pass payload or body, not both")
    return Request("PUT", f"/v1/schedules/{_q(name)}", json=body, idempotent=True)


def delete_schedule(name: str) -> Request:
    return Request("DELETE", f"/v1/schedules/{_q(name)}", idempotent=True)


def pause_schedule(name: str) -> Request:
    return Request("POST", f"/v1/schedules/{_q(name)}/pause", idempotent=True)


def resume_schedule(name: str) -> Request:
    return Request("POST", f"/v1/schedules/{_q(name)}/resume", idempotent=True)


def preview_cron(cron: str, timezone: str | None) -> Request:
    params = {"cron": cron} if timezone is None else {"cron": cron, "timezone": timezone}
    return Request("GET", "/v1/cron/preview", params=params, idempotent=True)


def trigger_schedule(name: str) -> Request:
    # Not idempotent: resending would create a second run.
    return Request("POST", f"/v1/schedules/{_q(name)}/trigger")


def list_runs(
    name: str, *, state: RunState | None, cursor: str | None, limit: int | None
) -> Request:
    params: dict[str, str] = {}
    if state is not None:
        params["state"] = state
    if cursor is not None:
        params["cursor"] = cursor
    if limit is not None:
        params["limit"] = str(limit)
    return Request("GET", f"/v1/schedules/{_q(name)}/runs", params=params, idempotent=True)


def get_run(run_id: str) -> Request:
    return Request("GET", f"/v1/runs/{_q(run_id)}", idempotent=True)


def list_run_attempts(run_id: str) -> Request:
    return Request("GET", f"/v1/runs/{_q(run_id)}/attempts", idempotent=True)


def get_run_delivery(run_id: str) -> Request:
    return Request("GET", f"/v1/runs/{_q(run_id)}/delivery", idempotent=True)


def cancel_run(run_id: str) -> Request:
    return Request("POST", f"/v1/runs/{_q(run_id)}/cancel", idempotent=True)


def retry_run(run_id: str) -> Request:
    # Not idempotent, like retry_task.
    return Request("POST", f"/v1/runs/{_q(run_id)}/retry")


def list_alert_channels() -> Request:
    return Request("GET", "/v1/alert-channels", idempotent=True)


def create_alert_channel(type: AlertChannelType, target: str) -> Request:
    # Not idempotent: a resend after it was added gets alert_channel_exists.
    return Request("POST", "/v1/alert-channels", json={"type": type, "target": target})


def delete_alert_channel(channel_id: str) -> Request:
    return Request("DELETE", f"/v1/alert-channels/{_q(channel_id)}", idempotent=True)


def test_alert_channel(channel_id: str) -> Request:
    # Not idempotent: a resend sends a second test message.
    return Request("POST", f"/v1/alert-channels/{_q(channel_id)}/test")
