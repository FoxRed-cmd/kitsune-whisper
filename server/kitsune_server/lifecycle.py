"""The persisted soft-stop flag.

A soft-stop keeps the process and the Admin API alive but releases the model:
``POST /transcribe`` then answers ``503`` until ``POST /admin/start`` loads the
model again. This module owns only the flag and its persistence, so a
supervised restart (or a crash) does not silently reload a model the user
deliberately unloaded.

The model itself belongs to exactly one runtime seam:
:class:`kitsune_server.runtime.ModelRuntime`. :class:`ModelManager` owns the
loaded Transcriber, online activation, and idle unload; the flag here is the
one thing that outlives a process. Tests that want a fixed model use
:class:`kitsune_server.runtime.FixedModelRuntime` rather than a second
Lifecycle-shaped Transcriber wrapper.
"""

from __future__ import annotations

import json
import os
import tempfile
from collections.abc import Mapping
from contextlib import suppress
from pathlib import Path

from .config import user_state_dir

STOPPED_FILENAME = "lifecycle.json"

# A restart is a self-exit with a non-zero code so systemd's Restart=on-failure
# and Task Scheduler's RestartOnFailure both bring the Server back.
RESTART_EXIT_CODE = 75


def lifecycle_state_path(env: Mapping[str, str] | None = None) -> Path:
    """The file that remembers a soft-stop across process restarts."""
    return user_state_dir(dict(env) if env is not None else None) / STOPPED_FILENAME


def _read_stopped(path: Path) -> bool:
    try:
        document = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return False
    return bool(document.get("stopped")) if isinstance(document, dict) else False


def _atomic_write(path: Path, stopped: bool) -> None:
    """Persist the soft-stop flag; a read-only state directory is not fatal."""
    path.parent.mkdir(parents=True, exist_ok=True)
    descriptor, temp_name = tempfile.mkstemp(
        dir=path.parent, prefix=f".{path.name}.", suffix=".tmp"
    )
    temp_path = Path(temp_name)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as handle:
            json.dump({"stopped": stopped}, handle)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(temp_path, path)
    except BaseException:
        temp_path.unlink(missing_ok=True)
        raise


class Lifecycle:
    """The soft-stop flag, and nothing else.

    ``state_path`` remembers the flag across process restarts; ``None`` keeps it
    in memory, which is what tests and throwaway Servers use. The runtime that
    owns the model asks this flag before loading, so a stop genuinely releases
    the model and a start clears the flag.
    """

    def __init__(
        self,
        *,
        state_path: Path | str | None = None,
        stopped: bool = False,
    ) -> None:
        self._state_path = Path(state_path).expanduser() if state_path is not None else None
        self._stopped = bool(stopped)
        if self._state_path is not None and _read_stopped(self._state_path):
            self._stopped = True

    @property
    def stopped(self) -> bool:
        return self._stopped

    def mark_stopped(self) -> None:
        """Remember a soft-stop without touching any model."""
        self._stopped = True
        self._persist(True)

    def clear(self) -> None:
        """Clear the soft-stop flag without loading any model."""
        self._stopped = False
        self._persist(False)

    def _persist(self, stopped: bool) -> None:
        if self._state_path is None:
            return
        # A soft-stop still applies to this process; only persistence across a
        # restart is lost when the state directory is read-only.
        with suppress(OSError):
            _atomic_write(self._state_path, stopped)


__all__ = [
    "RESTART_EXIT_CODE",
    "STOPPED_FILENAME",
    "Lifecycle",
    "lifecycle_state_path",
]
