"""The Model catalog and the online model lifecycle.

The catalog metadata is pinned with the release (disk size, memory estimate,
speed/quality hint) and can be overridden with ``server.catalog_file``.
Downloads are serialised through one worker, report progress, can be
cancelled, and are checked against a pinned checksum when the source supplies
one. Activation loads the new model before swapping it in, so a failed load
leaves the previous model serving; deleting the active model is refused. The
model is unloaded after ``idle_unload_seconds`` (``0`` disables unloading) and
reloaded lazily on the next request.

Every external effect is a port (:class:`ModelLoader`,
:class:`ModelDownloader`, :class:`ModelStorage`, and the clock), so the HTTP
tests never download, load, or touch a real model.
"""

from __future__ import annotations

import hashlib
import logging
import re
import shutil
import threading
import time
from collections.abc import Callable, Mapping, Sequence
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Literal, Protocol

import httpx
import yaml

from .config import ConfigError, ServerConfig
from .gpu import resolve_engine
from .transcriber import Transcriber

logger = logging.getLogger("kitsune.server")

ModelState = Literal["loaded", "cold", "loading", "failed"]
DownloadState = Literal["queued", "downloading", "verifying", "done", "cancelled", "failed"]
SpeedHint = Literal["fast", "balanced", "slow"]
QualityHint = Literal["basic", "good", "excellent"]

ACTIVE_DOWNLOAD_STATES = ("queued", "downloading", "verifying")


class ModelError(Exception):
    """A model operation the Admin API can report through the error envelope."""

    code = "model_error"


class UnknownModelError(ModelError):
    code = "unknown_model"


class ModelNotDownloadedError(ModelError):
    code = "model_not_downloaded"


class AlreadyInstalledError(ModelError):
    code = "model_already_installed"


class ModelActiveError(ModelError):
    code = "model_active"


class ModelBusyError(ModelError):
    code = "model_busy"


class ModelOfflineError(ModelError):
    code = "model_offline"


class DownloadCancelledError(ModelError):
    code = "download_cancelled"


class DownloadIntegrityError(ModelError):
    code = "model_checksum_failed"


class ModelLoadError(ModelError):
    code = "model_load_failed"


class CatalogError(ConfigError):
    """The override catalog file is missing or invalid."""


@dataclass(frozen=True)
class ModelSpec:
    """One catalog entry, with the metadata a user chooses a model by."""

    name: str
    disk_bytes: int
    memory_bytes: int
    speed: SpeedHint
    quality: QualityHint
    sha256: str = ""

    def as_dict(self) -> dict[str, Any]:
        return {
            "name": self.name,
            "disk_bytes": self.disk_bytes,
            "memory_bytes": self.memory_bytes,
            "speed": self.speed,
            "quality": self.quality,
        }


# Sizes are the faster-whisper CTranslate2 downloads (approximate, rounded to
# a megabyte) and the memory estimate is the resident model plus a decode
# buffer. Hints are stable keys the Client translates.
PINNED_CATALOG: tuple[ModelSpec, ...] = (
    ModelSpec("tiny", 75_000_000, 450_000_000, "fast", "basic"),
    ModelSpec("base", 145_000_000, 600_000_000, "fast", "good"),
    ModelSpec("small", 484_000_000, 1_300_000_000, "balanced", "good"),
    ModelSpec("medium", 1_530_000_000, 3_000_000_000, "slow", "excellent"),
    ModelSpec("large-v3", 3_090_000_000, 5_200_000_000, "slow", "excellent"),
)

RECOMMENDATIONS: Mapping[str, str] = {"cuda": "medium", "cpu": "base"}


