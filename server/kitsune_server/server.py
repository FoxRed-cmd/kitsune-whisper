"""Runtime wiring: build the app around the fake transcriber and serve it.

Ticket #13 runs the Server with a fake transcriber so the API is exercisable
without a model; ticket #14 swaps in the real faster-whisper adapter here.
"""

from __future__ import annotations

import logging

import uvicorn

from .app import create_app
from .config import ServerConfig
from .transcriber import EngineInfo, FakeTranscriber, Transcriber

logger = logging.getLogger("kitsune.server")


def build_transcriber(config: ServerConfig) -> Transcriber:
    return FakeTranscriber(
        info=EngineInfo(
            model=config.model,
            device=config.device,
            compute_type=config.compute_type,
        )
    )


def run_server(config: ServerConfig) -> None:
    logging.basicConfig(
        level=config.log_level.upper(),
        format="%(asctime)s %(levelname)s %(name)s %(message)s",
    )
    logger.info(
        "starting server model=%s device=%s compute_type=%s workers=%s",
        config.model,
        config.device,
        config.compute_type,
        config.workers or "auto",
    )
    app = create_app(config, build_transcriber(config))
    uvicorn.run(app, host=config.host, port=config.port, log_level=config.log_level)
