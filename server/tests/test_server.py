from __future__ import annotations

import errno
import logging
import socket
import sys
from pathlib import Path
from typing import Any

import pytest
import yaml
from kitsune_server import cli, server
from kitsune_server.config import ServerConfig
from kitsune_server.engine import EngineLoadError
from kitsune_server.transcriber import EngineInfo, FakeTranscriber


def allow_port(host: str, port: int) -> None:
    """Skip the real bind check in tests that only exercise runtime wiring."""


class FakeSocket:
    """Minimal socket double for the port preflight."""

    def __init__(self, failure: OSError | None = None) -> None:
        self.failure = failure
        self.bound: tuple[str, int] | None = None
        self.options: list[tuple[int, int, int]] = []
        self.closed = False

    def setsockopt(self, level: int, option: int, value: int) -> None:
        self.options.append((level, option, value))

    def bind(self, address: tuple[str, int]) -> None:
        if self.failure is not None:
            raise self.failure
        self.bound = address

    def __enter__(self) -> FakeSocket:
        return self

    def __exit__(self, *exc_info: object) -> None:
        self.closed = True


def socket_factory(probe: FakeSocket) -> Any:
    def factory(family: int, kind: int) -> FakeSocket:
        return probe

    return factory


def test_check_port_available_binds_the_configured_address() -> None:
    probe = FakeSocket()

    server.check_port_available("127.0.0.1", 8765, socket_factory=socket_factory(probe))

    assert probe.bound == ("127.0.0.1", 8765)
    assert probe.closed


def test_check_port_available_reuses_a_waiting_socket_on_posix() -> None:
    probe = FakeSocket()

    server.check_port_available("127.0.0.1", 8765, socket_factory=socket_factory(probe))

    if sys.platform != "win32":
        assert probe.options == [(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)]


def test_check_port_available_names_server_port_when_taken() -> None:
    probe = FakeSocket(OSError(errno.EADDRINUSE, "Address already in use"))

    with pytest.raises(server.PortInUseError) as caught:
        server.check_port_available("127.0.0.1", 8765, socket_factory=socket_factory(probe))

    message = str(caught.value)
    assert "server.port 8765" in message
    assert "127.0.0.1" in message
    assert "KITSUNE_SERVER_PORT" in message


def test_check_port_available_explains_other_bind_errors() -> None:
    probe = FakeSocket(OSError(errno.EACCES, "Permission denied"))

    with pytest.raises(server.PortInUseError) as caught:
        server.check_port_available("127.0.0.1", 80, socket_factory=socket_factory(probe))

    assert "server.port 80 cannot be bound" in str(caught.value)


def test_run_server_fails_fast_when_the_port_is_taken(monkeypatch: pytest.MonkeyPatch) -> None:
    loaded: list[ServerConfig] = []
    monkeypatch.setattr(server, "build_transcriber", loaded.append)

    def taken(host: str, port: int) -> None:
        raise server.PortInUseError(f"server.port {port} is already in use on {host}")

    with pytest.raises(server.PortInUseError):
        server.run_server(ServerConfig(), port_check=taken)

    assert loaded == []


