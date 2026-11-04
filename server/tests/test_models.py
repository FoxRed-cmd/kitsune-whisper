from __future__ import annotations

import asyncio
import threading
import time
from dataclasses import dataclass
from pathlib import Path
from types import SimpleNamespace
from typing import Any

import pytest
import yaml
from conftest import make_client, make_wav
from kitsune_server.app import create_app
from kitsune_server.config import ConfigError, ServerConfig
from kitsune_server.models import (
    AlreadyInstalledError,
    CatalogError,
    DownloadCancelledError,
    DownloadedModel,
    FileModelStorage,
    ModelCatalog,
    ModelLoadError,
    ModelManager,
    ModelNotDownloadedError,
    UnknownModelError,
)
from kitsune_server.processor import DisabledTextProcessor
from kitsune_server.transcriber import EngineInfo, FakeTranscriber


class FakeClock:
    def __init__(self, now: float = 1000.0) -> None:
        self.now = now

    def __call__(self) -> float:
        return self.now

    def advance(self, seconds: float) -> None:
        self.now += seconds


class FakeLoader:
    """Records loads and can fail for a named model."""

    def __init__(self, *, failures: dict[str, Exception] | None = None) -> None:
        self.calls: list[str] = []
        self.failures = failures or {}

    def load(self, config: ServerConfig) -> FakeTranscriber:
        self.calls.append(config.model)
        failure = self.failures.get(config.model) or self.failures.get(Path(config.model).name)
        if failure is not None:
            raise failure
        name = Path(config.model).name or config.model
        return FakeTranscriber(info=EngineInfo(name, "cpu", "int8"))


class FakeStorage:
    """An in-memory stand-in for the model directory."""

    def __init__(self, installed: dict[str, int] | None = None, root: Path | None = None) -> None:
        self.installed_sizes = dict(installed or {})
        self.root = root or Path("/models")
        self.deleted: list[str] = []

    def path(self, name: str) -> Path:
        return self.root / name

    def installed(self, name: str) -> bool:
        return name in self.installed_sizes

    def size(self, name: str) -> int:
        return self.installed_sizes.get(name, 0)

    def delete(self, name: str) -> int:
        self.deleted.append(name)
        return self.installed_sizes.pop(name, 0)


class FakeDownloader:
    """A controlled downloader: each model waits on a gate, then succeeds or fails."""

    def __init__(self) -> None:
        self.lock = threading.Lock()
        self.gates: dict[str, threading.Event] = {}
        self.started: dict[str, threading.Event] = {}
        self.failures: dict[str, Exception] = {}
        self.digests: dict[str, str] = {}
        self.received: dict[str, int] = {}
        self.active = 0
        self.max_active = 0

    def gate(self, name: str) -> threading.Event:
        return self.gates.setdefault(name, threading.Event())

    def started_event(self, name: str) -> threading.Event:
        return self.started.setdefault(name, threading.Event())

    def fetch(
        self,
        spec: Any,
        target: Path,
        report: Any,
        cancelled: Any,
    ) -> DownloadedModel:
        with self.lock:
            self.active += 1
            self.max_active = max(self.max_active, self.active)
        try:
            self.received[spec.name] = spec.disk_bytes // 2
            report(spec.disk_bytes // 2, spec.disk_bytes)
            self.started_event(spec.name).set()
            if not self.gate(spec.name).wait(timeout=10):
                raise RuntimeError("download gate timed out")
            if cancelled():
                raise DownloadCancelledError()
            failure = self.failures.get(spec.name)
            if failure is not None:
                raise failure
            report(spec.disk_bytes, spec.disk_bytes)
            return DownloadedModel(path=target, sha256=self.digests.get(spec.name, ""))
        finally:
            with self.lock:
                self.active -= 1


@dataclass
class FakeConfigStore:
    changes: list[dict[str, Any]]

    def patch(self, changes: dict[str, Any]) -> Any:
        self.changes.append(dict(changes))
        return SimpleNamespace(written=True, changed=tuple(changes), restart_required=())


def build_manager(
    *,
    config: ServerConfig | None = None,
    transcriber: FakeTranscriber | None = None,
    loader: FakeLoader | None = None,
    downloader: FakeDownloader | None = None,
    storage: FakeStorage | None = None,
    clock: FakeClock | None = None,
) -> ModelManager:
    config = config or ServerConfig(workers=1, model="small", idle_unload_seconds=600)
    return ModelManager(
        config,
        transcriber=transcriber,
        loader=loader or FakeLoader(),
        downloader=downloader or FakeDownloader(),
        storage=storage or FakeStorage(installed={"small": 10}),
        clock=clock or FakeClock(),
        cuda_device_count=0,
    )


def build_app(
    manager: ModelManager,
    *,
    config: ServerConfig | None = None,
    config_store: Any = None,
) -> Any:
    config = config or manager.config
    return create_app(
        config,
        manager.current_transcriber() or FakeTranscriber(),
        DisabledTextProcessor(),
        cuda_device_count=0,
        models=manager,
        config_store=config_store,
    )


def wait_for(predicate: Any, *, timeout: float = 5.0) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.01)
    raise AssertionError("condition was not met before the timeout")


