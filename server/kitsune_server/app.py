"""The FastAPI application: the HTTP seam shared with the Client.

Built by :func:`create_app`, which takes the resolved config plus a
``Transcriber`` and a ``TextProcessor`` port, so tests can register fakes and
load no model.
"""

from __future__ import annotations

import asyncio
import functools
import logging
import threading
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from typing import Annotated, Any

import anyio.to_thread
from fastapi import FastAPI, File, Form, UploadFile
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from starlette.concurrency import run_in_threadpool
from starlette.exceptions import HTTPException as StarletteHTTPException

from .audio import AudioDecodeError, decode_audio
from .config import ServerConfig, format_errors
from .languages import normalize_language
from .processor import ProcessingError, TextProcessor
from .transcriber import Transcriber, resolve_workers

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


def _error_response(
    status_code: int,
    code: str,
    message: str,
    headers: Mapping[str, str] | None = None,
) -> JSONResponse:
    return JSONResponse(
        status_code=status_code,
        content={"error": {"code": code, "message": message}},
        headers=headers,
    )


def _effective_language(request_language: str | None, config: ServerConfig) -> str | None:
    value = request_language
    if value is None or value.strip().lower() in ("", "auto"):
        value = config.decode.language
    if value is None or value.strip().lower() in ("", "auto"):
        return None
    try:
        return normalize_language(value)
    except ValueError as exc:
        raise ApiError(400, "bad_request", str(exc)) from exc


def _effective_prompt(request_prompt: str | None, config: ServerConfig) -> str | None:
    if request_prompt:
        return request_prompt
    return config.decode.initial_prompt or None


@dataclass(frozen=True)
class _StageOutcome:
    """The result of one processing stage: delivered text, whether it ran, and why not."""

    text: str
    applied: bool
    warning: str | None


def create_app(
    config: ServerConfig,
    transcriber: Transcriber,
    processor: TextProcessor,
) -> FastAPI:
    """Build the ASGI app around a config, a ``Transcriber`` and a ``TextProcessor``."""
    app = FastAPI(title="kitsune-whisper server", docs_url=None, redoc_url=None)
    app.state.config = config
    app.state.transcriber = transcriber
    app.state.processor = processor
    app.state.ready = True
    app.state.semaphore = threading.Semaphore(
        resolve_workers(config.workers, transcriber.info.device)
    )

    @app.exception_handler(ApiError)
    async def _handle_api_error(_request: Any, exc: ApiError) -> JSONResponse:
        return _error_response(exc.status_code, exc.code, exc.message, exc.headers)

    @app.exception_handler(RequestValidationError)
    async def _handle_validation_error(_request: Any, exc: RequestValidationError) -> JSONResponse:
        return _error_response(400, "bad_request", format_errors(exc.errors(), drop=1))

    @app.exception_handler(StarletteHTTPException)
    async def _handle_http_exception(_request: Any, exc: StarletteHTTPException) -> JSONResponse:
        return _error_response(
            exc.status_code,
            "error",
            str(exc.detail),
            getattr(exc, "headers", None),
        )

    @app.exception_handler(Exception)
    async def _handle_unexpected(_request: Any, exc: Exception) -> JSONResponse:
        logger.exception("unhandled error: %s", exc)
        return _error_response(500, "internal_error", "internal server error")

    async def _run_stage(
        name: str,
        stage: Callable[..., str],
        available: bool,
        text: str,
        language: str,
    ) -> _StageOutcome:
        """Run one processing stage, degrading to ``text`` on any failure."""
        if not processor.info.enabled:
            return _StageOutcome(text, False, f"{name} requested but processing is disabled")
        if not available:
            return _StageOutcome(text, False, f"{name} requested but unavailable")
        timeout = config.processing.stage_timeout_seconds
        try:
            output = await asyncio.wait_for(
                anyio.to_thread.run_sync(
                    functools.partial(stage, text, language=language),
                    abandon_on_cancel=True,
                ),
                timeout=timeout,
            )
        except TimeoutError:
            logger.warning("%s timed out after %ss", name, timeout)
            return _StageOutcome(text, False, f"{name} timed out after {timeout:g}s")
        except Exception as exc:
            if isinstance(exc, ProcessingError):
                logger.warning("%s failed: %s", name, exc)
            else:
                logger.exception("%s raised unexpectedly", name)
            return _StageOutcome(text, False, f"{name} failed: {exc}")
        if not output or not output.strip():
            logger.warning("%s produced empty text", name)
            return _StageOutcome(text, False, f"{name} produced no usable text")
        return _StageOutcome(output, True, None)

    @app.get("/health")
    async def health() -> dict[str, Any]:
        info = transcriber.info
        capability = processor.info
        return {
            "status": "ok",
            "model": info.model,
            "device": info.device,
            "compute_type": info.compute_type,
            "ready": bool(app.state.ready),
            "processing": {
                "enabled": capability.enabled,
                "refine": capability.refine,
                "summarize": capability.summarize,
            },
        }

    @app.post("/transcribe")
    async def transcribe(
        audio: Annotated[UploadFile, File()],
        language: Annotated[str | None, Form()] = None,
        initial_prompt: Annotated[str | None, Form()] = None,
        refine: Annotated[bool, Form()] = False,
        summarize: Annotated[bool, Form()] = False,
    ) -> dict[str, Any]:
        if not app.state.semaphore.acquire(blocking=False):
            raise ApiError(503, "unavailable", "server is busy", {"Retry-After": "1"})
        try:
            max_bytes = int(config.max_upload_mb * 1024 * 1024)
            declared_size = getattr(audio, "size", None)
            if declared_size is not None and declared_size > max_bytes:
                raise ApiError(
                    413,
                    "payload_too_large",
                    f"upload exceeds {config.max_upload_mb:g} MB",
                )
            data = await audio.read()
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

        raw_text = result.text
        delivered = raw_text
        applied = {"refine": False, "summarize": False}
        warnings: list[str] = []
        prompt_language = result.language if config.decode.task == "transcribe" else "en"

        # Fixed pipeline: Refine always precedes Summarize, each built on the
        # previous stage's delivered text.
        capability = processor.info
        stages = (
            ("refine", processor.refine, capability.refine, refine),
            ("summarize", processor.summarize, capability.summarize, summarize),
        )
        for name, stage, available, requested in stages:
            if not requested:
                continue
            outcome = await _run_stage(name, stage, available, delivered, prompt_language)
            delivered = outcome.text
            applied[name] = outcome.applied
            if outcome.warning is not None:
                warnings.append(outcome.warning)

        return {
            "text": delivered,
            "raw_text": raw_text,
            "language": result.language,
            "language_probability": result.language_probability,
            "duration": result.duration,
            "applied": applied,
            "warnings": warnings,
        }

    return app