@dataclass(frozen=True)
class ModelCatalog:
    """The pinned catalog, or the metadata read from ``server.catalog_file``."""

    models: tuple[ModelSpec, ...]

    @classmethod
    def load(cls, config: ServerConfig) -> ModelCatalog:
        if not config.catalog_file:
            return cls(PINNED_CATALOG)
        path = Path(config.catalog_file).expanduser()
        try:
            raw = path.read_text(encoding="utf-8")
        except OSError as exc:
            raise CatalogError(f"cannot read the model catalog {path}: {exc}") from exc
        try:
            document = yaml.safe_load(raw)
        except yaml.YAMLError as exc:
            raise CatalogError(f"invalid YAML in the model catalog {path}: {exc}") from exc
        entries = document.get("models") if isinstance(document, Mapping) else document
        if not isinstance(entries, Sequence) or isinstance(entries, (str, bytes)) or not entries:
            raise CatalogError(f"{path}: the model catalog must list at least one model")
        models: list[ModelSpec] = []
        for entry in entries:
            if not isinstance(entry, Mapping):
                raise CatalogError(f"{path}: every catalog entry must be a mapping")
            try:
                speed = str(entry["speed"])
                quality = str(entry["quality"])
                if speed not in ("fast", "balanced", "slow"):
                    raise CatalogError(f"{path}: invalid speed hint {speed!r}")
                if quality not in ("basic", "good", "excellent"):
                    raise CatalogError(f"{path}: invalid quality hint {quality!r}")
                spec = ModelSpec(
                    name=str(entry["name"]),
                    disk_bytes=int(entry["disk_bytes"]),
                    memory_bytes=int(entry["memory_bytes"]),
                    speed=speed,  # type: ignore[arg-type]
                    quality=quality,  # type: ignore[arg-type]
                    sha256=str(entry.get("sha256") or ""),
                )
            except (KeyError, TypeError, ValueError) as exc:
                raise CatalogError(f"{path}: invalid catalog entry: {exc}") from exc
            if not spec.name or spec.disk_bytes <= 0 or spec.memory_bytes <= 0:
                raise CatalogError(f"{path}: every model needs a name and positive sizes")
            if "/" in spec.name or "\\" in spec.name or spec.name in (".", ".."):
                raise CatalogError(
                    f"{path}: a catalog model name must be a plain file name, not {spec.name!r}"
                )
            models.append(spec)
        return cls(tuple(models))

    def spec(self, name: str) -> ModelSpec | None:
        for model in self.models:
            if model.name == name:
                return model
        return None

    def require(self, name: str) -> ModelSpec:
        spec = self.spec(name)
        if spec is None:
            known = ", ".join(model.name for model in self.models)
            raise UnknownModelError(f"no model named {name!r}; the catalog has {known}")
        return spec

    def recommendation(self, device: str) -> ModelSpec:
        name = RECOMMENDATIONS.get(device, "base")
        return self.spec(name) or self.models[0]


# -- ports -----------------------------------------------------------------


class ModelLoader(Protocol):
    """Loads the model named by ``config.model`` and returns a Transcriber."""

    def load(self, config: ServerConfig) -> Transcriber: ...


class ModelDownloader(Protocol):
    """Fetches one model into ``target`` and reports bytes as they arrive."""

    def fetch(
        self,
        spec: ModelSpec,
        target: Path,
        report: Callable[[int, int], None],
        cancelled: Callable[[], bool],
    ) -> DownloadedModel: ...


class ModelStorage(Protocol):
    """Where downloaded models live and how much space they use."""

    def path(self, name: str) -> Path: ...

    def installed(self, name: str) -> bool: ...

    def size(self, name: str) -> int: ...

    def delete(self, name: str) -> int: ...


class Scheduler(Protocol):
    def submit(self, work: Callable[[], None]) -> None: ...


@dataclass(frozen=True)
class DownloadedModel:
    """The result of a download; ``sha256`` is empty when the source has none."""

    path: Path
    sha256: str = ""


class ThreadScheduler:
    """Runs each download in its own daemon thread."""

    def submit(self, work: Callable[[], None]) -> None:
        threading.Thread(target=work, name="kitsune-model-download", daemon=True).start()


class FileModelStorage:
    """Downloads live under ``server.download_root/<model>``."""

    def __init__(self, root: str | Path) -> None:
        self.root = Path(root).expanduser()

    def path(self, name: str) -> Path:
        return self.root / name

    def installed(self, name: str) -> bool:
        directory = self.path(name)
        return (directory / "model.bin").is_file() and (directory / "config.json").is_file()

    def size(self, name: str) -> int:
        directory = self.path(name)
        if not directory.is_dir():
            return 0
        total = 0
        for child in directory.rglob("*"):
            try:
                if child.is_file():
                    total += child.stat().st_size
            except OSError:
                continue
        return total

    def delete(self, name: str) -> int:
        directory = self.path(name)
        if not directory.is_dir():
            return 0
        freed = self.size(name)
        shutil.rmtree(directory, ignore_errors=True)
        return freed


class WhisperLoader:
    """The production loader: builds the faster-whisper adapter."""

    def __init__(self, *, cuda_device_count: int = 0) -> None:
        self._cuda_device_count = cuda_device_count

    def load(self, config: ServerConfig) -> Transcriber:
        from .engine import WhisperTranscriber

        return WhisperTranscriber.load(config, cuda_device_count=self._cuda_device_count)