def test_run_server_logs_resolved_engine(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    fake = FakeTranscriber(info=EngineInfo("small", "cpu", "int8"))
    monkeypatch.setattr(server, "build_transcriber", lambda config, **kwargs: fake)
    monkeypatch.setattr(server.uvicorn, "run", lambda *args, **kwargs: None)

    with caplog.at_level(logging.INFO, logger="kitsune.server"):
        server.run_server(ServerConfig(), port_check=allow_port)

    messages = [record.getMessage() for record in caplog.records]
    assert any(
        "model=small device=cpu compute_type=int8 (resolved from auto)" in message
        for message in messages
    )


def test_run_server_warns_on_cpu_with_escape_hatch(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    fake = FakeTranscriber(info=EngineInfo("small", "cpu", "int8"))
    monkeypatch.setattr(server, "build_transcriber", lambda config, **kwargs: fake)
    monkeypatch.setattr(server.uvicorn, "run", lambda *args, **kwargs: None)

    with caplog.at_level(logging.INFO, logger="kitsune.server"):
        server.run_server(ServerConfig(), port_check=allow_port)

    warnings = [
        record.getMessage() for record in caplog.records if record.levelno == logging.WARNING
    ]
    assert any("CPU" in message and "base/tiny" in message for message in warnings)


def test_run_server_does_not_warn_on_cuda(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    fake = FakeTranscriber(info=EngineInfo("small", "cuda", "float16"))
    monkeypatch.setattr(server, "build_transcriber", lambda config, **kwargs: fake)
    monkeypatch.setattr(server.uvicorn, "run", lambda *args, **kwargs: None)

    with caplog.at_level(logging.INFO, logger="kitsune.server"):
        server.run_server(ServerConfig(), port_check=allow_port)

    assert not any(record.levelno == logging.WARNING for record in caplog.records)


def _capture_uvicorn(monkeypatch: pytest.MonkeyPatch) -> dict[str, object]:
    captured: dict[str, object] = {}
    fake = FakeTranscriber(info=EngineInfo("small", "cpu", "int8"))
    monkeypatch.setattr(server, "build_transcriber", lambda config, **kwargs: fake)

    def fake_run(app: object, **kwargs: object) -> None:
        captured.update(kwargs)

    monkeypatch.setattr(server.uvicorn, "run", fake_run)
    return captured


def test_run_server_binds_loopback_by_default(monkeypatch: pytest.MonkeyPatch) -> None:
    captured = _capture_uvicorn(monkeypatch)

    server.run_server(ServerConfig(), port_check=allow_port)

    assert captured["host"] == "127.0.0.1"
    assert captured["port"] == 8765


def test_run_server_binds_all_interfaces_when_exposed(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    captured = _capture_uvicorn(monkeypatch)

    with caplog.at_level(logging.WARNING, logger="kitsune.server"):
        server.run_server(ServerConfig(expose_to_network=True), port_check=allow_port)

    assert captured["host"] == "0.0.0.0"
    assert any("LAN" in record.getMessage() for record in caplog.records)


def test_run_server_detects_cuda_once_and_shares_it(monkeypatch: pytest.MonkeyPatch) -> None:
    detections: list[bool] = []
    monkeypatch.setattr(server, "detect_cuda_devices", lambda: detections.append(True) or 2)
    seen: dict[str, object] = {}

    def fake_build(config: ServerConfig, *, cuda_device_count: int | None = None) -> Any:
        seen["devices"] = cuda_device_count
        return FakeTranscriber(info=EngineInfo("small", "cuda", "float16"))

    monkeypatch.setattr(server, "build_transcriber", fake_build)
    monkeypatch.setattr(server.uvicorn, "run", lambda *args, **kwargs: None)

    server.run_server(ServerConfig(), port_check=allow_port)

    assert detections == [True]
    assert seen["devices"] == 2


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


def test_run_server_writes_its_output_to_the_log_file(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    fake = FakeTranscriber(info=EngineInfo("small", "cpu", "int8"))
    monkeypatch.setattr(server, "build_transcriber", lambda config, **kwargs: fake)
    monkeypatch.setattr(server.uvicorn, "run", lambda *args, **kwargs: None)
    log_file = tmp_path / "state" / "server.log"

    server.run_server(ServerConfig(), port_check=allow_port, log_file=log_file)

    assert "model=small" in log_file.read_text(encoding="utf-8")


def test_start_writes_a_fatal_config_error_to_the_log(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    log_file = tmp_path / "state" / "server.log"
    monkeypatch.setattr(server, "default_log_file", lambda: log_file)

    assert cli.main(["--config", str(tmp_path / "missing.yaml")]) == 1

    assert "config error" in log_file.read_text(encoding="utf-8")
    assert "config error" in capsys.readouterr().err


def test_start_writes_a_port_conflict_to_the_log(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    log_file = tmp_path / "state" / "server.log"
    monkeypatch.setattr(server, "default_log_file", lambda: log_file)

    def taken(config: ServerConfig, log_file: Path | None = None) -> None:
        raise server.PortInUseError(
            "server.port 8765 is already in use on 127.0.0.1; change server.port"
        )

    monkeypatch.setattr(server, "run_server", taken)

    assert cli.main(["start"]) == 1

    assert "server.port 8765" in log_file.read_text(encoding="utf-8")
    assert "error: server.port 8765" in capsys.readouterr().err


def test_default_log_file_lives_under_the_user_state_directory(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path
) -> None:
    monkeypatch.setenv("XDG_STATE_HOME", str(tmp_path / "state"))

    assert server.default_log_file() == tmp_path / "state" / "kitsune-whisper" / "server.log"


def test_main_reports_engine_load_error(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    path = tmp_path / "kitsune.yaml"
    path.write_text(yaml.safe_dump({"server": {"model": "small"}}), encoding="utf-8")

    def boom(config: ServerConfig, *, log_file: Path | None = None) -> None:
        raise EngineLoadError("model unavailable")

    monkeypatch.setattr(server, "run_server", boom)

    code = cli.main(["--config", str(path)])

    assert code == 1
    assert "engine error: model unavailable" in capsys.readouterr().err
