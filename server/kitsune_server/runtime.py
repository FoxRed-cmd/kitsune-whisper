"""The one model-runtime seam shared by transcription and the Admin API.

Before this module the HTTP app branched on "is a ModelManager present?" in
``/status``, ``/transcribe``, ``/admin/stop``, and ``/admin/start``, and a
``Lifecycle`` could stand in for a ``Transcriber``. That gave the same process
two ways to own a model. Now there is one protocol:

* :class:`ModelManager` owns the real lifecycle (lazy load, online activation,
  idle unload) and implements the protocol directly.
* :class:`FixedModelRuntime` wraps a fixed Transcriber for tests and throwaway
  Servers that never manage models. It is the only adapter that keeps a model
  alive across a soft-stop.

:class:`~kitsune_server.lifecycle.Lifecycle` is only the persisted soft-stop
flag. Every endpoint asks the runtime for one :class:`ModelSnapshot`, so
``/status``, ``/admin/stop``, ``/admin/start``, and ``/admin/restart`` cannot
disagree about the model, its state, or where it lives.
"""

from __future__ import annotations

from typing import Protocol

from .config import ServerConfig
from .lifecycle import Lifecycle
from .models import ModelManager, ModelSnapshot, ModelStoppedError
from .transcriber import EngineInfo, Transcriber


class ModelRuntime(Protocol):
    """One owner for the active model and its soft-stop state."""

    @property
    def soft_stop(self) -> Lifecycle: ...

    def info(self) -> EngineInfo: ...

    def snapshot(self) -> ModelSnapshot: ...

    def transcriber(self) -> Transcriber: ...

    def start(self) -> None: ...

    def stop(self) -> None: ...

    def touch(self) -> None: ...


class FixedModelRuntime:
    """A model that is loaded for the process lifetime.

    This is the adapter for tests and for Servers built without a
    ModelManager. A soft-stop only flips the persisted flag: releasing the
    fixed Transcriber would leave nothing to start again. Production always
    uses a :class:`ModelManager`, where a stop genuinely unloads.
    """

    def __init__(
        self,
        transcriber: Transcriber,
        *,
        soft_stop: Lifecycle | None = None,
        idle_unload_seconds: int = 0,
    ) -> None:
        self._transcriber = transcriber
        self._soft_stop = soft_stop if soft_stop is not None else Lifecycle()
        self._idle = max(0, int(idle_unload_seconds))

    @property
    def soft_stop(self) -> Lifecycle:
        return self._soft_stop

    def info(self) -> EngineInfo:
        return self._transcriber.info

    def snapshot(self) -> ModelSnapshot:
        stopped = self._soft_stop.stopped
        return ModelSnapshot(
            model=self._transcriber.info.model,
            state="stopped" if stopped else "loaded",
            idle_unload_seconds=self._idle,
            stopped=stopped,
            loaded=not stopped,
        )

    def transcriber(self) -> Transcriber:
        if self._soft_stop.stopped:
            raise ModelStoppedError(
                "the Server is soft-stopped; start it through the Admin API before transcribing"
            )
        return self._transcriber

    def start(self) -> None:
        self._soft_stop.clear()

    def stop(self) -> None:
        self._soft_stop.mark_stopped()

    def touch(self) -> None:
        """A fixed model has no idle timer."""


def build_runtime(
    config: ServerConfig,
    transcriber: Transcriber | None,
    *,
    models: ModelManager | None = None,
    soft_stop: Lifecycle | None = None,
) -> ModelRuntime:
    """Choose the one runtime for this process.

    ``models`` wins whenever it is present, because the manager already owns
    the loaded model and its soft-stop flag. Without one, the fixed adapter
    wraps the positional Transcriber so existing tests keep working.
    """
    if models is not None:
        return models
    if transcriber is None:
        raise ValueError("create_app needs either a Transcriber or a ModelManager")
    return FixedModelRuntime(
        transcriber,
        soft_stop=soft_stop,
        idle_unload_seconds=config.idle_unload_seconds,
    )


__all__ = [
    "FixedModelRuntime",
    "ModelRuntime",
    "ModelSnapshot",
    "ModelStoppedError",
    "build_runtime",
]
