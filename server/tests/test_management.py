"""Admin API tests for Server management: config, logs, restart, and lifecycle.

Every test stays on the HTTP seam with injected fakes: no supervisor, no real
model load, no process exit.
"""

from __future__ import annotations

import json
from pathlib import Path

import pytest
from conftest import make_client, make_wav
from fastapi import FastAPI
from kitsune_server.app import create_app
from kitsune_server.config import ServerConfig
from kitsune_server.lifecycle import Lifecycle, lifecycle_state_path
from kitsune_server.processor import FakeTextProcessor
from kitsune_server.store import ConfigStore
from kitsune_server.transcriber import EngineInfo, FakeTranscriber


def config_file(tmp_path: Path, text: str | None = None) -> Path:
    path = tmp_path / "kitsune.yaml"
    path.write_text(
        text
        or (
            "server:\n"
            "  port: 8765\n"
            "  processing:\n"
            "    enabled: true\n"
            "    api_key: sk-super-secret\n"
            "  decode:\n"
            "    beam_size: 5\n"
            "client:\n"
            "  hotkey: Ctrl+Shift+Space\n"
        ),
        encoding="utf-8",
    )
    return path


def build(
    *,
    tmp_path: Path,
    lifecycle: Lifecycle | None = None,
    log_file: Path | None = None,
    restart=None,
    shutdown=None,
    config_store: ConfigStore | None = None,
) -> FastAPI:
    transcriber = FakeTranscriber(info=EngineInfo("small", "cpu", "int8"))
    app = create_app(
        ServerConfig(workers=1),
        transcriber,
        FakeTextProcessor(),
        cuda_device_count=0,
        config_store=config_store,
        lifecycle=lifecycle,
        log_file=log_file,
        restart=restart,
        shutdown=shutdown,
    )
    return app


def audio_part() -> dict[str, tuple[str, bytes, str]]:
    return {"audio": ("clip.wav", make_wav(0.2), "audio/wav")}


async def test_admin_config_redacts_the_api_key_and_reports_sources(tmp_path: Path) -> None:
    store = ConfigStore(config_file(tmp_path), env={})
    app = build(tmp_path=tmp_path, config_store=store)

    async with make_client(app) as client:
        response = await client.get("/admin/config")

    assert response.status_code == 200
    body = response.json()
    assert body["config"]["processing"]["api_key"] == "***"
    assert body["config"]["decode"]["beam_size"] == 5
    assert body["redacted"] == ["server.processing.api_key"]
    assert "sk-super-secret" not in response.text
    assert body["fields"]["server.processing.api_key"]["value"] == "***"
    assert body["fields"]["server.port"]["source"] == "file"
    assert body["fields"]["server.port"]["mode"] == "restart"
    assert body["fields"]["server.log_level"]["mode"] == "hot"
    assert body["path"].endswith("kitsune.yaml")


async def test_admin_config_patch_writes_valid_changes_and_classifies_them(tmp_path: Path) -> None:
    path = config_file(tmp_path)
    store = ConfigStore(path, env={})
    app = build(tmp_path=tmp_path, config_store=store)

    async with make_client(app) as client:
        response = await client.patch(
            "/admin/config",
            json={"patch": {"port": 9100, "log_level": "debug", "decode": {"beam_size": 3}}},
        )
        status = await client.get("/status")

    assert response.status_code == 200
    body = response.json()
    assert body["written"] is True
    assert "server.port" in body["changed"]
    assert body["restart_required"] == ["server.port", "server.decode.beam_size"]
    assert body["hot_applied"] == ["server.log_level"]
    assert body["config"]["port"] == 9100
    assert status.json()["network"]["port"] == 8765
    text = path.read_text(encoding="utf-8")
    assert "port: 9100" in text
    assert "beam_size: 3" in text
    assert "client:" in text


async def test_admin_config_does_not_redact_an_unset_key(tmp_path: Path) -> None:
    path = tmp_path / "kitsune.yaml"
    path.write_text("server:\n  port: 8765\n", encoding="utf-8")
    app = build(tmp_path=tmp_path, config_store=ConfigStore(path, env={}))

    async with make_client(app) as client:
        response = await client.get("/admin/config")

    assert response.status_code == 200
    assert response.json()["redacted"] == []
    assert response.json()["config"]["processing"]["api_key"] is None


