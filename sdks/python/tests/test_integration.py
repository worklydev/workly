"""End-to-end tests against a real `workly dev` server (see conftest.py)."""

import contextlib
import threading
import time
import uuid
from collections.abc import Iterator
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from typing import Any

import anyio
import pytest

import workly

DEV_SIGNING_SECRET = "workly-dev-secret"
FINISHED = ("succeeded", "failed", "cancelled")


@pytest.fixture
def client(server_url: str) -> Iterator[workly.Workly]:
    with workly.Workly(server_url) as c:
        yield c


@pytest.fixture
def queue(client: workly.Workly) -> Iterator[str]:
    name = f"test-{uuid.uuid4().hex[:8]}"
    yield name
    with contextlib.suppress(workly.NotFoundError):
        client.queues.delete(name)


def wait_finished(client: workly.Workly, task_id: str, timeout: float = 10) -> workly.Task:
    deadline = time.monotonic() + timeout
    while True:
        task = client.tasks.get(task_id)
        if task.state in FINISHED:
            return task
        if time.monotonic() > deadline:
            pytest.fail(f"task still {task.state} after {timeout}s")
        time.sleep(0.05)


def test_delivery(client: workly.Workly, server_url: str, queue: str) -> None:
    task = client.tasks.enqueue(queue, url=f"{server_url}/_dev/echo", payload={"user_id": 1})
    assert task.state in ("pending", "running")
    assert task.payload == {"user_id": 1}

    task = wait_finished(client, task.id)
    assert task.state == "succeeded"
    assert task.last_status == 200
    [attempt] = client.tasks.attempts(task.id)
    assert (attempt.number, attempt.outcome, attempt.status_code) == (1, "success", 200)
    assert task.last_attempt_at == attempt.finished_at
    activity = client.queues.activity(queue)
    assert (activity.succeeded, activity.failed) == (1, 0)


def test_task_delivery(client: workly.Workly, queue: str) -> None:
    client.queues.put(queue)
    client.queues.pause(queue)
    task = client.tasks.enqueue(
        queue, url="http://127.0.0.1:9/hook", payload={"b": 1}, headers={"X-Tenant": "acme"}
    )
    d = client.tasks.delivery(task.id)
    assert (d.method, d.url, d.headers["X-Tenant"], d.headers["Workly-Attempt"]) == (
        "POST",
        "http://127.0.0.1:9/hook",
        "acme",
        "1",
    )
    workly.verify_signature(d.body, d.headers, secret="workly-dev-secret")


def test_signed_delivery(client: workly.Workly, queue: str) -> None:
    received: list[tuple[bytes, dict[str, str]]] = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self) -> None:
            body = self.rfile.read(int(self.headers["Content-Length"]))
            received.append((body, dict(self.headers.items())))
            self.send_response(204)
            self.end_headers()

        def log_message(self, format: str, *args: Any) -> None:
            pass

    target = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    threading.Thread(target=target.serve_forever, daemon=True).start()
    try:
        url = f"http://127.0.0.1:{target.server_address[1]}/hook"
        task = client.tasks.enqueue(queue, url=url, payload={"hello": "world"})
        assert wait_finished(client, task.id).state == "succeeded"
    finally:
        target.shutdown()

    [(body, headers)] = received
    workly.verify_signature(body, headers, DEV_SIGNING_SECRET)
    assert headers["Workly-Task-Id"] == task.id
    with pytest.raises(workly.SignatureVerificationError):
        workly.verify_signature(body + b" ", headers, DEV_SIGNING_SECRET)


def test_retry_and_attempts(client: workly.Workly, server_url: str, queue: str) -> None:
    client.queues.put(queue, max_attempts=2, min_backoff_seconds=0, max_backoff_seconds=0)
    task = client.tasks.enqueue(queue, url=f"{server_url}/_dev/echo?status=500")
    task = wait_finished(client, task.id)
    assert (task.state, task.attempt_count, task.last_status) == ("failed", 2, 500)
    assert [a.outcome for a in client.tasks.attempts(task.id)] == ["http_error", "http_error"]

    assert task.retried_at is None
    task = client.tasks.retry(task.id)
    assert task.state == "pending"
    assert task.retried_at is not None
    assert wait_finished(client, task.id).attempt_count == 4
    # Attempt 3 followed the manual retry; attempt 4 was automatic.
    assert [a.retried_at for a in client.tasks.attempts(task.id)] == [
        None,
        None,
        task.retried_at,
        None,
    ]


