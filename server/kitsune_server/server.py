"""Runtime wiring: build the real faster-whisper transcriber and serve it."""

from __future__ import annotations

import logging

import uvicorn

from .app import create_app
from .config import ServerConfig
from .engine import WhisperTranscriber
from .llm import OpenAITextProcessor
from .processor import DisabledTextProcessor, TextProcessor
from .transcriber import Transcriber

logger = logging.getLogger("kitsune.server")

CPU_ESCAPE_HATCH = (
    "running on CPU (the slow path): use a CUDA GPU for near-instant transcription, "
    "or set server.model to base/tiny for a smaller, faster CPU model"
)


def build_transcriber(config: ServerConfig) -> Transcriber:
    return WhisperTranscriber.load(config)


def build_processor(config: ServerConfig) -> TextProcessor:
    """Build the processing port; the endpoint is called lazily per stage."""
    if not config.processing.enabled:
        return DisabledTextProcessor()
    return OpenAITextProcessor(config.processing)


def run_server(config: ServerConfig) -> None:
    logging.basicConfig(
        level=config.log_level.upper(),
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )
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
    app = create_app(config, transcriber, processor)
    uvicorn.run(app, host=config.host, port=config.port, log_level=config.log_level)
