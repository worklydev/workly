"""`workly-cli` and `python -m workly_cli`: run the workly binary.

The package also installs the binary itself as `workly`; this entry point
exists so that `uvx workly-cli dev` works without naming the binary.
"""

from __future__ import annotations

import os
import subprocess
import sys

from . import find_workly_bin


def main() -> None:
    workly = find_workly_bin()
    if sys.platform == "win32":
        # No exec on Windows; forward the exit code instead.
        sys.exit(subprocess.call([workly, *sys.argv[1:]]))
    os.execv(workly, [workly, *sys.argv[1:]])


if __name__ == "__main__":
    main()