def test_cancel(client: workly.Workly, server_url: str, queue: str) -> None:
    task = client.tasks.enqueue(queue, url=f"{server_url}/_dev/echo", delay_seconds=3600)
    assert client.tasks.cancel(task.id).state == "cancelled"
    task = client.tasks.retry(task.id)
    assert wait_finished(client, task.id).state == "succeeded"
    with pytest.raises(workly.ConflictError) as exc:
        client.tasks.cancel(task.id)
    assert exc.value.code == "invalid_state"


def test_queues(client: workly.Workly, server_url: str, queue: str) -> None:
    q = client.queues.put(queue, max_attempts=3, rate_per_second=5)
    assert (q.name, q.max_attempts, q.rate_per_second, q.paused) == (queue, 3, 5, False)

    # Declarative: omitted settings go back to their defaults.
    q = client.queues.put(queue, max_attempts=3)
    assert q.rate_per_second is None

    assert client.queues.pause(queue).paused
    for _ in range(3):
        client.tasks.enqueue(queue, url=f"{server_url}/_dev/echo")
    stats = client.queues.get(queue).stats
    assert stats is not None
    assert (stats.due, stats.failed) == (3, 0)
    assert stats.oldest_due_at is not None
    assert queue in [q.name for q in client.queues.list()]

    assert client.queues.cancel_pending(queue) == 3
    assert [t.state for t in client.tasks.list(queue=queue)] == ["cancelled"] * 3
    assert not client.queues.resume(queue).paused

    # A 400 fails at once; retry_failed replays it.
    failed = client.tasks.enqueue(queue, url=f"{server_url}/_dev/echo?status=400")
    finished = wait_finished(client, failed.id)
    assert (finished.state, finished.last_outcome, finished.last_status) == (
        "failed",
        "http_error",
        400,
    )
    [group] = client.queues.failures(queue)
    assert (group.count, group.status_code, group.latest_task_id) == (1, 400, failed.id)
    assert [t.id for t in client.tasks.list(queue=queue, last_error=group.error)] == [failed.id]
    assert client.queues.retry_failed(queue, last_error="something else") == 0
    assert client.queues.retry_failed(queue, last_error=group.error) == 1
    assert client.tasks.get(failed.id).state in ("pending", "running", "failed")

    client.queues.delete(queue)
    with pytest.raises(workly.NotFoundError) as exc:
        client.queues.get(queue)
    assert exc.value.code == "queue_not_found"


def test_enqueue_many_and_search(client: workly.Workly, queue: str) -> None:
    client.queues.put(queue)
    client.queues.pause(queue)
    specs: list[workly.TaskSpec] = [
        {"url": "https://example.com/hook", "idempotency_key": f"k{i}", "body": str(i)}
        for i in range(5)
    ]
    results = client.tasks.enqueue_many(queue, specs)
    assert all(r.created for r in results)
    again = client.tasks.enqueue_many(queue, specs[:2])
    assert [r.created for r in again] == [False, False]
    assert [r.task.id for r in again] == [r.task.id for r in results[:2]]

    # IDs only order by millisecond, so a batch's tasks come back in any order.
    found = [t.id for t in client.tasks.list(queue=queue, page_size=2)]
    assert sorted(found) == sorted(r.task.id for r in results)

    page = client.tasks.list_page(queue=queue, limit=2)
    assert len(page.tasks) == 2
    assert page.next_cursor is not None

    # Pending tasks by run_at come in the order they run, soonest first.
    later = client.tasks.enqueue(queue, url="https://example.test", delay_seconds=60)
    soon = client.tasks.enqueue(queue, url="https://example.test", delay_seconds=30)
    by_run_at = [t.id for t in client.tasks.list(queue=queue, sort="run_at", page_size=2)]
    assert by_run_at[-2:] == [soon.id, later.id]

    [task] = client.tasks.list(idempotency_key="k3", queue=queue)
    assert task.body == "3"


def test_validation_error(client: workly.Workly, queue: str) -> None:
    with pytest.raises(workly.BadRequestError) as exc:
        client.tasks.enqueue(queue, url="ftp://example.com")
    assert exc.value.code == "invalid_request"


def test_connection_error() -> None:
    with (
        workly.Workly("http://127.0.0.1:1", max_retries=0) as c,
        pytest.raises(workly.APIConnectionError),
    ):
        c.queues.list()


@pytest.mark.anyio
@pytest.mark.parametrize("anyio_backend", ["asyncio"])
async def test_async_delivery(server_url: str, queue: str, anyio_backend: str) -> None:
    async with workly.AsyncWorkly(server_url) as c:
        task = await c.tasks.enqueue(queue, url=f"{server_url}/_dev/echo")
        for _ in range(200):
            task = await c.tasks.get(task.id)
            if task.state == "succeeded":
                break
            await anyio.sleep(0.05)
        assert task.state == "succeeded"
        assert [t.id async for t in c.tasks.list(queue=queue)] == [task.id]


