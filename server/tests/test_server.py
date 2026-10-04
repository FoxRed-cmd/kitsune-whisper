from __future__ import annotations

import logging
from pathlib import Path

import pytest
import yaml
from kitsune_server import cli, server
from kitsune_server.config import ServerConfig
from kitsune_server.engine import EngineLoadError
from kitsune_server.transcriber import EngineInfo, FakeTranscriber


def test_run_server_logs_resolved_engine(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    fake = FakeTranscriber(info=EngineInfo("small", "cpu", "int8"))
    monkeypatch.setattr(server, "build_transcriber", lambda config: fake)
    monkeypatch.setattr(server.uvicorn, "run", lambda *args, **kwargs: None)

    with caplog.at_level(logging.INFO, logger="kitsune.server"):
        server.run_server(ServerConfig())

    messages = [record.getMessage() for record in caplog.records]
    assert any(
        "model=small device=cpu compute_type=int8 (resolved from auto)" in message
        for message in messages
    )


def test_run_server_warns_on_cpu_with_escape_hatch(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    fake = FakeTranscriber(info=EngineInfo("small", "cpu", "int8"))
    monkeypatch.setattr(server, "build_transcriber", lambda config: fake)
    monkeypatch.setattr(server.uvicorn, "run", lambda *args, **kwargs: None)

    with caplog.at_level(logging.INFO, logger="kitsune.server"):
        server.run_server(ServerConfig())

    warnings = [
        record.getMessage() for record in caplog.records if record.levelno == logging.WARNING
    ]
    assert any("CPU" in message and "base/tiny" in message for message in warnings)


def test_run_server_does_not_warn_on_cuda(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    fake = FakeTranscriber(info=EngineInfo("small", "cuda", "float16"))
    monkeypatch.setattr(server, "build_transcriber", lambda config: fake)
    monkeypatch.setattr(server.uvicorn, "run", lambda *args, **kwargs: None)

    with caplog.at_level(logging.INFO, logger="kitsune.server"):
        server.run_server(ServerConfig())

    assert not any(record.levelno == logging.WARNING for record in caplog.records)


def test_build_transcriber_loads_whisper_adapter(monkeypatch: pytest.MonkeyPatch) -> None:
    calls: dict[str, object] = {}
    sentinel = FakeTranscriber()

    def fake_load(config: ServerConfig, **kwargs: object) -> FakeTranscriber:
        calls["config"] = config
        calls["kwargs"] = kwargs
        return sentinel

    monkeypatch.setattr(server.WhisperTranscriber, "load", fake_load)
    config = ServerConfig()

    assert server.build_transcriber(config) is sentinel
    assert calls["config"] is config


def test_main_reports_engine_load_error(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    path = tmp_path / "kitsune.yaml"
    path.write_text(yaml.safe_dump({"server": {"model": "small"}}), encoding="utf-8")

    def boom(config: ServerConfig) -> None:
        raise EngineLoadError("model unavailable")

    monkeypatch.setattr(server, "run_server", boom)

    code = cli.main(["--config", str(path)])

    assert code == 1
    assert "engine error: model unavailable" in capsys.readouterr().err
