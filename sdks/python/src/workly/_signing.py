"""Delivery signatures. Must match internal/signing in the server:

    Workly-Timestamp: <unix seconds>
    Workly-Signature: v1=<hex HMAC-SHA256 of "<timestamp>.<body>">

Several comma-separated signatures may be sent during key rotation.
"""

from __future__ import annotations

import hashlib
import hmac
import os
import time
from collections.abc import Mapping
from datetime import timedelta

from ._errors import SignatureVerificationError

HEADER_SIGNATURE = "Workly-Signature"
HEADER_TIMESTAMP = "Workly-Timestamp"

DEFAULT_TOLERANCE = timedelta(minutes=5)


def sign(body: bytes | str, secret: str | bytes, timestamp: int | None = None) -> dict[str, str]:
    """Return the signature headers Workly would send with `body`.

    Useful for testing your endpoint without a server.
    """
    ts = int(time.time()) if timestamp is None else timestamp
    return {
        HEADER_TIMESTAMP: str(ts),
        HEADER_SIGNATURE: "v1=" + _mac(_bytes(secret), ts, _bytes(body)).hex(),
    }


def verify_signature(
    body: bytes | str,
    headers: Mapping[str, str],
    secret: str | bytes | None = None,
    *,
    tolerance: timedelta | float | None = DEFAULT_TOLERANCE,
) -> None:
    """Check that a delivery came from Workly. Raises SignatureVerificationError if not.

    Pass the raw request body, exactly as received (not re-serialized JSON),
    and the request headers. `secret` defaults to `WORKLY_SIGNING_SECRET`.
    `tolerance` is how old the timestamp may be, which limits replays; None
    skips the check.
    """
    if secret is None:
        secret = os.environ.get("WORKLY_SIGNING_SECRET")
        if not secret:
            raise ValueError("pass secret or set WORKLY_SIGNING_SECRET")

    signature = _header(headers, HEADER_SIGNATURE)
    timestamp = _header(headers, HEADER_TIMESTAMP)
    try:
        ts = int(timestamp or "")
    except ValueError:
        raise SignatureVerificationError("missing or malformed signature headers") from None
    if not signature:
        raise SignatureVerificationError("missing or malformed signature headers")

    if tolerance is not None:
        seconds = tolerance.total_seconds() if isinstance(tolerance, timedelta) else tolerance
        if seconds > 0 and abs(time.time() - ts) > seconds:
            raise SignatureVerificationError("timestamp outside tolerance")

    want = _mac(_bytes(secret), ts, _bytes(body))
    for part in signature.split(","):
        scheme, _, value = part.strip().partition("=")
        if scheme != "v1":
            continue
        try:
            got = bytes.fromhex(value)
        except ValueError:
            continue
        if hmac.compare_digest(got, want):
            return
    raise SignatureVerificationError("signature does not match")


def _mac(secret: bytes, timestamp: int, body: bytes) -> bytes:
    return hmac.new(secret, f"{timestamp}.".encode() + body, hashlib.sha256).digest()


def _bytes(value: str | bytes) -> bytes:
    return value.encode() if isinstance(value, str) else value


def _header(headers: Mapping[str, str], name: str) -> str | None:
    # Framework header objects are case-insensitive; plain dicts may not be.
    value = headers.get(name)
    if value is None:
        lower = name.lower()
        value = next((v for k, v in headers.items() if k.lower() == lower), None)
    return value
