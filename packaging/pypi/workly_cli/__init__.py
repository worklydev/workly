"""The workly binary, packaged for pip.

Installing this package puts `workly` on your PATH. `find_workly_bin()`
returns its path, for tools that want to start a server themselves (for
example a pytest fixture running `workly dev`).
"""

from __future__ import annotations

import os
import sys
import sysconfig


def find_workly_bin() -> str:
    """Return the path to the workly binary installed with this package."""
    exe = "workly" + (sysconfig.get_config_var("EXE") or "")

    candidates = [sysconfig.get_path("scripts")]
    # pip install --user
    user_scheme = sysconfig.get_preferred_scheme("user")
    candidates.append(sysconfig.get_path("scripts", scheme=user_scheme))
    # pip install --target DIR puts scripts in DIR/bin next to the package
    candidates.append(os.path.join(os.path.dirname(os.path.dirname(__file__)), "bin"))

    for directory in candidates:
        path = os.path.join(directory, exe)
        if os.path.isfile(path):
            return path
    raise FileNotFoundError(f"{exe} not found in {candidates} (Python {sys.version.split()[0]})")