def wait_run_finished(client: workly.Workly, run_id: str, timeout: float = 10) -> workly.Run:
    deadline = time.monotonic() + timeout
    while True:
        run = client.runs.get(run_id)
        if run.state in (*FINISHED, "skipped"):
            return run
        if time.monotonic() > deadline:
            pytest.fail(f"run still {run.state} after {timeout}s")
        time.sleep(0.05)


def test_schedules(client: workly.Workly, server_url: str) -> None:
    name = f"test-{uuid.uuid4().hex[:8]}"
    s = client.schedules.put(
        name,
        cron="0 3 * * *",
        timezone="Europe/Stockholm",
        url=f"{server_url}/_dev/echo?status=500",
        payload={"kind": "daily"},
        max_attempts=2,
        min_backoff_seconds=0,
        max_backoff_seconds=0,
    )
    try:
        assert (s.name, s.timezone, s.overlap, s.paused) == (
            name,
            "Europe/Stockholm",
            "skip",
            False,
        )
        assert len(s.next_runs) == 5 and s.next_run_at == s.next_runs[0]
        assert s.payload == {"kind": "daily"}

        assert client.schedules.pause(name).next_run_at is None
        run = client.schedules.trigger(name)  # runs even while paused
        run = wait_run_finished(client, run.id)
        assert (run.state, run.trigger, run.attempt_count, run.last_status) == (
            "failed",
            "manual",
            2,
            500,
        )
        assert [a.status_code for a in client.runs.attempts(run.id)] == [500, 500]
        d = client.runs.delivery(run.id)
        assert d.headers["Workly-Run-Id"] == run.id
        workly.verify_signature(d.body, d.headers, DEV_SIGNING_SECRET)

        assert [r.id for r in client.schedules.runs(name, state="failed")] == [run.id]
        last = client.schedules.get(name).last_run
        assert last is not None and last.id == run.id
        assert client.schedules.resume(name).next_run_at is not None
        assert [x.name for x in client.schedules.list()].count(name) == 1

        with pytest.raises(workly.BadRequestError):
            client.schedules.put(name, cron="every day", url=server_url)
    finally:
        client.schedules.delete(name)
    with pytest.raises(workly.NotFoundError) as exc:
        client.runs.get(run.id)
    assert exc.value.code == "run_not_found"


def test_alert_channels(client: workly.Workly) -> None:
    hook = f"https://hooks.slack.com/services/T0/B0/{uuid.uuid4().hex}"
    channel = client.alerts.create("slack", hook)
    try:
        assert channel.target.endswith(hook[-4:]) and hook not in channel.target
        listed = client.alerts.list()
        assert channel.id in [c.id for c in listed.channels]
        if not listed.email_enabled:
            with pytest.raises(workly.BadRequestError) as exc:
                client.alerts.create("email", "ops@example.com")
            assert exc.value.code == "email_unavailable"
    finally:
        client.alerts.delete(channel.id)
    with pytest.raises(workly.NotFoundError):
        client.alerts.delete(channel.id)


def test_groups(client: workly.Workly, server_url: str, queue: str) -> None:
    group = client.groups.create(
        name="prices", on_complete=workly.Callback(url=f"{server_url}/_dev/echo", queue=queue)
    )
    tasks = [
        client.tasks.enqueue(queue, url=f"{server_url}/_dev/echo", group_id=group.id)
        for _ in range(3)
    ]
    assert all(t.group_id == group.id for t in tasks)
    group = client.groups.close(group.id)
    assert group.status in ("running", "succeeded")

    with pytest.raises(workly.ConflictError) as exc:
        client.tasks.enqueue(queue, url=f"{server_url}/_dev/echo", group_id=group.id)
    assert exc.value.code == "group_closed"

    deadline = time.monotonic() + 10
    while (group := client.groups.get(group.id)).callback_task_id is None:
        if time.monotonic() > deadline:
            pytest.fail(f"group still {group.status} after 10s")
        time.sleep(0.05)
    assert (group.status, group.succeeded, group.remaining) == ("succeeded", 3, 0)
    callback = wait_finished(client, group.callback_task_id)
    assert callback.state == "succeeded"
    assert callback.payload == {
        "group_id": group.id,
        "name": "prices",
        "status": "succeeded",
        "total": 3,
        "succeeded": 3,
        "failed": 0,
        "cancelled": 0,
    }
    assert next(g.id for g in client.groups.list(name="prices")) == group.id
    assert len(list(client.tasks.list(group_id=group.id))) == 3
