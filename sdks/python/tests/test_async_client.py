"""The async client shares request building with the sync one; these check the async plumbing."""

from typing import Any

import httpx
import pytest

import workly
from workly import _transport

from .test_client import TASK

pytestmark = pytest.mark.anyio


@pytest.fixture
def anyio_backend() -> str:
    return "asyncio"


@pytest.fixture(autouse=True)
def no_sleep(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(_transport, "delay", lambda *_: 0.0)  # pyright: ignore


def client(handler: Any) -> workly.AsyncWorkly:
    return workly.AsyncWorkly(http_client=httpx.AsyncClient(transport=httpx.MockTransport(handler)))


async def test_enqueue_and_retry() -> None:
    calls: list[httpx.Request] = []

    def handler(req: httpx.Request) -> httpx.Response:
        calls.append(req)
        if req.method == "GET" and len(calls) == 2:
            return httpx.Response(503)
        return httpx.Response(200, json=TASK)

    async with client(handler) as c:
        task = await c.tasks.enqueue("emails", url="https://x", payload={"a": 1})
        assert task.id == "task_1"
        await c.tasks.get("task_1")
    assert [r.method for r in calls] == ["POST", "GET", "GET"]


async def test_list_paginates() -> None:
    pages = {None: {"tasks": [TASK], "next_cursor": "c1"}, "c1": {"tasks": [TASK]}}

    def handler(req: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=pages[req.url.params.get("cursor")])

    tasks = [t async for t in client(handler).tasks.list(queue="emails")]
    assert len(tasks) == 2


async def test_errors() -> None:
    def handler(req: httpx.Request) -> httpx.Response:
        return httpx.Response(404, json={"error": {"code": "queue_not_found", "message": "no"}})

    with pytest.raises(workly.NotFoundError):
        await client(handler).queues.get("nope")


async def test_queue_methods() -> None:
    queue = {
        "name": "emails",
        "paused": True,
        "max_attempts": 10,
        "min_backoff_seconds": 1,
        "max_backoff_seconds": 3600,
        "backoff_multiplier": 2,
        "timeout_seconds": 30,
        "retry_on_4xx": False,
        "max_concurrency": 3,
        "created_at": "2026-09-27T10:00:00Z",
        "updated_at": "2026-09-27T10:00:00Z",
    }

    def handler(req: httpx.Request) -> httpx.Response:
        match req.method, req.url.path:
            case "POST", "/v1/queues/emails/cancel-pending":
                return httpx.Response(200, json={"cancelled": 2})
            case "GET", "/v1/queues/emails/failures":
                return httpx.Response(200, json={"groups": []})
            case "POST", "/v1/queues/emails/retry-failed":
                return httpx.Response(200, json={"retried": 1})
            case "DELETE", _:
                return httpx.Response(204)
            case "GET", "/v1/queues":
                return httpx.Response(200, json={"queues": [queue]})
            case _:
                return httpx.Response(200, json=queue)

    c = client(handler)
    assert (await c.queues.put("emails", max_attempts=10)).name == "emails"
    assert (await c.queues.pause("emails")).paused
    assert (await c.queues.resume("emails")).name == "emails"
    assert [q.name for q in await c.queues.list()] == ["emails"]
    assert await c.queues.cancel_pending("emails") == 2
    assert await c.queues.failures("emails") == []
    assert await c.queues.retry_failed("emails") == 1
    assert await c.queues.delete("emails") is None


async def test_schedules() -> None:
    from .test_client import RUN, SCHEDULE

    def handler(req: httpx.Request) -> httpx.Response:
        if req.url.path.endswith("/runs"):
            return httpx.Response(200, json={"runs": [RUN]})
        if req.url.path.endswith("/trigger"):
            return httpx.Response(201, json=RUN)
        return httpx.Response(200, json=SCHEDULE)

    async with client(handler) as c:
        s = await c.schedules.put("nightly", cron="0 3 * * *", url="https://x")
        assert s.name == "nightly"
        assert (await c.schedules.trigger("nightly")).id == "run_1"
        assert [r.id async for r in c.schedules.runs("nightly")] == ["run_1"]


async def test_groups() -> None:
    group = {
        "id": "grp_1",
        "status": "open",
        "total": 0,
        "succeeded": 0,
        "failed": 0,
        "cancelled": 0,
        "remaining": 0,
        "created_at": "2026-10-19T10:00:00Z",
    }
    pages = {None: {"groups": [group], "next_cursor": "c1"}, "c1": {"groups": [group]}}

    def handler(req: httpx.Request) -> httpx.Response:
        if req.method == "GET":
            return httpx.Response(200, json=pages[req.url.params.get("cursor")])
        return httpx.Response(200, json=group)

    c = client(handler)
    assert (await c.groups.create(name="x")).id == "grp_1"
    assert (await c.groups.close("grp_1")).status == "open"
    assert [g.id async for g in c.groups.list()] == ["grp_1", "grp_1"]
