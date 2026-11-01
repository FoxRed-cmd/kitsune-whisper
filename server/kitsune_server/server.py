"""Runtime wiring: build the real faster-whisper transcriber and serve it."""

from __future__ import annotations

import errno
import logging
import os
import socket
import sys
import time
from collections.abc import Callable
from pathlib import Path
from typing import TextIO

import uvicorn

from .app import create_app
from .config import CONFIG_FILENAME, ServerConfig, user_config_dir, user_state_dir
from .engine import WhisperTranscriber
from .gpu import detect_cuda_devices, resolve_engine
from .lifecycle import RESTART_EXIT_CODE, Lifecycle, LifecycleError, lifecycle_state_path
from .llm import OpenAITextProcessor
from .models import (
    FileModelStorage,
    HttpModelDownloader,
    ModelCatalog,
    ModelManager,
    WhisperLoader,
)
from .pairing import PairingCodeStore, pairing_store_path
from .processor import DisabledTextProcessor, TextProcessor
from .store import ConfigStore
from .transcriber import EngineInfo, Transcriber
from .trust import (
    CERT_FILENAME,
    KEY_FILENAME,
    Certificate,
    DeviceStore,
    device_store_path,
    load_or_create_certificate,
)

logger = logging.getLogger("kitsune.server")

LOG_FORMAT = "%(asctime)s %(levelname)s %(name)s %(message)s"


def default_log_file(env: dict[str, str] | None = None) -> Path:
    """The log file a supervised Server writes, next to the user's state data.

    Task Scheduler discards ``pythonw.exe`` output, so a hidden Windows start
    that fails is only diagnosable through this file.
    """
    return user_state_dir(env) / "server.log"


def configure_logging(
    level: str,
    *,
    log_file: Path | None = None,
    stream: TextIO = sys.stderr,
) -> None:
    """Send Server output to stderr and, when given, an appended log file."""
    root = logging.getLogger("kitsune")
    root.setLevel(level.upper())
    # Keep records flowing to the root logger so a test harness (or an embedding
    # process) still sees them.
    root.propagate = True
    for handler in list(root.handlers):
        root.removeHandler(handler)
        handler.close()
    formatter = logging.Formatter(LOG_FORMAT)
    handlers: list[logging.Handler] = [logging.StreamHandler(stream)]
    log_failed = False
    if log_file is not None:
        try:
            log_file.parent.mkdir(parents=True, exist_ok=True)
            handlers.append(logging.FileHandler(log_file, encoding="utf-8"))
        except OSError:
            # An unwritable state directory must not stop the Server; the
            # stderr handler still carries every message.
            log_failed = True
    for handler in handlers:
        handler.setFormatter(formatter)
        root.addHandler(handler)
    if log_failed:
        logger.warning("cannot write the Server log to %s; logging to stderr only", log_file)


def record_failure(log_file: Path | None, message: str) -> None:
    """Append one fatal startup line to the log; best-effort."""
    if log_file is None:
        return
    line = " ".join(message.split())
    try:
        log_file.parent.mkdir(parents=True, exist_ok=True)
        with log_file.open("a", encoding="utf-8") as handle:
            handle.write(f"{line}\n")
    except OSError:
        # A log that cannot be written must not hide the stderr message.
        pass


CPU_ESCAPE_HATCH = (
    "running on CPU (the slow path): use a CUDA GPU for near-instant transcription, "
    "or set server.model to base/tiny for a smaller, faster CPU model"
)

PortCheck = Callable[[str, int], None]


class PortInUseError(RuntimeError):
    """The configured address is already bound, so the listener cannot start."""


def check_port_available(
    host: str,
    port: int,
    *,
    socket_factory: Callable[[int, int], socket.socket] = socket.socket,
) -> None:
    """Bind-check ``host:port`` so a taken port fails with one actionable line.

    uvicorn's own bind failure surfaces as a traceback; the preflight turns the
    common "something else already listens on 8765" case into a message that
    names the setting to change.
    """
    family = socket.AF_INET6 if ":" in host else socket.AF_INET
    try:
        with socket_factory(family, socket.SOCK_STREAM) as probe:
            # Mirror uvicorn: reuse a port left in TIME_WAIT after a crash or a
            # supervised restart. On Windows SO_REUSEADDR would hijack an
            # actively bound port, so the preflight keeps the strict check there.
            if sys.platform != "win32":
                probe.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
            probe.bind((host, port))
    except OSError as exc:
        reason = exc.strerror or str(exc)
        location = user_config_dir() / CONFIG_FILENAME
        problem = "is already in use" if exc.errno == errno.EADDRINUSE else "cannot be bound"
        raise PortInUseError(
            f"server.port {port} {problem} on {host} ({reason}); "
            f"change server.port in {location} or set KITSUNE_SERVER_PORT "
            "to a free port"
        ) from exc


def tls_paths(config: ServerConfig, *, data_dir: Path | None = None) -> tuple[Path, Path]:
    """The certificate pair the listener uses: explicit overrides or the data dir."""
    if config.tls.cert_file and config.tls.key_file:
        return Path(config.tls.cert_file).expanduser(), Path(config.tls.key_file).expanduser()
    base = data_dir if data_dir is not None else user_state_dir() / "tls"
    return base / CERT_FILENAME, base / KEY_FILENAME


