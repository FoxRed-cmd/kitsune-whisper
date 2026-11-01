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
from kitsune_server.lifecycle import Lifecycle, LifecycleError, lifecycle_state_path
from kitsune_server.processor import FakeTextProcessor
from kitsune_server.store import ConfigStore
from kitsune_server.transcriber import EngineInfo, FakeTranscriber, Transcriber


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
    config_store: ConfigStore | None = None,
) -> FastAPI:
    transcriber = FakeTranscriber(info=EngineInfo("small", "cpu", "int8"))
    if lifecycle is None:
        lifecycle = Lifecycle(lambda: transcriber, info=transcriber.info)
    app = create_app(
        ServerConfig(workers=1),
        transcriber,
        FakeTextProcessor(),
        cuda_device_count=0,
        config_store=config_store,
        lifecycle=lifecycle,
        log_file=log_file,
        restart=restart,
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
    assert status.json()["network"]["port"] == 9100
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
    assert response.json() == {"restarting": True}
    assert calls == ["restart"]


async def test_restart_is_unavailable_without_an_exit_action(tmp_path: Path) -> None:
    app = build(tmp_path=tmp_path)

    async with make_client(app) as client:
        response = await client.post("/admin/restart")

    assert response.status_code == 503
    assert response.json()["error"]["code"] == "restart_unavailable"


async def test_soft_stop_unloads_the_model_and_transcribe_returns_503(tmp_path: Path) -> None:
    unloaded: list[Transcriber] = []
    current = FakeTranscriber(info=EngineInfo("small", "cpu", "int8"))

    def record(transcriber: Transcriber | None) -> None:
        assert transcriber is not None
        unloaded.append(transcriber)

    lifecycle = Lifecycle(
        lambda: FakeTranscriber(info=EngineInfo("small", "cpu", "int8")),
        unload=record,
        info=current.info,
    )
    lifecycle.start()
    app = build(tmp_path=tmp_path, lifecycle=lifecycle)

    async with make_client(app) as client:
        stopped = await client.post("/admin/stop")
        transcription = await client.post("/transcribe", files=audio_part())
        status = await client.get("/status")

    assert stopped.status_code == 200
    assert stopped.json()["stopped"] is True
    assert stopped.json()["loaded"] is False
    assert len(unloaded) == 1
    assert transcription.status_code == 503
    assert transcription.json()["error"]["code"] == "stopped"
    assert status.json()["lifecycle"] == {"stopped": True, "loaded": False}
    assert status.json()["model_state"] == "stopped"
    assert status.json()["ready"] is False


async def test_start_reloads_the_model_and_clears_the_stopped_state(tmp_path: Path) -> None:
    state_path = lifecycle_state_path({"XDG_STATE_HOME": str(tmp_path / "state")})
    loaded: list[FakeTranscriber] = []

    def load() -> FakeTranscriber:
        transcriber = FakeTranscriber(text="back again", info=EngineInfo("small", "cpu", "int8"))
        loaded.append(transcriber)
        return transcriber

    lifecycle = Lifecycle(load, info=EngineInfo("small", "cpu", "int8"), state_path=state_path)
    lifecycle.start()
    app = build(tmp_path=tmp_path, lifecycle=lifecycle)

    async with make_client(app) as client:
        await client.post("/admin/stop")
        assert json.loads(state_path.read_text(encoding="utf-8"))["stopped"] is True
        started = await client.post("/admin/start")
        transcription = await client.post("/transcribe", files=audio_part())

    assert started.status_code == 200
    assert started.json()["stopped"] is False
    assert started.json()["loaded"] is True
    assert len(loaded) == 2
    assert transcription.status_code == 200
    assert transcription.json()["text"] == "back again"
    assert json.loads(state_path.read_text(encoding="utf-8"))["stopped"] is False


async def test_a_soft_stop_survives_a_process_restart(tmp_path: Path) -> None:
    state_path = lifecycle_state_path({"XDG_STATE_HOME": str(tmp_path / "state")})
    first = Lifecycle(
        lambda: FakeTranscriber(),
        info=EngineInfo("small", "cpu", "int8"),
        state_path=state_path,
    )
    first.start()
    first.stop()

    restarted = Lifecycle(
        lambda: FakeTranscriber(),
        info=EngineInfo("small", "cpu", "int8"),
        state_path=state_path,
    )

    assert restarted.stopped is True
    assert restarted.loaded is False
    with pytest.raises(LifecycleError):
        _ = restarted.transcriber
    restarted.start()
    assert restarted.loaded is True


async def test_start_reports_a_load_failure_as_unavailable(tmp_path: Path) -> None:
    def broken() -> FakeTranscriber:
        raise RuntimeError("model is gone")

    lifecycle = Lifecycle(broken, info=EngineInfo("small", "cpu", "int8"), stopped=True)
    app = build(tmp_path=tmp_path, lifecycle=lifecycle)

    async with make_client(app) as client:
        response = await client.post("/admin/start")

    assert response.status_code == 503
    assert response.json()["error"]["code"] == "engine_load_failed"
    assert "model is gone" in response.json()["error"]["message"]
    assert lifecycle.stopped is True
