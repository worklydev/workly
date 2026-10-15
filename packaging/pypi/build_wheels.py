"""Build workly-cli wheels from goreleaser's binaries.

    python packaging/pypi/build_wheels.py [--dist dist] [--out dist/pypi]

Reads dist/artifacts.json and writes one wheel per platform. Each wheel
installs the binary as a script (`workly` on PATH), plus the small
workly_cli package in this directory. Standard library only, so it runs
anywhere goreleaser does.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import json
import re
import sys
import zipfile
from pathlib import Path

HERE = Path(__file__).parent
NAME = "workly_cli"

# Go binaries built with CGO_ENABLED=0 are static, so one Linux wheel serves
# glibc and musl alike. macOS 12 is the oldest release current Go supports.
PLATFORM_TAGS = {
    ("linux", "amd64"): "manylinux_2_17_x86_64.manylinux2014_x86_64.musllinux_1_1_x86_64",
    ("linux", "arm64"): "manylinux_2_17_aarch64.manylinux2014_aarch64.musllinux_1_1_aarch64",
    ("darwin", "amd64"): "macosx_12_0_x86_64",
    ("darwin", "arm64"): "macosx_12_0_arm64",
    ("windows", "amd64"): "win_amd64",
    ("windows", "arm64"): "win_arm64",
}

METADATA = """\
Metadata-Version: 2.4
Name: workly-cli
Version: {version}
Summary: The Workly server and CLI: an open-source HTTP task queue
Home-page: https://workly.run
Project-URL: Repository, https://github.com/worklydev/workly
Requires-Python: >=3.10
License-Expression: Apache-2.0
License-File: LICENSE
Description-Content-Type: text/markdown

{readme}"""

WHEEL = """\
Wheel-Version: 1.0
Generator: workly build_wheels.py
Root-Is-Purelib: false
{tags}
"""

ENTRY_POINTS = """\
[console_scripts]
workly-cli = workly_cli.__main__:main
"""


def pep440(version: str) -> str:
    """Convert goreleaser's semver (0.1.0, 0.1.0-rc.1) to a PEP 440 version."""
    m = re.fullmatch(r"(\d+\.\d+\.\d+)(?:-(alpha|beta|rc)\.?(\d+))?", version)
    if m:
        base, pre, n = m.groups()
        return base + ({"alpha": "a", "beta": "b", "rc": "rc"}[pre] + n if pre else "")
    if m := re.fullmatch(r"(\d+\.\d+\.\d+)-SNAPSHOT-\w+", version):
        return m.group(1) + ".dev0"  # local snapshots; never published
    raise SystemExit(f"can't convert version {version!r} to PEP 440")


def record_line(path: str, data: bytes) -> str:
    digest = base64.urlsafe_b64encode(hashlib.sha256(data).digest()).rstrip(b"=").decode()
    return f"{path},sha256={digest},{len(data)}"


def build_wheel(binary: Path, goos: str, goarch: str, version: str, out: Path) -> Path:
    platform = PLATFORM_TAGS[(goos, goarch)]
    tags = "\n".join(f"Tag: py3-none-{p}" for p in platform.split("."))
    dist_info = f"{NAME}-{version}.dist-info"
    exe = "workly.exe" if goos == "windows" else "workly"

    files: list[tuple[str, bytes, int]] = []  # (path, data, unix mode)
    for src in sorted((HERE / NAME).glob("*.py")):
        files.append((f"{NAME}/{src.name}", src.read_bytes(), 0o644))
    files.append((f"{NAME}-{version}.data/scripts/{exe}", binary.read_bytes(), 0o755))
    readme = (HERE / "README.md").read_text()
    files.append((f"{dist_info}/METADATA", METADATA.format(version=version, readme=readme).encode(), 0o644))
    files.append((f"{dist_info}/licenses/LICENSE", (HERE / "LICENSE").read_bytes(), 0o644))
    files.append((f"{dist_info}/WHEEL", WHEEL.format(tags=tags).encode(), 0o644))
    files.append((f"{dist_info}/entry_points.txt", ENTRY_POINTS.encode(), 0o644))

    record_path = f"{dist_info}/RECORD"
    record = "\n".join([*(record_line(p, d) for p, d, _ in files), f"{record_path},,"]) + "\n"
    files.append((record_path, record.encode(), 0o644))

    out.mkdir(parents=True, exist_ok=True)
    wheel = out / f"{NAME}-{version}-py3-none-{platform}.whl"
    with zipfile.ZipFile(wheel, "w", zipfile.ZIP_DEFLATED) as zf:
        for path, data, mode in files:
            info = zipfile.ZipInfo(path, date_time=(2020, 1, 1, 0, 0, 0))
            info.external_attr = (0o100000 | mode) << 16  # regular file + permissions
            info.compress_type = zipfile.ZIP_DEFLATED
            zf.writestr(info, data)
    return wheel


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--dist", type=Path, default=Path("dist"), help="goreleaser's output directory")
    parser.add_argument("--out", type=Path, default=None, help="where to write wheels (default DIST/pypi)")
    args = parser.parse_args()

    dist: Path = args.dist
    out: Path = args.out or dist / "pypi"
    version = pep440(json.loads((dist / "metadata.json").read_text())["version"])
    binaries = [a for a in json.loads((dist / "artifacts.json").read_text()) if a["type"] == "Binary"]
    if not binaries:
        sys.exit(f"no binaries in {dist}/artifacts.json; run goreleaser first")

    for a in binaries:
        # artifacts.json paths are relative to the directory goreleaser ran in.
        binary = dist.parent / a["path"]
        wheel = build_wheel(binary, a["goos"], a["goarch"], version, out)
        print(wheel)


if __name__ == "__main__":
    main()