class HttpModelDownloader:
    """Download CTranslate2 files from the pinned Hugging Face repositories.

    The Hub reports a SHA-256 ETag for its LFS blobs; we forward it as the
    downloaded digest so the manager can verify a pinned checksum when one is
    configured.
    """

    FILES = ("config.json", "model.bin", "tokenizer.json", "vocabulary.txt")
    REQUIRED = ("config.json", "model.bin")
    BASE = "https://huggingface.co/Systran/faster-whisper-{name}/resolve/main/{file}"

    def __init__(self, *, timeout: float = 60.0) -> None:
        self._timeout = timeout

    def fetch(
        self,
        spec: ModelSpec,
        target: Path,
        report: Callable[[int, int], None],
        cancelled: Callable[[], bool],
    ) -> DownloadedModel:
        if not spec.name:
            raise DownloadIntegrityError("the model has no name")
        target.mkdir(parents=True, exist_ok=True)
        totals: dict[str, int] = {}
        received = 0
        digest = ""
        with httpx.Client(timeout=self._timeout, follow_redirects=True) as client:
            for name in self.FILES:
                if cancelled():
                    raise DownloadCancelledError("the download was cancelled")
                url = self.BASE.format(name=spec.name, file=name)
                with client.stream("GET", url) as response:
                    if response.status_code == 404 and name not in self.REQUIRED:
                        continue
                    response.raise_for_status()
                    length = int(response.headers.get("content-length") or 0)
                    totals[name] = length
                    destination = target / name
                    digest = ""
                    checksum = hashlib.sha256()
                    with destination.open("wb") as handle:
                        for chunk in response.iter_bytes():
                            if cancelled():
                                raise DownloadCancelledError("the download was cancelled")
                            handle.write(chunk)
                            checksum.update(chunk)
                            received += len(chunk)
                            report(received, sum(totals.values()))
                    if name == "model.bin":
                        etag = (response.headers.get("x-linked-etag") or "").strip('"')
                        if re.fullmatch(r"[0-9a-fA-F]{64}", etag):
                            if etag.lower() != checksum.hexdigest():
                                raise DownloadIntegrityError(
                                    "the downloaded model.bin does not match the checksum "
                                    "the source reported"
                                )
                            digest = etag.lower()
                        else:
                            digest = checksum.hexdigest()
        return DownloadedModel(path=target, sha256=digest)


@dataclass
class _Download:
    """Mutable progress for one queued or running download."""

    model: str
    state: DownloadState = "queued"
    received_bytes: int = 0
    total_bytes: int = 0
    error: str = ""
    cancel_event: threading.Event = field(default_factory=threading.Event)

    def as_dict(self) -> dict[str, Any]:
        return {
            "model": self.model,
            "state": self.state,
            "received_bytes": self.received_bytes,
            "total_bytes": self.total_bytes,
            "error": self.error,
        }

    @property
    def active(self) -> bool:
        return self.state in ACTIVE_DOWNLOAD_STATES


