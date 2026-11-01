"""Runtime wiring: build the real faster-whisper transcriber and serve it."""

from __future__ import annotations

import logging
import socket
import sys
from collections.abc import Callable

import uvicorn

from .app import create_app
from .config import CONFIG_FILENAME, ServerConfig, user_config_dir
from .engine import WhisperTranscriber
from .gpu import detect_cuda_devices
from .llm import OpenAITextProcessor
from .processor import DisabledTextProcessor, TextProcessor
from .transcriber import Transcriber

logger = logging.getLogger("kitsune.server")

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
        raise PortInUseError(
            f"server.port {port} is already in use on {host} ({reason}); "
            f"change server.port in {location} or set KITSUNE_SERVER_PORT "
            "to a free port"
        ) from exc


def build_transcriber(config: ServerConfig) -> Transcriber:
    return WhisperTranscriber.load(config)


def build_processor(config: ServerConfig) -> TextProcessor:
    """Build the processing port; the endpoint is called lazily per stage."""
    if not config.processing.enabled:
        return DisabledTextProcessor()
    return OpenAITextProcessor(config.processing)


def run_server(
    config: ServerConfig,
    *,
    port_check: PortCheck = check_port_available,
) -> None:
    logging.basicConfig(
        level=config.log_level.upper(),
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )
    # Fail before downloading or loading a model when the listener cannot bind.
    port_check(config.bind_host, config.port)
    transcriber = build_transcriber(config)
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
        cuda_device_count=detect_cuda_devices(),
    )
    if config.expose_to_network:
        logger.warning(
            "server.expose_to_network is on: listening on %s:%s and reachable from the LAN",
            config.bind_host,
            config.port,
        )
    uvicorn.run(app, host=config.bind_host, port=config.port, log_level=config.log_level)
