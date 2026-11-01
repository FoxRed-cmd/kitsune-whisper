"""Soft-stop state and the model load/unload seam for the Admin API.

A soft-stop keeps the process and the Admin API alive but releases the model:
``POST /transcribe`` then answers ``503`` until ``POST /admin/start`` loads the
model again. The stopped flag is persisted next to the Server state so a
supervised restart (or a crash) does not silently reload a model the user
deliberately unloaded.

The HTTP app takes a :class:`Lifecycle` so tests inject a loader and an unload
recorder and never load a real model. Production wraps
``WhisperTranscriber.load``; dropping the reference releases the model.

#76 (Model catalog) extends this seam rather than adding a second one: an
online model activation is a ``Lifecycle`` swap (``stop`` then ``start`` with a
new model config), and idle unload is ``stop`` without the persisted stopped
flag. ``create_app(lifecycle=...)`` stays the single lifecycle injection point.
"""

from __future__ import annotations

import json
import os
import tempfile
from collections.abc import Callable, Mapping
from contextlib import suppress
from pathlib import Path

from .config import user_state_dir
from .transcriber import AudioClip, EngineInfo, Transcriber, Transcription

STOPPED_FILENAME = "lifecycle.json"

# A restart is a self-exit with a non-zero code so systemd's Restart=on-failure
# and Task Scheduler's RestartOnFailure both bring the Server back.
RESTART_EXIT_CODE = 75


class LifecycleError(Exception):
    """The model cannot be loaded or used in the current lifecycle state."""


Loader = Callable[[], Transcriber]
Unloader = Callable[[Transcriber | None], None]


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
    """Owns the loaded model and the persisted soft-stop flag.

    ``load`` builds a transcriber; ``unload`` receives the one being released
    (tests record it, production lets it fall out of scope). ``info`` describes
    the model while it is unloaded, so ``GET /status`` can still name the model
    and report that it is stopped.
    """

    def __init__(
        self,
        load: Loader,
        *,
        unload: Unloader | None = None,
        info: EngineInfo | None = None,
        state_path: Path | str | None = None,
        stopped: bool = False,
    ) -> None:
        self._load = load
        self._unload = unload if unload is not None else (lambda _transcriber: None)
        self._fallback_info = info
        self._state_path = Path(state_path).expanduser() if state_path is not None else None
        self._transcriber: Transcriber | None = None
        self._stopped = bool(stopped)
        if self._state_path is not None and _read_stopped(self._state_path):
            self._stopped = True

    @property
    def stopped(self) -> bool:
        return self._stopped

    @classmethod
    def from_transcriber(cls, transcriber: Transcriber) -> Lifecycle:
        """Wrap an already loaded model, for tests and throwaway Servers."""
        lifecycle = cls(lambda: transcriber, info=transcriber.info)
        lifecycle._transcriber = transcriber
        return lifecycle

    @property
    def loaded(self) -> bool:
        return self._transcriber is not None

    @property
    def info(self) -> EngineInfo:
        """The loaded model's metadata, or the last known one while unloaded."""
        if self._transcriber is not None:
            return self._transcriber.info
        if self._fallback_info is not None:
            return self._fallback_info
        return EngineInfo("", "", "")

    @property
    def transcriber(self) -> Transcriber:
        """The loaded model; raises while soft-stopped or cold."""
        if self._stopped:
            raise LifecycleError(
                "the Server is soft-stopped; start it through the Admin API before transcribing"
            )
        if self._transcriber is None:
            raise LifecycleError("the model is not loaded; start the Server through the Admin API")
        return self._transcriber

    def start(self) -> Transcriber:
        """Load the model and clear the soft-stop. Idempotent while loaded."""
        if self._transcriber is not None and not self._stopped:
            return self._transcriber
        try:
            transcriber = self._load()
        except Exception as exc:
            raise LifecycleError(str(exc)) from exc
        self._transcriber = transcriber
        self._stopped = False
        self._persist(False)
        return transcriber

    def stop(self) -> None:
        """Release the model and remember the soft-stop. Idempotent."""
        transcriber = self._transcriber
        self._transcriber = None
        try:
            if transcriber is not None:
                self._unload(transcriber)
        finally:
            # Even a failing unload must not leave the Server claiming to be
            # loaded and ready.
            self._stopped = True
            self._persist(True)

    def transcribe(
        self,
        clip: AudioClip,
        *,
        language: str | None = None,
        initial_prompt: str | None = None,
    ) -> Transcription:
        """Delegate to the loaded model, so a Lifecycle is a ``Transcriber``."""
        return self.transcriber.transcribe(
            clip,
            language=language,
            initial_prompt=initial_prompt,
        )

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
    "LifecycleError",
    "lifecycle_state_path",
]
