"""Runtime wiring: build the real faster-whisper transcriber and serve it."""

from __future__ import annotations

import errno
import logging
import socket
import sys
from collections.abc import Callable
from pathlib import Path
from typing import TextIO

import uvicorn

from .app import create_app
from .config import CONFIG_FILENAME, ServerConfig, user_config_dir, user_state_dir
from .engine import WhisperTranscriber
from .gpu import detect_cuda_devices
from .llm import OpenAITextProcessor
from .processor import DisabledTextProcessor, TextProcessor
from .transcriber import Transcriber

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


def build_transcriber(
    config: ServerConfig,
    *,
    cuda_device_count: int | None = None,
) -> Transcriber:
    return WhisperTranscriber.load(config, cuda_device_count=cuda_device_count)


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
) -> None:
    configure_logging(config.log_level, log_file=log_file)
    # Fail before downloading or loading a model when the listener cannot bind.
    port_check(config.bind_host, config.port)
    # Detect once so model loading and /health cannot disagree.
    cuda_devices = detect_cuda_devices()
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
    app = create_app(
        config,
        transcriber,
        processor,
        cuda_device_count=cuda_devices,
    )
    if config.expose_to_network:
        logger.warning(
            "server.expose_to_network is on: listening on %s:%s and reachable from the LAN",
            config.bind_host,
            config.port,
        )
    uvicorn.run(app, host=config.bind_host, port=config.port, log_level=config.log_level)