def model_entry(manager: ModelManager, name: str) -> dict[str, Any]:
    return next(item for item in manager.view()["models"] if item["name"] == name)


# -- catalog ---------------------------------------------------------------


def test_catalog_has_the_five_classic_models_with_metadata() -> None:
    catalog = ModelCatalog.load(ServerConfig())

    assert [spec.name for spec in catalog.models] == ["tiny", "base", "small", "medium", "large-v3"]
    for spec in catalog.models:
        assert spec.disk_bytes > 0
        assert spec.memory_bytes > 0
        assert spec.speed in {"fast", "balanced", "slow"}
        assert spec.quality in {"basic", "good", "excellent"}


def test_catalog_recommends_by_resolved_device() -> None:
    catalog = ModelCatalog.load(ServerConfig())

    assert catalog.recommendation("cpu").name == "base"
    assert catalog.recommendation("cuda").name == "medium"


def test_catalog_file_overrides_the_pinned_metadata(tmp_path: Path) -> None:
    path = tmp_path / "catalog.yaml"
    path.write_text(
        yaml.safe_dump(
            {
                "models": [
                    {
                        "name": "custom",
                        "disk_bytes": 1000,
                        "memory_bytes": 2000,
                        "speed": "fast",
                        "quality": "good",
                        "sha256": "abc",
                    }
                ]
            }
        ),
        encoding="utf-8",
    )

    catalog = ModelCatalog.load(ServerConfig(catalog_file=str(path)))

    assert [spec.name for spec in catalog.models] == ["custom"]
    assert catalog.models[0].sha256 == "abc"


def test_catalog_view_reports_recommendation_and_installation(
    tmp_path: Path,
) -> None:
    manager = build_manager(
        storage=FakeStorage(installed={"small": 10, "base": 20}),
    )
    view = manager.view()

    assert view["device"] == "cpu"
    assert view["recommended"] == "base"
    assert view["state"] == "cold"
    by_name = {item["name"]: item for item in view["models"]}
    assert by_name["base"]["recommended"] is True
    assert by_name["base"]["installed"] is True
    assert by_name["tiny"]["installed"] is False
    assert by_name["tiny"]["download"] is None


# -- downloads -------------------------------------------------------------


async def test_download_reports_progress_and_serialises_models() -> None:
    downloader = FakeDownloader()
    storage = FakeStorage(installed={"small": 10})
    manager = build_manager(downloader=downloader, storage=storage)
    app = build_app(manager)

    async with make_client(app) as client:
        first = await client.post("/admin/models/download", json={"model": "base"})
        assert first.status_code == 202
        wait_for(downloader.started_event("base").is_set)

        progress = (await client.get("/admin/models")).json()
        base = next(item for item in progress["models"] if item["name"] == "base")
        assert base["download"]["state"] == "downloading"
        assert base["download"]["received_bytes"] == base["disk_bytes"] // 2
        assert base["download"]["total_bytes"] == base["disk_bytes"]

        second = await client.post("/admin/models/download", json={"model": "tiny"})
        assert second.status_code == 202
        queued = (await client.get("/admin/models")).json()
        tiny = next(item for item in queued["models"] if item["name"] == "tiny")
        assert tiny["download"]["state"] == "queued"

        downloader.gate("base").set()
        wait_for(downloader.started_event("tiny").is_set)
        downloader.gate("tiny").set()
        wait_for(
            lambda: (
                model_entry(manager, "base")["download"] is not None
                and model_entry(manager, "tiny")["download"] is not None
                and model_entry(manager, "base")["download"]["state"] == "done"
                and model_entry(manager, "tiny")["download"]["state"] == "done"
            )
        )

    assert downloader.max_active == 1


