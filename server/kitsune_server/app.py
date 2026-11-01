"""The FastAPI application: the HTTP seam shared with the Client.

Built by :func:`create_app`, which takes the resolved config plus a
``Transcriber`` and a ``TextProcessor`` port, so tests can register fakes and
load no model.
"""

from __future__ import annotations

import asyncio
import functools
import ipaddress
import logging
import threading
from collections.abc import Callable, Mapping
from dataclasses import dataclass
from typing import Annotated, Any

import anyio.to_thread
from fastapi import Depends, FastAPI, File, Form, Request, UploadFile
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field
from starlette.concurrency import run_in_threadpool
from starlette.exceptions import HTTPException as StarletteHTTPException

from . import __version__
from .audio import AudioDecodeError, decode_audio
from .config import ServerConfig, format_errors
from .gpu import detect_cuda_devices, gpu_status
from .languages import normalize_language
from .processor import ProcessingError, TextProcessor
from .transcriber import Transcriber, resolve_workers
from .trust import Certificate, DeviceStore
from .trust import Device as TrustedDevice

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


class LocalPairRequest(BaseModel):
    """The body of the loopback auto-pair request."""

    name: str = Field(default="Local client", min_length=1, max_length=128)


def is_loopback_address(host: str | None) -> bool:
    """Return True when the HTTP peer is on the loopback interface."""
    if not host:
        return False
    candidate = host.strip()
    # An IPv4-mapped IPv6 peer (::ffff:127.0.0.1) is still loopback.
    if candidate.lower().startswith("::ffff:"):
        candidate = candidate[7:]
    try:
        return ipaddress.ip_address(candidate).is_loopback
    except ValueError:
        return False


def create_app(
    config: ServerConfig,
    transcriber: Transcriber,
    processor: TextProcessor,
    *,
    cuda_device_count: int | None = None,
    devices: DeviceStore | None = None,
    certificate: Certificate | None = None,
    version: str = __version__,
) -> FastAPI:
    """Build the ASGI app around a config, a ``Transcriber`` and a ``TextProcessor``.

    ``cuda_device_count`` is the GPU detector's result. Production passes the
    detected count; tests pass a fake so the API seam never probes CUDA.
    ``devices`` is the Device store; ``None`` keeps an in-memory store for
    tests and throwaway Servers. ``certificate`` supplies the fingerprint the
    Client pins; it is absent when TLS is explicitly disabled.
    """
    app = FastAPI(title="kitsune-whisper server", docs_url=None, redoc_url=None)
    app.state.config = config
    app.state.transcriber = transcriber
    app.state.processor = processor
    app.state.devices = devices if devices is not None else DeviceStore()
    app.state.certificate = certificate
    app.state.version = version
    if cuda_device_count is None:
        cuda_device_count = detect_cuda_devices()
    app.state.cuda_device_count = cuda_device_count
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

    async def authenticate(request: Request) -> TrustedDevice | None:
        """Require a valid Device token unless the operator opted out.

        The failure message never says whether a token is unknown or revoked:
        the store cannot tell them apart, and neither should the response.
        """
        if config.allow_anonymous:
            return None
        header = request.headers.get("authorization")
        unauthorized = {
            "WWW-Authenticate": "Bearer",
        }
        if not header or not header.strip():
            raise ApiError(
                401,
                "unauthorized",
                "a Device token is required; pair this Client with the Server",
                unauthorized,
            )
        parts = header.split(None, 1)
        if len(parts) != 2 or parts[0].lower() != "bearer" or not parts[1].strip():
            raise ApiError(
                401,
                "unauthorized",
                "the Authorization header must be 'Bearer <Device token>'",
                unauthorized,
            )
        address = request.client.host if request.client is not None else None
        device = app.state.devices.verify(parts[1].strip(), address)
        if device is None:
            raise ApiError(
                401,
                "unauthorized",
                "the Device token is invalid or has been revoked; pair this Client again",
                unauthorized,
            )
        return device

    @app.get("/health")
    async def health() -> dict[str, Any]:
        """Liveness and version only: reachable before pairing, no secrets."""
        return {"status": "ok", "version": app.state.version}

    @app.get("/status")
    async def status(
        device: TrustedDevice | None = Depends(authenticate),  # noqa: B008
    ) -> dict[str, Any]:
        """Authenticated status for the Tray, Settings, and diagnostics."""
        info = transcriber.info
        capability = processor.info
        gpu = gpu_status(config, cuda_device_count=app.state.cuda_device_count)
        certificate = app.state.certificate
        return {
            "status": "ok",
            "version": app.state.version,
            "model": info.model,
            "device": info.device,
            "compute_type": info.compute_type,
            "ready": bool(app.state.ready),
            "processing": {
                "enabled": capability.enabled,
                "refine": capability.refine,
                "summarize": capability.summarize,
            },
            "gpu": gpu.as_dict(),
            "tls_fingerprint": certificate.fingerprint if certificate is not None else "",
            "auth": {
                "anonymous": config.allow_anonymous,
                "device": device.name if device else None,
            },
        }

    @app.post("/pair/local", status_code=201)
    async def pair_local(
        request: Request,
        payload: LocalPairRequest | None = None,
    ) -> dict[str, Any]:
        """Issue a Device token to the Client on this machine, without a code.

        Only a genuine loopback peer may use this endpoint. A Docker port
        forward makes the peer address the bridge gateway, so Docker Servers
        pair with a code instead (ADR 0008).
        """
        peer = request.client.host if request.client is not None else None
        if not is_loopback_address(peer):
            raise ApiError(
                403,
                "forbidden",
                "local auto-pairing is available only from the loopback interface",
            )
        name = payload.name.strip() if payload is not None else "Local client"
        device, token = app.state.devices.create(name, peer)
        certificate = app.state.certificate
        return {
            "device": device.as_dict(),
            "token": token,
            "fingerprint": certificate.fingerprint if certificate is not None else "",
        }

    @app.post("/transcribe", dependencies=[Depends(authenticate)])
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
