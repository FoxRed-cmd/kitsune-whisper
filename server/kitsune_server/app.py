"""The FastAPI application: the HTTP seam shared with the Client.

Built by :func:`create_app`, which takes the resolved config and a
``Transcriber`` port so tests can register a fake and load no model.
"""

from __future__ import annotations

import logging
import threading
from collections.abc import Mapping
from typing import Annotated, Any

from fastapi import FastAPI, File, Form, UploadFile
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from starlette.concurrency import run_in_threadpool
from starlette.exceptions import HTTPException as StarletteHTTPException

from .audio import AudioDecodeError, decode_audio
from .config import ServerConfig
from .transcriber import Transcriber

logger = logging.getLogger("kitsune.server")


class ApiError(Exception):
    """An error rendered through the ``{error:{code,message}}`` envelope."""

    def __init__(
        self,
        status_code: int,
        code: str,
        message: str,
        headers: Mapping[str, str] | None = None,
    ) -> None:
        super().__init__(message)
        self.status_code = status_code
        self.code = code
        self.message = message
        self.headers = dict(headers or {})


def _resolve_workers(config: ServerConfig, transcriber: Transcriber) -> int:
    """One knob for HTTP concurrency and CT2 workers (0 = auto)."""
    if config.workers > 0:
        return config.workers
    return 1 if transcriber.info.device == "cuda" else 2


def _effective_language(request_language: str | None, config: ServerConfig) -> str | None:
    value = request_language
    if value is None or value.strip().lower() in ("", "auto"):
        value = config.decode.language
    if value is None or value.strip().lower() in ("", "auto"):
        return None
    return value


def _effective_prompt(request_prompt: str | None, config: ServerConfig) -> str | None:
    if request_prompt:
        return request_prompt
    return config.decode.initial_prompt or None


def _validation_message(exc: RequestValidationError) -> str:
    parts = []
    for error in exc.errors():
        loc = ".".join(str(item) for item in error["loc"][1:]) or "body"
        parts.append(f"{loc}: {error['msg']}")
    return "; ".join(parts)


def create_app(config: ServerConfig, transcriber: Transcriber) -> FastAPI:
    """Build the ASGI app around a config and a ``Transcriber``."""
    app = FastAPI(title="kitsune-whisper server", docs_url=None, redoc_url=None)
    app.state.config = config
    app.state.transcriber = transcriber
    app.state.ready = True
    app.state.semaphore = threading.Semaphore(_resolve_workers(config, transcriber))

    @app.exception_handler(ApiError)
    async def _handle_api_error(_request: Any, exc: ApiError) -> JSONResponse:
        return JSONResponse(
            status_code=exc.status_code,
            content={"error": {"code": exc.code, "message": exc.message}},
            headers=exc.headers,
        )

    @app.exception_handler(RequestValidationError)
    async def _handle_validation_error(_request: Any, exc: RequestValidationError) -> JSONResponse:
        return JSONResponse(
            status_code=400,
            content={"error": {"code": "bad_request", "message": _validation_message(exc)}},
        )

    @app.exception_handler(StarletteHTTPException)
    async def _handle_http_exception(_request: Any, exc: StarletteHTTPException) -> JSONResponse:
        code = {404: "not_found", 405: "method_not_allowed"}.get(exc.status_code, "error")
        return JSONResponse(
            status_code=exc.status_code,
            content={"error": {"code": code, "message": str(exc.detail)}},
            headers=getattr(exc, "headers", None),
        )

    @app.exception_handler(Exception)
    async def _handle_unexpected(_request: Any, exc: Exception) -> JSONResponse:
        logger.exception("unhandled error: %s", exc)
        return JSONResponse(
            status_code=500,
            content={"error": {"code": "internal_error", "message": "internal server error"}},
        )

    @app.get("/health")
    async def health() -> dict[str, Any]:
        info = transcriber.info
        return {
            "status": "ok",
            "model": info.model,
            "device": info.device,
            "compute_type": info.compute_type,
            "ready": bool(app.state.ready),
        }

    @app.post("/transcribe")
    async def transcribe(
        audio: Annotated[UploadFile, File()],
        language: Annotated[str | None, Form()] = None,
        initial_prompt: Annotated[str | None, Form()] = None,
    ) -> dict[str, Any]:
        if not app.state.semaphore.acquire(blocking=False):
            raise ApiError(503, "unavailable", "server is busy", {"Retry-After": "1"})
        try:
            data = await audio.read()
            max_bytes = int(config.max_upload_mb * 1024 * 1024)
            if len(data) > max_bytes:
                raise ApiError(
                    413,
                    "payload_too_large",
                    f"upload exceeds {config.max_upload_mb:g} MB",
                )
            try:
                clip = await run_in_threadpool(decode_audio, data)
            except AudioDecodeError as exc:
                raise ApiError(415, "unsupported_media", f"could not decode audio: {exc}") from exc
            if clip.duration > config.max_audio_seconds:
                raise ApiError(
                    413,
                    "payload_too_large",
                    f"audio exceeds {config.max_audio_seconds} s",
                )
            result = await run_in_threadpool(
                transcriber.transcribe,
                clip,
                language=_effective_language(language, config),
                initial_prompt=_effective_prompt(initial_prompt, config),
            )
        finally:
            app.state.semaphore.release()

        return {
            "text": result.text,
            "language": result.language,
            "language_probability": result.language_probability,
            "duration": result.duration,
        }

    return app