def test_a_download_while_soft_stopped_does_not_load_the_model() -> None:
    from kitsune_server.lifecycle import Lifecycle

    downloader = FakeDownloader()
    downloader.gate("base").set()
    loader = FakeLoader()
    soft_stop = Lifecycle(stopped=True)
    manager = ModelManager(
        ServerConfig(workers=1, model="small"),
        loader=loader,
        downloader=downloader,
        storage=FakeStorage(),
        soft_stop=soft_stop,
    )

    manager.start_download("base")
    wait_for(lambda: model_entry(manager, "base")["download"]["state"] == "done")

    assert loader.calls == []
    assert manager.current_transcriber() is None
    assert manager.snapshot().state == "stopped"
    assert soft_stop.stopped is True


def test_a_download_does_not_replace_an_active_model() -> None:
    downloader = FakeDownloader()
    downloader.gate("base").set()
    loader = FakeLoader()
    manager = build_manager(
        transcriber=FakeTranscriber(info=EngineInfo("small", "cpu", "int8")),
        loader=loader,
        downloader=downloader,
        storage=FakeStorage(installed={"small": 10}),
    )

    manager.start_download("base")
    wait_for(lambda: model_entry(manager, "base")["download"]["state"] == "done")

    assert manager.active_model == "small"
    assert manager.current_transcriber() is not None
    assert loader.calls == []


async def test_cancel_stops_a_running_download() -> None:
    downloader = FakeDownloader()
    manager = build_manager(downloader=downloader)
    app = build_app(manager)

    async with make_client(app) as client:
        await client.post("/admin/models/download", json={"model": "base"})
        wait_for(downloader.started_event("base").is_set)

        response = await client.post("/admin/models/cancel", json={"model": "base"})
        assert response.status_code == 200
        downloader.gate("base").set()
        wait_for(lambda: model_entry(manager, "base")["download"]["state"] == "cancelled")

    view = manager.view()
    base = next(item for item in view["models"] if item["name"] == "base")
    assert base["installed"] is False


async def test_download_failure_is_reported_and_cleaned_up() -> None:
    downloader = FakeDownloader()
    downloader.failures["base"] = RuntimeError("network down")
    storage = FakeStorage(installed={"small": 10})
    manager = build_manager(downloader=downloader, storage=storage)
    app = build_app(manager)

    async with make_client(app) as client:
        await client.post("/admin/models/download", json={"model": "base"})
        wait_for(downloader.started_event("base").is_set)
        downloader.gate("base").set()
        wait_for(lambda: model_entry(manager, "base")["download"]["state"] == "failed")

    base = next(item for item in manager.view()["models"] if item["name"] == "base")
    assert "network down" in base["download"]["error"]
    assert "base" in storage.deleted


def test_download_with_a_pinned_hash_rejects_a_mismatch(tmp_path: Path) -> None:
    catalog = tmp_path / "catalog.yaml"
    catalog.write_text(
        yaml.safe_dump(
            {
                "models": [
                    {
                        "name": "custom",
                        "disk_bytes": 10,
                        "memory_bytes": 20,
                        "speed": "fast",
                        "quality": "good",
                        "sha256": "expected",
                    }
                ]
            }
        ),
        encoding="utf-8",
    )
    downloader = FakeDownloader()
    downloader.digests["custom"] = "different"
    downloader.gate("custom").set()
    storage = FakeStorage()
    manager = build_manager(
        config=ServerConfig(workers=1, catalog_file=str(catalog)),
        downloader=downloader,
        storage=storage,
    )

    manager.start_download("custom")
    wait_for(lambda: model_entry(manager, "custom")["download"]["state"] == "failed")

    item = model_entry(manager, "custom")
    assert "checksum" in item["download"]["error"].lower()
    assert item["installed"] is False
    assert "custom" in storage.deleted


