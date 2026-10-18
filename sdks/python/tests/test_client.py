"""Client behaviour against a fake transport. test_integration.py covers the real server."""

import json
from collections.abc import Callable
from datetime import UTC, datetime
from typing import Any

import httpx
import pytest

import workly
from workly import _transport

TASK: dict[str, Any] = {
    "id": "task_1",
    "queue": "emails",
    "state": "pending",
    "method": "POST",
    "url": "https://example.com/hook",
    "headers": {},
    "payload": {"user_id": 1},
    "run_at": "2026-09-27T10:00:00.123456789Z",
    "attempt_count": 0,
    "created_at": "2026-09-27T10:00:00Z",
    "updated_at": "2026-09-27T10:00:00Z",
    "some_future_field": True,
}

Handler = Callable[[httpx.Request], httpx.Response]


@pytest.fixture(autouse=True)
def no_sleep(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(_transport, "delay", lambda *_: 0.0)  # pyright: ignore


def client(handler: Handler, **kwargs: Any) -> workly.Workly:
    return workly.Workly(http_client=httpx.Client(transport=httpx.MockTransport(handler)), **kwargs)


def recorder(*responses: httpx.Response) -> tuple[list[httpx.Request], Handler]:
    requests: list[httpx.Request] = []
    queue = list(responses)

    def handler(req: httpx.Request) -> httpx.Response:
        requests.append(req)
        return queue.pop(0) if len(queue) > 1 else queue[0]

    return requests, handler


def test_defaults(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.delenv("WORKLY_URL", raising=False)
    monkeypatch.delenv("WORKLY_API_KEY", raising=False)
    requests, handler = recorder(httpx.Response(200, json=TASK))
    client(handler).tasks.get("task_1")
    assert str(requests[0].url) == "http://localhost:7337/v1/tasks/task_1"
    assert "authorization" not in requests[0].headers
    assert requests[0].headers["user-agent"].startswith("workly-python/")


def test_env(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("WORKLY_URL", "https://workly.example.com/")
    monkeypatch.setenv("WORKLY_API_KEY", "key")
    requests, handler = recorder(httpx.Response(200, json=TASK))
    c = client(handler)
    assert c.url == "https://workly.example.com"
    c.tasks.get("task_1")
    assert requests[0].headers["authorization"] == "Bearer key"


def test_arguments_override_env(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("WORKLY_API_KEY", "env")
    requests, handler = recorder(httpx.Response(200, json=TASK))
    client(handler, url="http://other:1", api_key="arg").tasks.get("task_1")
    assert str(requests[0].url).startswith("http://other:1/")
    assert requests[0].headers["authorization"] == "Bearer arg"


def test_enqueue() -> None:
    requests, handler = recorder(httpx.Response(201, json=TASK))
    task = client(handler).tasks.enqueue(
        "emails",
        url="https://example.com/hook",
        payload={"user_id": 1},
        run_at=datetime(2026, 9, 27, 10, tzinfo=UTC),
        headers={"X-Tenant": "a"},
        traceparent="00-abc-def-01",
    )
    req = requests[0]
    assert req.method == "POST"
    assert req.url.path == "/v1/queues/emails/tasks"
    assert req.headers["traceparent"] == "00-abc-def-01"
    assert json.loads(req.content) == {
        "url": "https://example.com/hook",
        "payload": {"user_id": 1},
        "run_at": "2026-09-27T10:00:00+00:00",
        "headers": {"X-Tenant": "a"},
    }
    assert task.id == "task_1"
    assert task.payload == {"user_id": 1}
    assert task.run_at == datetime(2026, 9, 27, 10, 0, 0, 123456, tzinfo=UTC)
    assert task.finished_at is None


def test_enqueue_validation() -> None:
    c = client(recorder(httpx.Response(201, json=TASK))[1])
    with pytest.raises(ValueError, match="timezone-aware"):
        c.tasks.enqueue("q", url="https://x", run_at=datetime(2026, 1, 1))
    with pytest.raises(ValueError, match="payload or body"):
        c.tasks.enqueue("q", url="https://x", payload={}, body="x")
    with pytest.raises(ValueError, match="delay_seconds or run_at"):
        c.tasks.enqueue("q", url="https://x", delay_seconds=1, run_at=datetime.now(UTC))


def test_path_escaping() -> None:
    requests, handler = recorder(httpx.Response(200, json=TASK))
    client(handler).tasks.get("../queues")
    assert requests[0].url.raw_path == b"/v1/tasks/..%2Fqueues"


@pytest.mark.parametrize(
    ("status", "code", "error"),
    [
        (400, "invalid_request", workly.BadRequestError),
        (401, "unauthorized", workly.AuthenticationError),
        (403, "quota_exceeded", workly.PermissionDeniedError),
        (404, "task_not_found", workly.NotFoundError),
        (409, "invalid_state", workly.ConflictError),
        (413, "payload_too_large", workly.PayloadTooLargeError),
        (429, "rate_limited", workly.RateLimitError),
        (500, "internal", workly.APIError),
    ],
)
def test_errors(status: int, code: str, error: type[workly.APIError]) -> None:
    body = {"error": {"code": code, "message": "nope"}}
    c = client(recorder(httpx.Response(status, json=body))[1], max_retries=0)
    with pytest.raises(error) as exc:
        c.tasks.get("task_1")
    assert (exc.value.status, exc.value.code, exc.value.message) == (status, code, "nope")


def test_error_without_json_body() -> None:
    c = client(recorder(httpx.Response(502, text="bad gateway"))[1], max_retries=0)
    with pytest.raises(workly.APIError) as exc:
        c.tasks.get("task_1")
    assert (exc.value.code, exc.value.message) == ("unknown", "bad gateway")


def test_retries_idempotent_requests() -> None:
    requests, handler = recorder(httpx.Response(503), httpx.Response(200, json=TASK))
    client(handler).tasks.get("task_1")
    assert len(requests) == 2


def test_gives_up_after_max_retries() -> None:
    requests, handler = recorder(httpx.Response(503))
    with pytest.raises(workly.APIError):
        client(handler, max_retries=2).tasks.get("task_1")
    assert len(requests) == 3


def test_enqueue_without_key_is_not_retried() -> None:
    requests, handler = recorder(httpx.Response(503))
    with pytest.raises(workly.APIError):
        client(handler).tasks.enqueue("q", url="https://x")
    assert len(requests) == 1


def test_enqueue_with_key_is_retried() -> None:
    requests, handler = recorder(httpx.Response(503), httpx.Response(201, json=TASK))
    client(handler).tasks.enqueue("q", url="https://x", idempotency_key="k")
    assert len(requests) == 2


def test_429_is_retried_even_for_enqueue() -> None:
    limited = httpx.Response(
        429,
        headers={"Retry-After": "0"},
        json={"error": {"code": "rate_limited", "message": "slow down"}},
    )
    requests, handler = recorder(limited, httpx.Response(201, json=TASK))
    client(handler).tasks.enqueue("q", url="https://x")
    assert len(requests) == 2


@pytest.mark.parametrize("retry_after", ["-1", "nan", "inf", "Wed, 21 Oct 2026 07:28:00 GMT"])
def test_unusable_retry_after(retry_after: str) -> None:
    from workly import _transport

    response = httpx.Response(503, headers={"Retry-After": retry_after})
    assert 0 <= _transport.delay(0, response) <= 8


def test_connect_errors_are_retried() -> None:
    calls = 0

    def handler(req: httpx.Request) -> httpx.Response:
        nonlocal calls
        calls += 1
        if calls == 1:
            raise httpx.ConnectError("refused", request=req)
        return httpx.Response(201, json=TASK)

    client(handler).tasks.enqueue("q", url="https://x")
    assert calls == 2


def test_read_errors_are_not_retried_for_enqueue() -> None:
    calls = 0

    def handler(req: httpx.Request) -> httpx.Response:
        nonlocal calls
        calls += 1
        raise httpx.ReadTimeout("slow", request=req)

    with pytest.raises(workly.APIConnectionError):
        client(handler).tasks.enqueue("q", url="https://x")
    assert calls == 1


def test_list_paginates() -> None:
    pages = {
        None: {"tasks": [TASK, {**TASK, "id": "task_2"}], "next_cursor": "c1"},
        "c1": {"tasks": [{**TASK, "id": "task_3"}]},
    }
    requests: list[httpx.Request] = []

    def handler(req: httpx.Request) -> httpx.Response:
        requests.append(req)
        return httpx.Response(200, json=pages[req.url.params.get("cursor")])

    tasks = client(handler).tasks.list(
        queue="emails",
        state="failed",
        created_after=datetime(2026, 1, 1, tzinfo=UTC),
        sort="last_attempt_at",
        order="asc",
        page_size=2,
    )
    assert [t.id for t in tasks] == ["task_1", "task_2", "task_3"]
    assert dict(requests[0].url.params) == {
        "queue": "emails",
        "state": "failed",
        "created_after": "2026-01-01T00:00:00+00:00",
        "sort": "last_attempt_at",
        "order": "asc",
        "limit": "2",
    }
    assert requests[1].url.params["cursor"] == "c1"


def test_enqueue_many() -> None:
    body = {"results": [{"created": True, "task": TASK}, {"created": False, "task": TASK}]}
    requests, handler = recorder(httpx.Response(200, json=body))
    results = client(handler).tasks.enqueue_many(
        "emails",
        [{"url": "https://x", "idempotency_key": "a"}, {"url": "https://y", "body": "hi"}],
    )
    assert [r.created for r in results] == [True, False]
    assert requests[0].url.path == "/v1/queues/emails/tasks:batch"
    assert json.loads(requests[0].content) == {
        "tasks": [{"url": "https://x", "idempotency_key": "a"}, {"url": "https://y", "body": "hi"}]
    }


def test_queue_put_sends_only_given_settings() -> None:
    queue = {
        "name": "emails",
        "paused": False,
        "max_attempts": 5,
        "min_backoff_seconds": 1,
        "max_backoff_seconds": 3600,
        "backoff_multiplier": 2,
        "timeout_seconds": 30,
        "retry_on_4xx": False,
        "rate_per_second": 10,
        "max_concurrency": 3,
        "created_at": "2026-09-27T10:00:00Z",
        "updated_at": "2026-09-27T10:00:00Z",
    }
    requests, handler = recorder(httpx.Response(201, json=queue))
    q = client(handler).queues.put("emails", max_attempts=5, rate_per_second=10)
    assert requests[0].method == "PUT"
    assert json.loads(requests[0].content) == {"max_attempts": 5, "rate_per_second": 10}
    assert (q.rate_per_second, q.max_concurrency, q.stats) == (10, 3, None)


def test_queue_delete_cancel_pending_and_retry_failed() -> None:
    requests, handler = recorder(
        httpx.Response(204),
        httpx.Response(200, json={"cancelled": 3}),
        httpx.Response(200, json={"retried": 2}),
        httpx.Response(200, json={"retried": 1}),
    )
    c = client(handler)
    assert c.queues.delete("emails") is None
    assert c.queues.cancel_pending("emails") == 3
    assert c.queues.retry_failed("emails") == 2
    assert c.queues.retry_failed("emails", last_error="HTTP 503 Service Unavailable") == 1
    assert [(r.method, r.url.path, dict(r.url.params)) for r in requests] == [
        ("DELETE", "/v1/queues/emails", {}),
        ("POST", "/v1/queues/emails/cancel-pending", {}),
        ("POST", "/v1/queues/emails/retry-failed", {}),
        ("POST", "/v1/queues/emails/retry-failed", {"last_error": "HTTP 503 Service Unavailable"}),
    ]


def test_queue_failures() -> None:
    requests, handler = recorder(
        httpx.Response(
            200,
            json={
                "groups": [
                    {
                        "error": "HTTP 503 Service Unavailable",
                        "count": 7,
                        "outcome": "http_error",
                        "status_code": 503,
                        "last_failed_at": "2026-09-27T10:00:00Z",
                        "latest_task_id": "task_1",
                        "response_body": "down",
                    },
                    {
                        "error": "no response within 30s",
                        "count": 1,
                        "outcome": "timeout",
                        "last_failed_at": "2026-09-27T09:00:00Z",
                        "latest_task_id": "task_2",
                    },
                ]
            },
        )
    )
    groups = client(handler).queues.failures("emails", limit=5)
    assert dict(requests[0].url.params) == {"limit": "5"}
    assert [(g.error, g.count, g.status_code, g.response_body) for g in groups] == [
        ("HTTP 503 Service Unavailable", 7, 503, "down"),
        ("no response within 30s", 1, None, None),
    ]
    assert groups[1].outcome == "timeout"


def test_task_delivery() -> None:
    requests, handler = recorder(
        httpx.Response(
            200,
            json={
                "method": "POST",
                "url": "https://example.com/hook",
                "headers": {"Workly-Attempt": "1"},
                "body": '{"user_id": 1}',
            },
        )
    )
    d = client(handler).tasks.delivery("task_1")
    assert (requests[0].method, requests[0].url.path) == ("GET", "/v1/tasks/task_1/delivery")
    assert (d.method, d.headers, d.body) == ("POST", {"Workly-Attempt": "1"}, '{"user_id": 1}')


def test_queue_activity() -> None:
    requests, handler = recorder(
        httpx.Response(
            200,
            json={"since": "2026-09-20T10:00:00Z", "succeeded": 5, "failed": 1, "cancelled": 0},
        )
    )
    a = client(handler).queues.activity("emails", since=datetime(2026, 9, 20, 10, tzinfo=UTC))
    assert requests[0].url.path == "/v1/queues/emails/activity"
    assert requests[0].url.params["since"].startswith("2026-09-20T10:00:00")
    assert (a.succeeded, a.failed, a.cancelled, a.retention_seconds) == (5, 1, 0, None)


def test_tasks_activity() -> None:
    requests, handler = recorder(
        httpx.Response(
            200,
            json={
                "range": "1h",
                "bucket_seconds": 300,
                "buckets": [{"start": "2026-09-20T10:00:00Z", "counts": {"emails": 3}}],
            },
        )
    )
    a = client(handler).tasks.activity(range="1h")
    assert (requests[0].url.path, requests[0].url.params["range"]) == ("/v1/activity", "1h")
    assert (a.bucket_seconds, a.buckets[0].counts, a.retention_seconds) == (
        300,
        {"emails": 3},
        None,
    )


def test_preview_cron() -> None:
    requests, handler = recorder(
        httpx.Response(200, json={"next_runs": ["2026-09-21T03:00:00Z"], "interval_seconds": 86400})
    )
    p = client(handler).schedules.preview_cron("0 3 * * *", timezone="Europe/Stockholm")
    assert requests[0].url.path == "/v1/cron/preview"
    assert dict(requests[0].url.params) == {"cron": "0 3 * * *", "timezone": "Europe/Stockholm"}
    assert (len(p.next_runs), p.interval_seconds) == (1, 86400)


SCHEDULE: dict[str, Any] = {
    "name": "nightly",
    "cron": "0 3 * * *",
    "timezone": "Europe/Stockholm",
    "method": "POST",
    "url": "https://example.com/report",
    "headers": {"Content-Type": "application/json"},
    "payload": {"kind": "daily"},
    "timeout_seconds": 30,
    "max_attempts": 3,
    "min_backoff_seconds": 5,
    "max_backoff_seconds": 300,
    "backoff_multiplier": 2,
    "retry_on_4xx": False,
    "overlap": "skip",
    "missed": "skip",
    "paused": False,
    "next_run_at": "2026-10-02T01:00:00Z",
    "next_runs": ["2026-10-02T01:00:00Z", "2026-10-03T01:00:00Z"],
    "last_run": {
        "id": "run_1",
        "state": "skipped",
        "trigger": "schedule",
        "scheduled_for": "2026-10-01T01:00:00Z",
    },
    "created_at": "2026-09-27T10:00:00Z",
    "updated_at": "2026-09-27T10:00:00Z",
    "some_future_field": True,
}

RUN: dict[str, Any] = {
    "id": "run_1",
    "schedule": "nightly",
    "state": "skipped",
    "trigger": "schedule",
    "scheduled_for": "2026-10-01T01:00:00Z",
    "skip_reason": "overlap",
    "method": "POST",
    "url": "https://example.com/report",
    "headers": {},
    "body": "go",
    "run_at": "2026-10-01T01:00:00Z",
    "attempt_count": 0,
    "created_at": "2026-10-01T01:00:00Z",
    "updated_at": "2026-10-01T01:00:00Z",
}


def test_put_schedule() -> None:
    requests, handler = recorder(httpx.Response(201, json=SCHEDULE))
    s = client(handler).schedules.put(
        "nightly",
        cron="0 3 * * *",
        timezone="Europe/Stockholm",
        url="https://example.com/report",
        payload={"kind": "daily"},
        overlap="allow",
    )
    req = requests[0]
    assert (req.method, req.url.path) == ("PUT", "/v1/schedules/nightly")
    assert json.loads(req.content) == {
        "cron": "0 3 * * *",
        "timezone": "Europe/Stockholm",
        "url": "https://example.com/report",
        "payload": {"kind": "daily"},
        "overlap": "allow",
    }
    assert s.next_runs == [
        datetime(2026, 10, 2, 1, tzinfo=UTC),
        datetime(2026, 10, 3, 1, tzinfo=UTC),
    ]
    assert s.last_run is not None and s.last_run.state == "skipped"
    with pytest.raises(ValueError, match="payload or body"):
        client(handler).schedules.put("n", cron="@daily", url="https://x", payload={}, body="x")


def test_trigger_is_not_retried() -> None:
    requests, handler = recorder(httpx.Response(503))
    with pytest.raises(workly.APIError):
        client(handler).schedules.trigger("nightly")
    assert len(requests) == 1


def test_runs() -> None:
    pages = {None: {"runs": [RUN], "next_cursor": "c1"}, "c1": {"runs": [RUN]}}
    seen: list[httpx.Request] = []

    def handler(req: httpx.Request) -> httpx.Response:
        seen.append(req)
        return httpx.Response(200, json=pages[req.url.params.get("cursor")])

    runs = list(client(handler).schedules.runs("nightly", state="skipped"))
    assert [r.skip_reason for r in runs] == ["overlap", "overlap"]
    assert seen[0].url.path == "/v1/schedules/nightly/runs"
    assert seen[0].url.params["state"] == "skipped"

    requests, retry_handler = recorder(httpx.Response(200, json=RUN))
    run = client(retry_handler).runs.retry("run_1")
    assert (requests[0].method, requests[0].url.path) == ("POST", "/v1/runs/run_1/retry")
    assert run.body == "go" and run.payload is None


def test_alert_channels() -> None:
    channel = {
        "id": "ach_1",
        "type": "slack",
        "target": "https://hooks.slack.com/services/…abcd",
        "created_at": "2026-10-06T14:00:00Z",
    }
    requests, handler = recorder(
        httpx.Response(200, json={"channels": [channel], "email_enabled": False}),
        httpx.Response(201, json=channel),
        httpx.Response(204),
        httpx.Response(204),
    )
    c = client(handler)
    listed = c.alerts.list()
    assert (listed.email_enabled, [x.id for x in listed.channels]) == (False, ["ach_1"])
    created = c.alerts.create("slack", "https://hooks.slack.com/services/T0/B0/abcd")
    assert (created.type, created.created_at) == ("slack", datetime(2026, 10, 6, 14, tzinfo=UTC))
    c.alerts.test("ach_1")
    c.alerts.delete("ach_1")
    assert [(q.method, q.url.path) for q in requests] == [
        ("GET", "/v1/alert-channels"),
        ("POST", "/v1/alert-channels"),
        ("POST", "/v1/alert-channels/ach_1/test"),
        ("DELETE", "/v1/alert-channels/ach_1"),
    ]
    assert json.loads(requests[1].content) == {
        "type": "slack",
        "target": "https://hooks.slack.com/services/T0/B0/abcd",
    }
