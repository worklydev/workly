"""Retry and response handling shared by the sync and async clients."""

from __future__ import annotations

import math
import random
from typing import Any

import httpx

from ._errors import APIConnectionError, api_error
from ._requests import Request

# Gateway errors, retried for requests that are safe to repeat. A 429 is
# retried for every request: the server refused it without doing anything.
_GATEWAY_STATUSES = frozenset({502, 503, 504})
_MAX_DELAY = 8.0


def retry_after_error(req: Request, exc: httpx.TransportError) -> bool:
    # A request that never connected can always be resent; anything else
    # may have reached the server.
    if isinstance(exc, (httpx.ConnectError, httpx.ConnectTimeout)):
        return True
    return req.idempotent


def retry_after_status(req: Request, response: httpx.Response) -> bool:
    status = response.status_code
    return status == 429 or (req.idempotent and status in _GATEWAY_STATUSES)


def delay(attempt: int, response: httpx.Response | None = None) -> float:
    if response is not None:
        try:
            retry_after = float(response.headers.get("Retry-After", ""))
        except ValueError:
            retry_after = math.nan  # absent, or an HTTP date
        if retry_after >= 0:  # False for NaN
            return min(retry_after, _MAX_DELAY)
    return min(0.5 * 2**attempt, _MAX_DELAY) * random.uniform(0.5, 1)


def connection_error(req: Request, exc: httpx.TransportError) -> APIConnectionError:
    return APIConnectionError(f"{req.method} {req.path}: {exc!r}")


def decode(response: httpx.Response) -> Any:
    if response.status_code >= 400:
        code, message = "unknown", response.text or response.reason_phrase
        try:
            err = response.json()["error"]
            code, message = err["code"], err["message"]
        except (ValueError, KeyError, TypeError):
            pass
        raise api_error(response.status_code, code, message)
    if response.status_code == 204 or not response.content:
        return None
    return response.json()