def test_download_refuses_an_unknown_or_already_installed_model() -> None:
    manager = build_manager(storage=FakeStorage(installed={"small": 10}))

    with pytest.raises(UnknownModelError):
        manager.start_download("huge")

    with pytest.raises(AlreadyInstalledError):
        manager.start_download("small")


# -- activation ------------------------------------------------------------


async def test_activation_swaps_the_model_online() -> None:
    loader = FakeLoader()
    manager = build_manager(
        transcriber=FakeTranscriber(info=EngineInfo("small", "cpu", "int8")),
        loader=loader,
        storage=FakeStorage(installed={"small": 10, "base": 20}),
    )
    store = FakeConfigStore([])
    app = build_app(manager, config_store=store)

    async with make_client(app) as client:
        response = await client.post("/admin/models/activate", json={"model": "base"})

    assert response.status_code == 200
    body = response.json()
    assert body["state"] == "loaded"
    assert body["active_model"] == "base"
    assert loader.calls and loader.calls[-1].endswith("base")
    assert store.changes == [{"model": "base"}]


async def test_failed_activation_keeps_the_previous_model_and_reports_the_error() -> None:
    loader = FakeLoader(failures={"base": RuntimeError("boom")})
    previous = FakeTranscriber(info=EngineInfo("small", "cpu", "int8"), text="still here")
    manager = build_manager(
        transcriber=previous,
        loader=loader,
        storage=FakeStorage(installed={"small": 10, "base": 20}),
    )
    app = build_app(manager)

    async with make_client(app) as client:
        response = await client.post("/admin/models/activate", json={"model": "base"})
        assert response.status_code == 409
        error = response.json()["error"]
        assert error["code"] == "model_load_failed"
        assert "boom" in error["message"]

        view = (await client.get("/admin/models")).json()
        assert view["state"] == "failed"
        assert view["active_model"] == "small"
        assert "boom" in view["error"]

        status = (await client.get("/status")).json()
        assert status["model_state"] == "failed"

        transcribed = await client.post(
            "/transcribe", files={"audio": ("clip.wav", make_wav(), "audio/wav")}
        )
        assert transcribed.status_code == 200
        assert transcribed.json()["text"] == "still here"


async def test_activating_a_model_that_is_not_downloaded_is_refused() -> None:
    manager = build_manager(storage=FakeStorage(installed={"small": 10}))

    with pytest.raises(ModelNotDownloadedError):
        manager.activate("base")


async def test_a_local_ctranslate2_directory_can_be_activated(tmp_path: Path) -> None:
    local = tmp_path / "my-ct2-model"
    local.mkdir()
    (local / "model.bin").write_bytes(b"weights")
    loader = FakeLoader()
    manager = build_manager(
        transcriber=FakeTranscriber(info=EngineInfo("small", "cpu", "int8")),
        loader=loader,
    )
    store = FakeConfigStore([])
    app = build_app(manager, config_store=store)

    async with make_client(app) as client:
        response = await client.post("/admin/models/activate", json={"path": str(local)})

    assert response.status_code == 200
    body = response.json()
    assert body["active_path"] == str(local)
    assert body["active_model"] == str(local)
    assert loader.calls[-1] == str(local)
    assert store.changes == [{"model": str(local)}]


