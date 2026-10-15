"""Starts a real `workly dev` server for the integration tests.

Uses the binary at WORKLY_BIN, or builds one from this checkout with `go
build`, so the SDK is always tested against the server from the same commit.
"""

import os
import re
import shutil
import subprocess
import threading
from collections.abc import Iterator
from pathlib import Path

import pytest

REPO = Path(__file__).resolve().parents[3]


@pytest.fixture(scope="session")
def workly_bin(tmp_path_factory: pytest.TempPathFactory) -> Path:
    if path := os.environ.get("WORKLY_BIN"):
        return Path(path)
    if shutil.which("go") is None:
        pytest.skip("set WORKLY_BIN or install Go to run integration tests")
    out = tmp_path_factory.mktemp("bin") / "workly"
    subprocess.run(["go", "build", "-o", str(out), "./cmd/workly"], cwd=REPO, check=True)
    return out


@pytest.fixture(scope="session")
def server_url(workly_bin: Path, tmp_path_factory: pytest.TempPathFactory) -> Iterator[str]:
    data = tmp_path_factory.mktemp("data")
    proc = subprocess.Popen(
        [str(workly_bin), "dev", "--port", "0", "--database-url", f"sqlite://{data}/dev.db"],
        cwd=data,
        stderr=subprocess.PIPE,
        text=True,
        env={k: v for k, v in os.environ.items() if not k.startswith("WORKLY_")},
    )
    assert proc.stderr is not None
    url = None
    for line in proc.stderr:
        if m := re.search(r"API:\s+(\S+)", line):
            url = m.group(1)
            break
    if url is None:
        proc.kill()
        pytest.fail("workly dev exited before it was ready")
    # Keep draining stderr so the server never blocks on a full pipe.
    threading.Thread(target=proc.stderr.read, daemon=True).start()
    yield url
    proc.terminate()
    proc.wait(timeout=10)