def build_trust(
    config: ServerConfig,
    *,
    data_dir: Path | None = None,
) -> tuple[Certificate | None, DeviceStore]:
    """Build the TLS identity and the Device store the Server runs with."""
    certificate = None
    if config.tls.enabled:
        cert_file, key_file = tls_paths(config, data_dir=data_dir)
        certificate = load_or_create_certificate(cert_file, key_file)
    store = DeviceStore(device_store_path(config, data_dir=data_dir))
    return certificate, store


def build_pairing_codes(
    config: ServerConfig,
    *,
    data_dir: Path | None = None,
) -> PairingCodeStore:
    return PairingCodeStore(pairing_store_path(config, data_dir=data_dir))


def build_transcriber(
    config: ServerConfig,
    *,
    cuda_device_count: int | None = None,
) -> Transcriber:
    return WhisperTranscriber.load(config, cuda_device_count=cuda_device_count)


def build_soft_stop(
    config: ServerConfig,
    *,
    info: EngineInfo,
    state_path: Path | str | None = None,
) -> Lifecycle:
    """The persisted soft-stop flag; the ModelManager owns the model itself."""
    return Lifecycle(
        _no_model_load,
        info=info,
        state_path=state_path if state_path is not None else lifecycle_state_path(),
    )


def _no_model_load() -> Transcriber:
    raise LifecycleError("the model lifecycle is managed by the model manager")


def exit_for_restart() -> None:
    """Exit with the supervisor-visible restart code (ADR 0007)."""
    logger.warning(
        "restarting: exiting with %s so the supervisor starts the Server again",
        RESTART_EXIT_CODE,
    )
    logging.shutdown()
    os._exit(RESTART_EXIT_CODE)


def build_model_manager(
    config: ServerConfig,
    *,
    transcriber: Transcriber | None,
    cuda_device_count: int = 0,
) -> ModelManager:
    """Wire the model lifecycle to the real loader, downloader, and model directory."""
    return ModelManager(
        config,
        transcriber=transcriber,
        loader=WhisperLoader(cuda_device_count=cuda_device_count),
        downloader=HttpModelDownloader(),
        storage=FileModelStorage(config.download_root),
        clock=time.monotonic,
        cuda_device_count=cuda_device_count,
    )


def build_processor(config: ServerConfig) -> TextProcessor:
    """Build the processing port; the endpoint is called lazily per stage."""
    if not config.processing.enabled:
        return DisabledTextProcessor()
    return OpenAITextProcessor(config.processing)


def run_server(
    config: ServerConfig,
    *,
    port_check: PortCheck = check_port_available,
    log_file: Path | None = None,
    config_store: ConfigStore | None = None,
) -> None:
    configure_logging(config.log_level, log_file=log_file)
    # Fail before downloading or loading a model when the listener cannot bind.
    port_check(config.bind_host, config.port)
    certificate, devices = build_trust(config)
    pairing_codes = build_pairing_codes(config)
    if certificate is not None:
        logger.info(
            "TLS certificate %s (fingerprint %s)",
            "generated" if certificate.created else "loaded",
            certificate.fingerprint,
        )
    else:
        logger.warning(
            "server.tls.enabled is false: the Server is serving plain HTTP and "
            "Device tokens cross the network in clear text"
        )
    # Detect once so model loading and /status cannot disagree.
    cuda_devices = detect_cuda_devices()
    # Validate the override catalog before downloading or loading a model, so a
    # bad catalog fails with a config error instead of after a costly load.
    ModelCatalog.load(config)
    resolved_info = resolve_engine(config, cuda_device_count=cuda_devices)
    lifecycle = build_soft_stop(config, info=resolved_info)
    if lifecycle.stopped:
        logger.warning(
            "Server is soft-stopped: the model is not loaded and /transcribe returns 503 "
            "until POST /admin/start"
        )
        transcriber: Transcriber | None = None
        info = resolved_info
    else:
        transcriber = build_transcriber(config, cuda_device_count=cuda_devices)
        info = transcriber.info
    resolved_note = (
        " (resolved from auto)" if "auto" in (config.device, config.compute_type) else ""
    )
    logger.info(
        "model=%s device=%s compute_type=%s%s",
        info.model,
        info.device,
        info.compute_type,
        resolved_note,
    )
    if info.device == "cpu":
        logger.warning(CPU_ESCAPE_HATCH)
    processor = build_processor(config)
    models = build_model_manager(config, transcriber=transcriber, cuda_device_count=cuda_devices)
    app = create_app(
        config,
        transcriber if transcriber is not None else lifecycle,
        processor,
        cuda_device_count=cuda_devices,
        devices=devices,
        certificate=certificate,
        pairing_codes=pairing_codes,
        config_store=config_store if config_store is not None else ConfigStore(),
        models=models,
        lifecycle=lifecycle,
        log_file=log_file if log_file is not None else default_log_file(),
        restart=exit_for_restart,
    )
    if config.expose_to_network:
        logger.warning(
            "server.expose_to_network is on: listening on %s:%s and reachable from the LAN",
            config.bind_host,
            config.port,
        )
    models.start_idle_monitor()
    try:
        if certificate is not None:
            uvicorn.run(
                app,
                host=config.bind_host,
                port=config.port,
                log_level=config.log_level,
                ssl_certfile=str(certificate.cert_file),
                ssl_keyfile=str(certificate.key_file),
            )
            return
        uvicorn.run(app, host=config.bind_host, port=config.port, log_level=config.log_level)
    finally:
        models.stop_idle_monitor()