async def test_activation_quiesces_transcription() -> None:
    loader = FakeLoader()
    manager = build_manager(
        transcriber=FakeTranscriber(info=EngineInfo("small", "cpu", "int8")),
        loader=loader,
        storage=FakeStorage(installed={"small": 10, "base": 20}),
    )
    app = build_app(manager)

    started = threading.Event()
    release = threading.Event()

    def slow_load(config: ServerConfig) -> FakeTranscriber:
        started.set()
        release.wait(timeout=5)
        return FakeTranscriber(info=EngineInfo("base", "cpu", "int8"))

    loader.load = slow_load  # type: ignore[method-assign]

    async with make_client(app) as client:
        activation = asyncio.create_task(
            client.post("/admin/models/activate", json={"model": "base"})
        )
        for _ in range(500):
            if started.is_set():
                break
            await asyncio.sleep(0.01)
        assert started.is_set()

        loading = (await client.get("/status")).json()
        assert loading["model_state"] == "loading"

        busy = await client.post(
            "/transcribe", files={"audio": ("clip.wav", make_wav(), "audio/wav")}
        )
        assert busy.status_code == 503

        release.set()
        done = await activation

    assert done.status_code == 200


# -- deletion --------------------------------------------------------------


async def test_deleting_the_active_model_is_refused() -> None:
    storage = FakeStorage(installed={"small": 10, "base": 20})
    manager = build_manager(
        transcriber=FakeTranscriber(info=EngineInfo("small", "cpu", "int8")),
        storage=storage,
    )
    app = build_app(manager)

    async with make_client(app) as client:
        active = await client.delete("/admin/models/small")
        assert active.status_code == 409
        assert active.json()["error"]["code"] == "model_active"

        unused = await client.delete("/admin/models/base")
        assert unused.status_code == 204

    assert storage.deleted == ["base"]
    assert storage.installed("base") is False


def _write_model(directory: Path, size: int = 8) -> None:
    directory.mkdir(parents=True, exist_ok=True)
    (directory / "model.bin").write_bytes(b"x" * size)
    (directory / "config.json").write_text("{}", encoding="utf-8")


def test_file_storage_accepts_the_catalog_layout(tmp_path: Path) -> None:
    storage = FileModelStorage(tmp_path)
    _write_model(tmp_path / "tiny")

    assert storage.installed("tiny") is True
    assert storage.path("tiny") == tmp_path / "tiny"
    assert storage.size("tiny") > 0
    assert storage.installed("base") is False
    assert storage.path("base") == tmp_path / "base"


def test_file_storage_accepts_a_hugging_face_snapshot(tmp_path: Path) -> None:
    """A model loaded by faster-whisper before the catalog is still installed.

    Soft-stop and idle unload reload the active model through the manager;
    refusing an HF-cached model would leave the Server stopped until a restart.
    """
    storage = FileModelStorage(tmp_path)
    repo = tmp_path / "models--Systran--faster-whisper-tiny"
    snapshot = repo / "snapshots" / "abc123"
    _write_model(snapshot)
    (repo / "blobs").mkdir()
    (repo / "blobs" / "blob").write_bytes(b"y" * 16)

    assert storage.installed("tiny") is True
    assert storage.path("tiny") == snapshot
    # model.bin (8) + config.json ("{}", 2) + blob (16)
    assert storage.size("tiny") == 26

    freed = storage.delete("tiny")
    assert freed == 26
    assert storage.installed("tiny") is False
    assert not repo.exists()


async def test_deleting_an_unknown_name_is_refused_without_touching_the_disk() -> None:
    storage = FakeStorage(installed={"small": 10})
    manager = build_manager(storage=storage)
    app = build_app(manager)

    async with make_client(app) as client:
        response = await client.delete("/admin/models/huge")

    assert response.status_code == 404
    assert response.json()["error"]["code"] == "unknown_model"
    with pytest.raises(UnknownModelError):
        manager.delete("../../etc")
    assert storage.deleted == []


def test_a_catalog_name_cannot_escape_the_download_root(tmp_path: Path) -> None:
    path = tmp_path / "catalog.yaml"
    path.write_text(
        yaml.safe_dump(
            {
                "models": [
                    {
                        "name": "../../etc",
                        "disk_bytes": 1,
                        "memory_bytes": 1,
                        "speed": "fast",
                        "quality": "good",
                    }
                ]
            }
        ),
        encoding="utf-8",
    )

    with pytest.raises(CatalogError) as caught:
        ModelCatalog.load(ServerConfig(catalog_file=str(path)))
    assert "plain file name" in str(caught.value)


# -- idle unload and status transitions -----------------------------------