async def test_generic_config_patch_refuses_anonymous_access(tmp_path: Path) -> None:
    path = config_file(tmp_path)
    original = path.read_bytes()
    app = build(tmp_path=tmp_path, config_store=ConfigStore(path, env={}))

    async with make_client(app) as client:
        patched = await client.patch("/admin/config", json={"patch": {"allow_anonymous": True}})

    assert patched.status_code == 400
    assert patched.json()["error"]["code"] == "confirmation_required"
    assert "allow_anonymous" in patched.json()["error"]["message"]
    assert path.read_bytes() == original


async def test_generic_config_patch_refuses_lan_exposure(tmp_path: Path) -> None:
    path = config_file(tmp_path)
    original = path.read_bytes()
    app = build(tmp_path=tmp_path, config_store=ConfigStore(path, env={}))

    async with make_client(app) as client:
        patched = await client.patch("/admin/config", json={"patch": {"expose_to_network": True}})
        read = await client.get("/admin/config")

    assert patched.status_code == 400
    assert patched.json()["error"]["code"] == "confirmation_required"
    assert "/admin/exposure" in patched.json()["error"]["message"]
    assert path.read_bytes() == original
    fields = read.json()["fields"]
    assert fields["server.expose_to_network"]["locked"] is True
    assert fields["server.expose_to_network"]["reason"]
    assert fields["server.allow_anonymous"]["locked"] is True


async def test_admin_config_patch_rejects_invalid_values_and_leaves_the_file(
    tmp_path: Path,
) -> None:
    path = config_file(tmp_path)
    original = path.read_bytes()
    app = build(tmp_path=tmp_path, config_store=ConfigStore(path, env={}))

    async with make_client(app) as client:
        response = await client.patch("/admin/config", json={"patch": {"port": 0}})

    assert response.status_code == 400
    assert response.json()["error"]["code"] == "bad_request"
    assert "server.port" in response.json()["error"]["message"]
    assert path.read_bytes() == original