class ModelManager:
    """Owns the active Transcriber, the catalog, downloads, and idle unload."""

    def __init__(
        self,
        config: ServerConfig,
        *,
        transcriber: Transcriber | None = None,
        loader: ModelLoader | None = None,
        downloader: ModelDownloader | None = None,
        storage: ModelStorage | None = None,
        catalog: ModelCatalog | None = None,
        clock: Callable[[], float] = time.monotonic,
        scheduler: Scheduler | None = None,
        cuda_device_count: int = 0,
    ) -> None:
        self.config = config
        self._device = resolve_engine(config, cuda_device_count=cuda_device_count).device
        self._catalog = catalog or ModelCatalog.load(config)
        self._loader = loader or WhisperLoader(cuda_device_count=cuda_device_count)
        self._downloader = downloader or HttpModelDownloader()
        self._storage = storage or FileModelStorage(config.download_root)
        self._scheduler = scheduler or ThreadScheduler()
        self._clock = clock
        self._lock = threading.RLock()
        self._transcriber = transcriber
        self._active_model = config.model
        self._active_path = config.model if _looks_like_path(config.model) else ""
        self._state: ModelState = "loaded" if transcriber is not None else "cold"
        self._error = ""
        self._idle = int(config.idle_unload_seconds)
        self._last_activity = clock()
        self._downloads: dict[str, _Download] = {}
        self._queue: list[str] = []
        self._working = False
        self._monitor_stop = threading.Event()
        self._monitor_started = False

    # -- read-only state ---------------------------------------------------

    @property
    def catalog(self) -> ModelCatalog:
        return self._catalog

    @property
    def state(self) -> ModelState:
        with self._lock:
            return self._state

    @property
    def active_model(self) -> str:
        with self._lock:
            return self._active_model

    @property
    def active_path(self) -> str:
        with self._lock:
            return self._active_path

    @property
    def idle_unload_seconds(self) -> int:
        with self._lock:
            return self._idle

    def current_transcriber(self) -> Transcriber | None:
        with self._lock:
            return self._transcriber

    def view(self) -> dict[str, Any]:
        """The whole Models screen state in one immutable snapshot."""
        with self._lock:
            recommendation = self._catalog.recommendation(self._device)
            models = []
            for spec in self._catalog.models:
                download = self._downloads.get(spec.name)
                models.append(
                    {
                        **spec.as_dict(),
                        "recommended": spec.name == recommendation.name,
                        "installed": self._storage.installed(spec.name),
                        "installed_bytes": self._storage.size(spec.name),
                        "download": download.as_dict() if download is not None else None,
                    }
                )
            return {
                "device": self._device,
                "recommended": recommendation.name,
                "state": self._state,
                "active_model": self._active_model,
                "active_path": self._active_path,
                "error": self._error,
                "idle_unload_seconds": self._idle,
                "models": models,
            }

    # -- downloads ---------------------------------------------------------

    def start_download(self, name: str) -> _Download:
        if self.config.offline:
            raise ModelOfflineError(
                "this Server is configured offline; set server.offline=false to download models"
            )
        spec = self._catalog.require(name)
        with self._lock:
            if self._storage.installed(name):
                raise AlreadyInstalledError(f"{name} is already downloaded")
            existing = self._downloads.get(name)
            if existing is not None and existing.active:
                return existing
            record = _Download(name, total_bytes=spec.disk_bytes)
            self._downloads[name] = record
            self._queue.append(name)
            if not self._working:
                self._working = True
                self._scheduler.submit(self._drain)
            return record

    def cancel_download(self, name: str) -> _Download:
        with self._lock:
            record = self._downloads.get(name)
            if record is None or not record.active:
                raise UnknownModelError(f"no download of {name!r} is in progress")
            record.cancel_event.set()
            if record.state == "queued":
                record.state = "cancelled"
                if name in self._queue:
                    self._queue.remove(name)
            return record

    def _drain(self) -> None:
        """One worker serialises every download until the queue is empty."""
        while True:
            with self._lock:
                while self._queue:
                    name = self._queue[0]
                    record = self._downloads[name]
                    if record.state != "queued":
                        self._queue.pop(0)
                        continue
                    self._queue.pop(0)
                    break
                else:
                    self._working = False
                    return
                record.state = "downloading"
            self._fetch(record)

    def _fetch(self, record: _Download) -> None:
        spec = self._catalog.require(record.model)
        target = self._storage.path(record.model)
        try:
            downloaded = self._downloader.fetch(
                spec,
                target,
                lambda received, total: self._progress(record, received, total),
                record.cancel_event.is_set,
            )
            if record.cancel_event.is_set():
                raise DownloadCancelledError("the download was cancelled")
            self._set_state(record, "verifying")
            self._verify(spec, downloaded)
            if record.cancel_event.is_set():
                raise DownloadCancelledError("the download was cancelled")
            with self._lock:
                record.state = "done"
                record.received_bytes = self._storage.size(record.model)
                record.total_bytes = record.received_bytes or record.total_bytes
                record.error = ""
        except DownloadCancelledError as exc:
            self._cleanup(record, exc)
        except Exception as exc:
            self._cleanup(record, exc)

    def _progress(self, record: _Download, received: int, total: int) -> None:
        with self._lock:
            if record.state != "downloading":
                return
            record.received_bytes = received
            if total > 0:
                record.total_bytes = total

    def _set_state(self, record: _Download, state: DownloadState) -> None:
        with self._lock:
            record.state = state

    def _cleanup(self, record: _Download, exc: Exception) -> None:
        try:
            self._storage.delete(record.model)
        except OSError:
            logger.warning("could not remove the failed download of %s", record.model)
        with self._lock:
            record.state = "cancelled" if isinstance(exc, DownloadCancelledError) else "failed"
            record.error = "" if isinstance(exc, DownloadCancelledError) else str(exc)
        if not isinstance(exc, DownloadCancelledError):
            logger.warning("model download %s failed: %s", record.model, exc)

    def _verify(self, spec: ModelSpec, downloaded: DownloadedModel) -> None:
        if not spec.sha256:
            return
        if not downloaded.sha256:
            raise DownloadIntegrityError(
                f"the source provided no checksum for {spec.name}; cannot verify the download"
            )
        if downloaded.sha256.lower() != spec.sha256.lower():
            raise DownloadIntegrityError(
                f"the checksum of {spec.name} does not match the catalog "
                f"(expected {spec.sha256[:12]}…, got {downloaded.sha256[:12]}…)"
            )

    # -- activation --------------------------------------------------------

    def resolve_reference(self, name: str) -> tuple[str, str, str]:
        """Resolve a catalog name to (label, reference, path) or validate a local path."""
        if _looks_like_path(name):
            directory = Path(name).expanduser()
            if not directory.is_dir():
                raise ModelNotDownloadedError(f"the model directory {directory} does not exist")
            return str(directory), str(directory), str(directory)
        self._catalog.require(name)
        if not self._storage.installed(name):
            raise ModelNotDownloadedError(
                f"{name} is not downloaded yet; download it before activating it"
            )
        return name, str(self._storage.path(name)), ""

    def activate(self, name: str) -> None:
        """Load ``name`` and swap it in, keeping the previous model on failure."""
        label, reference, path = self.resolve_reference(name)
        with self._lock:
            self._state = "loading"
            self._error = ""
            previous_error = ""
        candidate = self.config.model_copy(update={"model": reference})
        try:
            loaded = self._loader.load(candidate)
        except Exception as exc:
            previous_error = f"could not load {label}: {exc}"
            with self._lock:
                self._state = "failed"
                self._error = previous_error
            raise ModelLoadError(previous_error) from exc
        with self._lock:
            self._transcriber = loaded
            self._active_model = label
            self._active_path = path
            self._state = "loaded"
            self._error = ""
            self._last_activity = self._clock()

    # -- deletion ----------------------------------------------------------

    def delete(self, name: str) -> int:
        # Only catalog names may be deleted, so a crafted path cannot escape the
        # download root through the Admin API.
        self._catalog.require(name)
        with self._lock:
            if name and name == self._active_model:
                raise ModelActiveError(
                    f"{name} is the active model; activate another model before deleting it"
                )
            record = self._downloads.get(name)
            if record is not None and record.active:
                raise ModelBusyError(f"{name} is still downloading")
            if not self._storage.installed(name):
                raise UnknownModelError(f"{name} is not downloaded")
            freed = self._storage.delete(name)
            self._downloads.pop(name, None)
            return freed

    # -- idle unload -------------------------------------------------------

    def touch(self) -> None:
        with self._lock:
            self._last_activity = self._clock()

    def set_idle_unload_seconds(self, seconds: int) -> None:
        if seconds < 0:
            raise ValueError("idle unload seconds must be zero or positive")
        with self._lock:
            self._idle = seconds

    def maybe_unload(self) -> bool:
        """Unload the model when it has been idle long enough.

        A failed activation leaves the previous model serving with state
        ``failed``; that model must still be unloaded when it goes idle.
        """
        with self._lock:
            if self._idle <= 0 or self._state == "loading" or self._transcriber is None:
                return False
            if self._clock() - self._last_activity < self._idle:
                return False
            self._transcriber = None
            self._state = "cold"
            self._error = ""
            logger.info("unloaded %s after %ss idle", self._active_model, self._idle)
            return True

    def ensure_loaded(self) -> Transcriber:
        """Return the active Transcriber, loading it after an idle unload."""
        with self._lock:
            if self._transcriber is not None:
                self._last_activity = self._clock()
                return self._transcriber
            label = self._active_model
            if not label:
                self._state = "failed"
                self._error = "no model is configured"
                raise ModelLoadError(self._error)
            self._state = "loading"
            self._error = ""
        try:
            _, reference, _ = self.resolve_reference(label)
            loaded = self._loader.load(self.config.model_copy(update={"model": reference}))
        except ModelError as exc:
            with self._lock:
                self._state = "failed"
                self._error = str(exc)
            raise ModelLoadError(str(exc)) from exc
        except Exception as exc:
            message = f"could not load {label}: {exc}"
            with self._lock:
                self._state = "failed"
                self._error = message
            raise ModelLoadError(message) from exc
        with self._lock:
            self._transcriber = loaded
            self._state = "loaded"
            self._error = ""
            self._last_activity = self._clock()
            return loaded

    def start_idle_monitor(self, *, interval: float = 1.0) -> None:
        """Unload the model in the background once it has been idle."""
        with self._lock:
            if self._monitor_started:
                return
            self._monitor_started = True

        def loop() -> None:
            while not self._monitor_stop.wait(interval):
                try:
                    self.maybe_unload()
                except Exception:  # a monitor bug must not stop the Server
                    logger.exception("idle unload failed")

        threading.Thread(target=loop, name="kitsune-idle-monitor", daemon=True).start()

    def stop_idle_monitor(self) -> None:
        self._monitor_stop.set()


def _looks_like_path(value: str) -> bool:
    return "/" in value or "\\" in value or value in (".", "..") or Path(value).is_absolute()
