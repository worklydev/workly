import pytest

from workly import SignatureVerificationError, _signing, sign, verify_signature

NOW = 1790000000


@pytest.fixture(autouse=True)
def frozen_time(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setattr(_signing.time, "time", lambda: float(NOW))


def test_sign_matches_server() -> None:
    # Same vector as internal/signing/signing_test.go.
    headers = sign(b'{"user_id":"123"}', "secret", timestamp=NOW)
    assert headers == {
        "Workly-Timestamp": "1790000000",
        "Workly-Signature": "v1=00f7255c88a43b3e2d47b211ff8e71cf935601cabff5e7adad0de2cc72a4e3c6",
    }


def test_valid() -> None:
    verify_signature(b"hello", sign(b"hello", "secret"), "secret")
    verify_signature("hello", sign("hello", b"secret"), b"secret")


def test_lowercase_header_names() -> None:
    headers = {k.lower(): v for k, v in sign(b"hello", "secret").items()}
    verify_signature(b"hello", headers, "secret")


def test_rotation_list() -> None:
    headers = sign(b"hello", "secret")
    headers["Workly-Signature"] = "v1=00ff, v1=zz, " + headers["Workly-Signature"]
    verify_signature(b"hello", headers, "secret")


def test_secret_from_env(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("WORKLY_SIGNING_SECRET", "secret")
    verify_signature(b"hello", sign(b"hello", "secret"))
    monkeypatch.delenv("WORKLY_SIGNING_SECRET")
    with pytest.raises(ValueError):
        verify_signature(b"hello", sign(b"hello", "secret"))


def test_tolerance() -> None:
    old = sign(b"hello", "secret", timestamp=NOW - 6 * 60)
    with pytest.raises(SignatureVerificationError, match="tolerance"):
        verify_signature(b"hello", old, "secret")
    verify_signature(b"hello", old, "secret", tolerance=600)
    verify_signature(b"hello", old, "secret", tolerance=None)


@pytest.mark.parametrize(
    ("name", "secret", "body", "change"),
    [
        ("wrong secret", "other", b"hello", {}),
        ("tampered body", "secret", b"hellO", {}),
        ("tampered timestamp", "secret", b"hello", {"Workly-Timestamp": str(NOW + 1)}),
        ("unknown scheme", "secret", b"hello", {"scheme": "v0"}),
    ],
)
def test_invalid(name: str, secret: str, body: bytes, change: dict[str, str]) -> None:
    headers = sign(b"hello", "secret")
    if scheme := change.pop("scheme", None):
        headers["Workly-Signature"] = scheme + headers["Workly-Signature"][2:]
    headers.update(change)
    with pytest.raises(SignatureVerificationError, match="does not match"):
        verify_signature(body, headers, secret)


@pytest.mark.parametrize(
    "headers",
    [
        {"Workly-Timestamp": str(NOW)},
        {"Workly-Timestamp": "yesterday", "Workly-Signature": "v1=00"},
        {},
    ],
)
def test_malformed(headers: dict[str, str]) -> None:
    with pytest.raises(SignatureVerificationError, match="malformed"):
        verify_signature(b"hello", headers, "secret")