async def test_admin_config_patch_reports_a_read_only_file(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    path = config_file(tmp_path)
    app = build(tmp_path=tmp_path, config_store=ConfigStore(path, env={}))

    def read_only(*_args: object, **_kwargs: object) -> None:
        raise OSError("read-only file system")

    monkeypatch.setattr("kitsune_server.store._atomic_write", read_only)

    async with make_client(app) as client:
        response = await client.patch("/admin/config", json={"patch": {"port": 9100}})

    assert response.status_code == 503
    assert response.json()["error"]["code"] == "config_unavailable"
    assert "read-only" in response.json()["error"]["message"]


async def test_admin_config_patch_never_writes_a_redacted_sentinel(tmp_path: Path) -> None:
    path = config_file(tmp_path)
    original = path.read_bytes()
    app = build(tmp_path=tmp_path, config_store=ConfigStore(path, env={}))

    async with make_client(app) as client:
        response = await client.patch(
            "/admin/config",
            json={"patch": {"processing": {"api_key": "***"}}},
        )

    assert response.status_code == 200
    assert response.json()["written"] is False
    assert path.read_bytes() == original


async def test_admin_config_is_unavailable_without_a_store(tmp_path: Path) -> None:
    app = build(tmp_path=tmp_path)

    async with make_client(app) as client:
        response = await client.get("/admin/config")

    assert response.status_code == 503
    assert response.json()["error"]["code"] == "config_unavailable"


async def test_device_list_never_returns_token_hashes(tmp_path: Path) -> None:
    app = build(tmp_path=tmp_path)
    _, token = app.state.devices.create("Laptop", "127.0.0.1")

    async with make_client(app) as client:
        response = await client.get("/admin/devices")

    assert response.status_code == 200
    assert token not in response.text
    assert "hash" not in response.text
    assert response.json()["devices"][0]["name"] == "Laptop"


async def test_log_tail_returns_the_last_lines(tmp_path: Path) -> None:
    log_file = tmp_path / "server.log"
    log_file.write_text(
        "\n".join(f"line {index}" for index in range(1, 6)) + "\n", encoding="utf-8"
    )
    app = build(tmp_path=tmp_path, log_file=log_file)

    async with make_client(app) as client:
        response = await client.get("/admin/logs", params={"tail": 2})

    assert response.status_code == 200
    body = response.json()
    assert body["lines"] == ["line 4", "line 5"]
    assert body["truncated"] is True
    assert body["path"] == str(log_file)


async def test_log_tail_rejects_an_out_of_range_size(tmp_path: Path) -> None:
    app = build(tmp_path=tmp_path, log_file=tmp_path / "server.log")

    async with make_client(app) as client:
        response = await client.get("/admin/logs", params={"tail": 0})

    assert response.status_code == 400
    assert response.json()["error"]["code"] == "bad_request"


async def test_restart_returns_accepted_and_signals_the_exit(tmp_path: Path) -> None:
    calls: list[str] = []
    app = build(tmp_path=tmp_path, restart=lambda: calls.append("restart"))

    async with make_client(app) as client:
        response = await client.post("/admin/restart")

    assert response.status_code == 202
    body = response.json()
    assert body["restarting"] is True
    assert body["stopped"] is False
    assert body["loaded"] is True
    assert body["model_state"] == "loaded"
    assert body["model"] == "small"
    assert calls == ["restart"]


async def test_restart_is_unavailable_without_an_exit_action(tmp_path: Path) -> None:
    app = build(tmp_path=tmp_path)

    async with make_client(app) as client:
        response = await client.post("/admin/restart")

    assert response.status_code == 503
    assert response.json()["error"]["code"] == "restart_unavailable"


async def test_hard_stop_clears_the_soft_stop_and_signals_the_exit(tmp_path: Path) -> None:
    calls: list[str] = []
    soft_stop = Lifecycle()
    soft_stop.mark_stopped()
    app = build(tmp_path=tmp_path, lifecycle=soft_stop, shutdown=lambda: calls.append("stop"))

    async with make_client(app) as client:
        response = await client.post("/admin/hard-stop")

    assert response.status_code == 202
    assert soft_stop.stopped is False
    assert calls == ["stop"]


async def test_hard_restart_clears_the_soft_stop_and_signals_the_exit(tmp_path: Path) -> None:
    calls: list[str] = []
    soft_stop = Lifecycle()
    soft_stop.mark_stopped()
    app = build(tmp_path=tmp_path, lifecycle=soft_stop, restart=lambda: calls.append("restart"))

    async with make_client(app) as client:
        response = await client.post("/admin/hard-restart")

    assert response.status_code == 202
    assert response.json()["restarting"] is True
    assert soft_stop.stopped is False
    assert calls == ["restart"]


async def test_hard_lifecycle_is_available_only_to_a_loopback_peer(tmp_path: Path) -> None:
    calls: list[str] = []
    soft_stop = Lifecycle()
    soft_stop.mark_stopped()
    app = build(
        tmp_path=tmp_path,
        lifecycle=soft_stop,
        restart=lambda: calls.append("restart"),
        shutdown=lambda: calls.append("stop"),
    )

    async with make_client(app, peer=("192.168.1.20", 1234)) as client:
        stopped = await client.post("/admin/hard-stop")
        restarted = await client.post("/admin/hard-restart")

    assert stopped.status_code == 403
    assert stopped.json()["error"]["code"] == "local_only"
    assert restarted.status_code == 403
    assert calls == []
    assert soft_stop.stopped is True


async def test_hard_stop_is_unavailable_without_an_exit_action(tmp_path: Path) -> None:
    app = build(tmp_path=tmp_path)

    async with make_client(app) as client:
        response = await client.post("/admin/hard-stop")

    assert response.status_code == 503
    assert response.json()["error"]["code"] == "stop_unavailable"


async def test_soft_stop_with_a_fixed_runtime_refuses_transcription(tmp_path: Path) -> None:
    # The fixed adapter is the test/no-manager path: it keeps the process's
    # model but must still honour the soft-stop contract on the HTTP seam.
    lifecycle = Lifecycle()
    app = build(tmp_path=tmp_path, lifecycle=lifecycle)

    async with make_client(app) as client:
        stopped = await client.post("/admin/stop")
        transcription = await client.post("/transcribe", files=audio_part())
        status = await client.get("/status")

    assert stopped.status_code == 200
    assert stopped.json()["stopped"] is True
    assert stopped.json()["loaded"] is False
    assert stopped.json()["model_state"] == "stopped"
    assert lifecycle.stopped is True
    assert transcription.status_code == 503
    assert transcription.json()["error"]["code"] == "stopped"
    assert status.json()["lifecycle"] == {"stopped": True, "loaded": False}
    assert status.json()["model_state"] == "stopped"
    assert status.json()["ready"] is False


async def test_start_reloads_the_model_and_clears_the_stopped_state(tmp_path: Path) -> None:
    from kitsune_server.models import ModelManager

    state_path = lifecycle_state_path({"XDG_STATE_HOME": str(tmp_path / "state")})
    loader = _ManagerLoader()
    soft_stop = Lifecycle(state_path=state_path)
    manager = ModelManager(
        ServerConfig(workers=1, model="small"),
        loader=loader,
        storage=_ManagerStorage(),
        soft_stop=soft_stop,
    )
    manager.start()
    app = create_app(
        manager.config,
        None,
        FakeTextProcessor(),
        cuda_device_count=0,
        models=manager,
    )

    async with make_client(app) as client:
        await client.post("/admin/stop")
        assert json.loads(state_path.read_text(encoding="utf-8"))["stopped"] is True
        started = await client.post("/admin/start")
        transcription = await client.post("/transcribe", files=audio_part())

    assert started.status_code == 200
    assert started.json()["stopped"] is False
    assert started.json()["loaded"] is True
    assert started.json()["model_state"] == "loaded"
    assert len(loader.calls) == 2
    assert transcription.status_code == 200
    assert transcription.json()["text"] == "loaded again"
    assert json.loads(state_path.read_text(encoding="utf-8"))["stopped"] is False


def test_a_soft_stop_survives_a_process_restart(tmp_path: Path) -> None:
    state_path = lifecycle_state_path({"XDG_STATE_HOME": str(tmp_path / "state")})
    first = Lifecycle(state_path=state_path)
    first.mark_stopped()

    restarted = Lifecycle(state_path=state_path)

    assert restarted.stopped is True
    restarted.clear()
    assert restarted.stopped is False
    assert Lifecycle(state_path=state_path).stopped is False


async def test_start_reports_a_load_failure_as_unavailable(tmp_path: Path) -> None:
    from kitsune_server.models import ModelManager

    class BrokenLoader:
        def load(self, config: ServerConfig) -> FakeTranscriber:
            raise RuntimeError("model is gone")

    soft_stop = Lifecycle(stopped=True)
    manager = ModelManager(
        ServerConfig(workers=1, model="small"),
        loader=BrokenLoader(),
        storage=_ManagerStorage(),
        soft_stop=soft_stop,
    )
    app = create_app(
        manager.config,
        None,
        FakeTextProcessor(),
        cuda_device_count=0,
        models=manager,
    )

    async with make_client(app) as client:
        response = await client.post("/admin/start")

    assert response.status_code == 503
    assert response.json()["error"]["code"] == "engine_load_failed"
    assert "model is gone" in response.json()["error"]["message"]
    assert soft_stop.stopped is True


class _ManagerLoader:
    """Loads a fresh fake model for the model manager's activation path."""

    def __init__(self) -> None:
        self.calls: list[str] = []

    def load(self, config: ServerConfig) -> FakeTranscriber:
        self.calls.append(config.model)
        return FakeTranscriber(text="loaded again", info=EngineInfo("small", "cpu", "int8"))


class _ManagerStorage:
    """``small`` is installed, so activation and reload stay offline."""

    def path(self, name: str) -> Path:
        return Path("/models") / name

    def installed(self, name: str) -> bool:
        return name == "small"

    def size(self, name: str) -> int:
        return 10

    def delete(self, name: str) -> int:
        return 0


async def test_soft_stop_defers_to_the_model_manager(tmp_path: Path) -> None:
    from kitsune_server.models import ModelManager

    clock = [1000.0]
    loader = _ManagerLoader()
    manager = ModelManager(
        ServerConfig(workers=1, model="small", idle_unload_seconds=600),
        transcriber=FakeTranscriber(info=EngineInfo("small", "cpu", "int8")),
        loader=loader,
        storage=_ManagerStorage(),
        clock=lambda: clock[0],
    )
    assert manager.current_transcriber() is not None
    app = create_app(
        manager.config,
        None,
        FakeTextProcessor(),
        cuda_device_count=0,
        models=manager,
    )

    async with make_client(app) as client:
        stopped = await client.post("/admin/stop")
        status = await client.get("/status")
        transcription = await client.post("/transcribe", files=audio_part())

    assert stopped.json()["model_state"] == "stopped"
    assert manager.current_transcriber() is None
    assert manager.state == "cold"
    assert status.json()["model_state"] == "stopped"
    assert status.json()["lifecycle"] == {"stopped": True, "loaded": False}
    assert status.json()["ready"] is False
    assert transcription.status_code == 503
    assert transcription.json()["error"]["code"] == "stopped"

    async with make_client(app) as client:
        started = await client.post("/admin/start")
        transcribe = await client.post("/transcribe", files=audio_part())

    assert started.json()["model_state"] == "loaded"
    assert manager.current_transcriber() is not None
    assert len(loader.calls) == 1 and loader.calls[0].endswith("small")
    assert transcribe.status_code == 200
    assert transcribe.json()["text"] == "loaded again"


async def test_idle_unload_still_reports_cold_and_reloads_lazily(tmp_path: Path) -> None:
    from kitsune_server.models import ModelManager

    clock = [1000.0]
    manager = ModelManager(
        ServerConfig(workers=1, model="small", idle_unload_seconds=1),
        transcriber=FakeTranscriber(info=EngineInfo("small", "cpu", "int8")),
        loader=_ManagerLoader(),
        storage=_ManagerStorage(),
        clock=lambda: clock[0],
    )
    app = create_app(
        manager.config,
        None,
        FakeTextProcessor(),
        cuda_device_count=0,
        models=manager,
    )
    clock[0] += 2

    async with make_client(app) as client:
        status = await client.get("/status")
        transcribe = await client.post("/transcribe", files=audio_part())

    assert status.json()["model_state"] == "cold"
    assert status.json()["lifecycle"] == {"stopped": False, "loaded": False}
    assert status.json()["ready"] is True
    assert transcribe.status_code == 200
    assert manager.state == "loaded"


async def test_stop_start_restart_and_status_share_one_model_payload(tmp_path: Path) -> None:
    from kitsune_server.models import ModelManager

    manager = ModelManager(
        ServerConfig(workers=1, model="small", idle_unload_seconds=600),
        transcriber=FakeTranscriber(text="again", info=EngineInfo("small", "cpu", "int8")),
        loader=_ManagerLoader(),
        storage=_ManagerStorage(),
    )
    app = create_app(
        manager.config,
        None,
        FakeTextProcessor(),
        cuda_device_count=0,
        models=manager,
        restart=lambda: None,
    )

    async with make_client(app) as client:
        initial = (await client.get("/status")).json()
        stopped = (await client.post("/admin/stop")).json()
        models_after_stop = (await client.get("/admin/models")).json()
        after_stop = (await client.get("/status")).json()
        started = (await client.post("/admin/start")).json()
        after_start = (await client.get("/status")).json()
        restarted = (await client.post("/admin/restart")).json()

    keys = ("model", "model_state", "model_path", "idle_unload_seconds", "stopped", "loaded")
    for key in keys:
        assert stopped[key] == after_stop[key]
        assert started[key] == after_start[key]
        assert restarted[key] == after_start[key]
    assert stopped["model_state"] == "stopped"
    assert stopped["loaded"] is False
    assert after_stop["ready"] is False
    assert models_after_stop["state"] == "stopped"
    assert started["model_state"] == "loaded"
    assert started["loaded"] is True
    assert after_start["ready"] is True
    assert restarted["restarting"] is True
    assert initial["model"] == "small"
    assert initial["idle_unload_seconds"] == 600


async def test_idle_unload_is_hot_only_when_a_manager_applies_it(tmp_path: Path) -> None:
    from kitsune_server.models import ModelManager

    path = config_file(tmp_path)
    store = ConfigStore(path, env={})
    manager = ModelManager(
        ServerConfig(workers=1, model="small"),
        transcriber=FakeTranscriber(info=EngineInfo("small", "cpu", "int8")),
        loader=_ManagerLoader(),
        storage=_ManagerStorage(),
    )
    app = create_app(
        manager.config,
        None,
        FakeTextProcessor(),
        cuda_device_count=0,
        config_store=store,
        models=manager,
    )

    async with make_client(app) as client:
        patched = await client.patch("/admin/config", json={"patch": {"idle_unload_seconds": 300}})
        status = await client.get("/status")

    assert patched.status_code == 200
    assert patched.json()["hot_applied"] == ["server.idle_unload_seconds"]
    assert patched.json()["restart_required"] == []
    assert manager.idle_unload_seconds == 300
    assert status.json()["idle_unload_seconds"] == 300

    # Without a manager the same edit is honestly restart-required.
    fixed = build(tmp_path=tmp_path, config_store=ConfigStore(path, env={}))
    async with make_client(fixed) as client:
        fixed_patch = await client.patch(
            "/admin/config", json={"patch": {"idle_unload_seconds": 120}}
        )
    assert fixed_patch.json()["hot_applied"] == []
    assert fixed_patch.json()["restart_required"] == ["server.idle_unload_seconds"]


def test_the_runtime_is_the_only_owner_of_the_loaded_model(tmp_path: Path) -> None:
    import gc
    import weakref

    from kitsune_server.models import ModelManager

    manager = ModelManager(
        ServerConfig(workers=1, model="small"),
        loader=_ManagerLoader(),
        storage=_ManagerStorage(),
    )
    manager.start()
    # The test holds no other strong reference: stop must release the model.
    reference = weakref.ref(manager.current_transcriber())
    manager.stop()
    gc.collect()

    assert reference() is None
    assert manager.current_transcriber() is None
