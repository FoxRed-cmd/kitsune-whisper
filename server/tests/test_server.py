from __future__ import annotations

import asyncio
import errno
import logging
import socket
import sys
from pathlib import Path
from typing import Any

import pytest
import yaml
from conftest import make_client, make_wav
from kitsune_server import cli, lifecycle, server
from kitsune_server.config import ServerConfig
from kitsune_server.engine import EngineLoadError
from kitsune_server.models import CatalogError, DownloadedModel
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
    built: list[ServerConfig] = []
    monkeypatch.setattr(
        server, "build_model_manager", lambda config, **kwargs: built.append(config)
    )

    def taken(host: str, port: int) -> None:
        raise server.PortInUseError(f"server.port {port} is already in use on {host}")

    with pytest.raises(server.PortInUseError):
        server.run_server(ServerConfig(), port_check=taken)

    assert built == []


def test_run_server_logs_resolved_engine(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    monkeypatch.setattr(server, "detect_cuda_devices", lambda: 0)
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
    monkeypatch.setattr(server, "detect_cuda_devices", lambda: 0)
    monkeypatch.setattr(server.uvicorn, "run", lambda *args, **kwargs: None)

    with caplog.at_level(logging.INFO, logger="kitsune.server"):
        server.run_server(ServerConfig(), port_check=allow_port)

    warnings = [
        record.getMessage() for record in caplog.records if record.levelno == logging.WARNING
    ]
    assert any("CPU" in message and "base/tiny" in message for message in warnings)


def test_run_server_rejects_pinned_cuda_without_the_runtime(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    monkeypatch.setattr(server, "detect_cuda_devices", lambda: 0)
    built: list[object] = []
    monkeypatch.setattr(server, "build_model_manager", lambda *args, **kwargs: built.append(True))

    with pytest.raises(EngineLoadError) as caught:
        server.run_server(ServerConfig(device="cuda"), port_check=allow_port)

    assert built == []
    assert "nvidia-cublas-cu12" in str(caught.value)


def test_run_server_does_not_warn_on_cuda(
    monkeypatch: pytest.MonkeyPatch, caplog: pytest.LogCaptureFixture
) -> None:
    monkeypatch.setattr(server, "detect_cuda_devices", lambda: 1)
    monkeypatch.setattr(server.uvicorn, "run", lambda *args, **kwargs: None)

    with caplog.at_level(logging.INFO, logger="kitsune.server"):
        server.run_server(ServerConfig(), port_check=allow_port)

    assert not any(record.levelno == logging.WARNING for record in caplog.records)


def _capture_uvicorn(monkeypatch: pytest.MonkeyPatch) -> dict[str, object]:
    captured: dict[str, object] = {}
    monkeypatch.setattr(server, "detect_cuda_devices", lambda: 0)

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
    real_build = server.build_model_manager

    def capture(config: ServerConfig, *, cuda_device_count: int = 0, **kwargs: Any) -> Any:
        seen["devices"] = cuda_device_count
        return real_build(config, cuda_device_count=cuda_device_count, **kwargs)

    monkeypatch.setattr(server, "build_model_manager", capture)
    monkeypatch.setattr(server.uvicorn, "run", lambda *args, **kwargs: None)

    server.run_server(ServerConfig(), port_check=allow_port)

    assert detections == [True]
    assert seen["devices"] == 2


class _RecordingLoader:
    """Records model loads and returns a fake, so lifecycle tests stay offline."""

    def __init__(self) -> None:
        self.calls: list[str] = []

    def load(self, config: ServerConfig) -> FakeTranscriber:
        self.calls.append(config.model)
        name = Path(config.model).name or config.model
        return FakeTranscriber(
            info=EngineInfo(name, "cpu", "int8"),
            text="downloaded model",
        )


class _RecordingDownloader:
    """Writes a minimal local model instead of fetching from Hugging Face."""

    def __init__(self) -> None:
        self.calls: list[str] = []

    def fetch(
        self,
        spec: Any,
        target: Path,
        report: Any,
        cancelled: Any,
    ) -> DownloadedModel:
        self.calls.append(spec.name)
        target.mkdir(parents=True, exist_ok=True)
        (target / "model.bin").write_bytes(b"weights")
        (target / "config.json").write_text("{}", encoding="utf-8")
        report(spec.disk_bytes, spec.disk_bytes)
        return DownloadedModel(path=target)


def _start_offline_server(
    tmp_path: Path,
    monkeypatch: pytest.MonkeyPatch,
    *,
    loader: _RecordingLoader,
    downloader: _RecordingDownloader,
) -> Any:
    """Run the real startup wiring with fake model ports, returning the ASGI app."""
    monkeypatch.setattr(server, "WhisperLoader", lambda **kwargs: loader)
    monkeypatch.setattr(server, "HttpModelDownloader", lambda **kwargs: downloader)
    monkeypatch.setattr(server, "detect_cuda_devices", lambda: 0)
    captured: dict[str, Any] = {}

    def capture(app: Any, **kwargs: Any) -> None:
        captured["app"] = app

    monkeypatch.setattr(server.uvicorn, "run", capture)
    config = ServerConfig(workers=1, download_root=str(tmp_path / "models"))
    server.run_server(config, port_check=allow_port, log_file=tmp_path / "server.log")
    return captured["app"]


async def test_run_server_starts_without_a_model_and_fetches_nothing(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    loader = _RecordingLoader()
    downloader = _RecordingDownloader()
    app = _start_offline_server(tmp_path, monkeypatch, loader=loader, downloader=downloader)

    async with make_client(app) as client:
        health = await client.get("/health")
        before = await client.post(
            "/transcribe",
            files={"audio": ("clip.wav", make_wav(0.2), "audio/wav")},
        )

    assert health.status_code == 200
    body = health.json()
    assert body["model"] == ""
    assert body["model_state"] == "cold"
    assert body["loaded"] is False
    assert before.status_code == 503
    assert before.json()["error"]["code"] == "model_unavailable"
    # Startup and the refused transcription must not touch the network seam.
    assert loader.calls == []
    assert downloader.calls == []


async def test_a_downloaded_model_is_used_without_a_restart(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    loader = _RecordingLoader()
    downloader = _RecordingDownloader()
    app = _start_offline_server(tmp_path, monkeypatch, loader=loader, downloader=downloader)

    async with make_client(app) as client:
        before = await client.post(
            "/transcribe",
            files={"audio": ("clip.wav", make_wav(0.2), "audio/wav")},
        )
        assert before.status_code == 503

        queued = await client.post("/admin/models/download", json={"model": "base"})
        assert queued.status_code == 202
        view: dict[str, Any] = {}
        for _ in range(500):
            view = (await client.get("/admin/models")).json()
            if view["active_model"] == "base" and view["state"] == "loaded":
                break
            await asyncio.sleep(0.01)

        health = await client.get("/health")
        transcribed = await client.post(
            "/transcribe",
            files={"audio": ("clip.wav", make_wav(0.2), "audio/wav")},
        )

    assert view["active_model"] == "base"
    assert view["state"] == "loaded"
    assert health.json()["model"] == "base"
    assert health.json()["loaded"] is True
    assert transcribed.status_code == 200
    assert transcribed.json()["text"] == "downloaded model"
    assert downloader.calls == ["base"]
    assert loader.calls and loader.calls[-1].endswith("base")


def test_run_server_writes_its_output_to_the_log_file(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(server, "detect_cuda_devices", lambda: 0)
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

    def taken(
        config: ServerConfig,
        log_file: Path | None = None,
        config_store: object = None,
    ) -> None:
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


def test_lifecycle_state_and_log_share_the_xdg_state_home(tmp_path: Path) -> None:
    """The Docker image sets XDG_STATE_HOME=/state instead of adding a schema field."""

    env = {"XDG_STATE_HOME": str(tmp_path / "state")}
    state = tmp_path / "state" / "kitsune-whisper"

    assert lifecycle.lifecycle_state_path(env) == state / "lifecycle.json"
    assert server.default_log_file(env) == state / "server.log"


def test_exit_for_restart_uses_the_supervisor_restart_code(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    """ADR 0007: the supervisor restarts on this exit, Docker via its policy."""

    exits: list[int] = []
    monkeypatch.setattr(server.os, "_exit", exits.append)
    monkeypatch.setattr(server.logging, "shutdown", lambda: None)

    server.exit_for_restart()

    assert exits == [lifecycle.RESTART_EXIT_CODE]
    assert lifecycle.RESTART_EXIT_CODE == 75


def test_run_server_survives_an_unwritable_log_path(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    monkeypatch.setattr(server, "detect_cuda_devices", lambda: 0)
    monkeypatch.setattr(server.uvicorn, "run", lambda *args, **kwargs: None)
    blocker = tmp_path / "not-a-directory"
    blocker.write_text("", encoding="utf-8")

    server.run_server(ServerConfig(), port_check=allow_port, log_file=blocker / "server.log")


def test_main_reports_a_bad_catalog_as_a_config_error(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    path = tmp_path / "kitsune.yaml"
    path.write_text(yaml.safe_dump({"server": {"model": "small"}}), encoding="utf-8")

    def boom(
        config: ServerConfig,
        *,
        log_file: Path | None = None,
        config_store: object = None,
    ) -> None:
        raise CatalogError("catalog model name must be a plain file name")

    monkeypatch.setattr(server, "run_server", boom)

    code = cli.main(["--config", str(path)])

    assert code == 1
    assert "config error: catalog" in capsys.readouterr().err


def test_main_reports_engine_load_error(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch, capsys: pytest.CaptureFixture[str]
) -> None:
    path = tmp_path / "kitsune.yaml"
    path.write_text(yaml.safe_dump({"server": {"model": "small"}}), encoding="utf-8")

    def boom(
        config: ServerConfig,
        *,
        log_file: Path | None = None,
        config_store: object = None,
    ) -> None:
        raise EngineLoadError("model unavailable")

    monkeypatch.setattr(server, "run_server", boom)

    code = cli.main(["--config", str(path)])

    assert code == 1
    assert "engine error: model unavailable" in capsys.readouterr().err


def test_release_version_reads_the_installed_bundle(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    root = tmp_path / "server"
    package = root / "lib" / "kitsune_server"
    package.mkdir(parents=True)
    (root / "bundle.json").write_text('{"version": "v0.5.0"}\n', encoding="utf-8")
    monkeypatch.setattr(server, "__file__", str(package / "server.py"))

    assert server.release_version() == "v0.5.0"


def test_release_version_falls_back_to_the_package_version(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    package = tmp_path / "lib" / "kitsune_server"
    package.mkdir(parents=True)
    monkeypatch.setattr(server, "__file__", str(package / "server.py"))

    assert server.release_version() == server.__version__


def test_release_version_honours_the_environment(monkeypatch: pytest.MonkeyPatch) -> None:
    monkeypatch.setenv("KITSUNE_SERVER_VERSION", "v0.9.0")

    assert server.release_version() == "v0.9.0"