def test_idle_unload_defaults_to_600_seconds_and_reloads_lazily() -> None:
    clock = FakeClock()
    loader = FakeLoader()
    manager = build_manager(
        transcriber=FakeTranscriber(info=EngineInfo("small", "cpu", "int8")),
        loader=loader,
        clock=clock,
    )

    assert manager.idle_unload_seconds == 600
    assert manager.state == "loaded"

    clock.advance(599)
    assert manager.maybe_unload() is False
    assert manager.state == "loaded"

    clock.advance(1)
    assert manager.maybe_unload() is True
    assert manager.state == "cold"

    loaded = manager.ensure_loaded()
    assert loaded is not None
    assert manager.state == "loaded"
    assert loader.calls


def test_the_previous_model_still_unloads_after_a_failed_activation() -> None:
    clock = FakeClock()
    loader = FakeLoader(failures={"base": RuntimeError("boom")})
    manager = build_manager(
        transcriber=FakeTranscriber(info=EngineInfo("small", "cpu", "int8")),
        loader=loader,
        storage=FakeStorage(installed={"small": 10, "base": 20}),
        clock=clock,
    )

    with pytest.raises(ModelLoadError):
        manager.activate("base")
    assert manager.state == "failed"

    clock.advance(600)
    assert manager.maybe_unload() is True
    assert manager.state == "cold"
    assert manager.active_model == "small"


def test_zero_idle_timeout_never_unloads() -> None:
    clock = FakeClock()
    manager = build_manager(
        config=ServerConfig(workers=1, model="small", idle_unload_seconds=0),
        transcriber=FakeTranscriber(info=EngineInfo("small", "cpu", "int8")),
        clock=clock,
    )

    clock.advance(1_000_000)
    assert manager.maybe_unload() is False
    assert manager.state == "loaded"


async def test_status_reports_the_model_state_and_idle_setting() -> None:
    clock = FakeClock()
    manager = build_manager(
        transcriber=FakeTranscriber(info=EngineInfo("small", "cpu", "int8")),
        clock=clock,
    )
    app = build_app(manager)

    async with make_client(app) as client:
        status = (await client.get("/status")).json()
        assert status["model_state"] == "loaded"
        assert status["idle_unload_seconds"] == 600
        assert status["model_path"] == ""

        response = await client.post("/admin/models/idle-unload", json={"seconds": 0})
        assert response.status_code == 200
        assert response.json()["idle_unload_seconds"] == 0

        clock.advance(10_000)
        assert manager.maybe_unload() is False
        assert (await client.get("/status")).json()["model_state"] == "loaded"


async def test_model_endpoints_require_a_device_token() -> None:
    manager = build_manager()
    app = build_app(manager)

    async with make_client(app, token="") as client:
        response = await client.get("/admin/models")

    assert response.status_code == 401


async def test_idle_timeout_is_persisted_in_the_server_configuration() -> None:
    manager = build_manager()
    store = FakeConfigStore([])
    app = build_app(manager, config_store=store)

    async with make_client(app) as client:
        response = await client.post("/admin/models/idle-unload", json={"seconds": 300})

    assert response.status_code == 200
    assert store.changes == [{"idle_unload_seconds": 300}]


async def test_negative_idle_timeout_is_rejected() -> None:
    manager = build_manager()
    app = build_app(manager)

    async with make_client(app) as client:
        response = await client.post("/admin/models/idle-unload", json={"seconds": -1})

    assert response.status_code == 400
    assert response.json()["error"]["code"] == "bad_request"


class FailingStore(FakeConfigStore):
    def patch(self, changes: dict[str, Any]) -> Any:
        self.changes.append(dict(changes))
        raise ConfigError("server.idle_unload_seconds is set by the environment")


async def test_a_persist_failure_still_applies_the_idle_timeout() -> None:
    manager = build_manager()
    store = FailingStore([])
    app = build_app(manager, config_store=store)

    async with make_client(app) as client:
        response = await client.post("/admin/models/idle-unload", json={"seconds": 120})

    assert response.status_code == 200
    body = response.json()
    assert body["idle_unload_seconds"] == 120
    assert "environment" in body["warning"]
