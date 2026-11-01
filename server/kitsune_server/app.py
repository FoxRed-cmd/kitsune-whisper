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
from pathlib import Path
from typing import Annotated, Any

import anyio.to_thread
from fastapi import BackgroundTasks, Depends, FastAPI, File, Form, Query, Request, UploadFile
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from pydantic import BaseModel, Field
from starlette.concurrency import run_in_threadpool
from starlette.exceptions import HTTPException as StarletteHTTPException

from . import __version__
from .audio import AudioDecodeError, decode_audio
from .config import ConfigError, ServerConfig, format_errors
from .gpu import detect_cuda_devices, gpu_status
from .languages import normalize_language
from .lifecycle import Lifecycle, LifecycleError
from .pairing import (
    ExpiredPairingCodeError,
    PairingCodeStore,
    PairingRateLimitedError,
    ReusedPairingCodeError,
    WrongPairingCodeError,
)
from .processor import ProcessingError, TextProcessor
from .redact import redact_config, redact_value, strip_redacted
from .store import ConfigStore
from .transcriber import Transcriber, resolve_workers
from .trust import Certificate, DeviceStore
from .trust import Device as TrustedDevice

logger = logging.getLogger("kitsune.server")

# Fields this Server can apply to the running process without a restart. The
# store classifies more fields as hot for later tickets (#76 activates models
# online); #77 only claims what it actually applies: values read from
# ``app.state.config`` on the next request and the log level. Everything else
# is reported as restart-required so the UI never promises a live change that
# did not happen.
_APPLIED_HOT_FIELDS = frozenset(
    {
        "server.log_level",
        "server.max_audio_seconds",
        "server.max_upload_mb",
        "server.decode.language",
        "server.decode.initial_prompt",
    }
)

MAX_LOG_TAIL = 1000


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


class RemotePairRequest(BaseModel):
    """The body of the one-time Pairing code exchange."""

    code: str = Field(min_length=1, max_length=64)
    name: str = Field(default="Device", min_length=1, max_length=128)


class ExposureRequest(BaseModel):
    """The body of the LAN exposure switch."""

    expose: bool
    confirm: bool = False


class ConfigPatchRequest(BaseModel):
    """A partial ``server:``-shaped patch validated like startup."""

    patch: dict[str, Any] = Field(default_factory=dict)


def _config_payload(store: ConfigStore) -> dict[str, Any]:
    """The editable configuration with secrets masked and per-field metadata."""
    config = store.effective()
    document, redacted = redact_config(config)
    fields: dict[str, Any] = {}
    for path, state in store.states().items():
        fields[path] = {
            "value": redact_value(path, state.value),
            "source": state.source,
            "locked": state.locked,
            "reason": state.reason,
            "mode": state.mode,
        }
    return {
        "path": str(store.target_path),
        "config": document,
        "redacted": redacted,
        "fields": fields,
    }


def _tail_lines(path: Path, limit: int) -> tuple[list[str], bool]:
    """Read the last ``limit`` lines, reporting whether older lines exist."""
    with path.open("r", encoding="utf-8", errors="replace") as handle:
        lines = [line.rstrip("\r\n") for line in handle]
    truncated = len(lines) > limit
    return lines[-limit:], truncated


def _model_state(lifecycle: Lifecycle) -> str:
    if lifecycle.stopped:
        return "stopped"
    if lifecycle.loaded:
        return "loaded"
    return "cold"


def _lifecycle_payload(lifecycle: Lifecycle) -> dict[str, Any]:
    return {
        "stopped": lifecycle.stopped,
        "loaded": lifecycle.loaded,
        "model": lifecycle.info.model,
        "model_state": _model_state(lifecycle),
    }


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
    pairing_codes: PairingCodeStore | None = None,
    config_store: ConfigStore | None = None,
    lifecycle: Lifecycle | None = None,
    log_file: Path | None = None,
    restart: Callable[[], None] | None = None,
    version: str = __version__,
) -> FastAPI:
    """Build the ASGI app around a config, a ``Transcriber`` and a ``TextProcessor``.

    ``cuda_device_count`` is the GPU detector's result. Production passes the
    detected count; tests pass a fake so the API seam never probes CUDA.
    ``devices`` is the Device store; ``None`` keeps an in-memory store for
    tests and throwaway Servers. ``certificate`` supplies the fingerprint the
    Client pins; it is absent when TLS is explicitly disabled.
    ``pairing_codes`` holds the one-time Pairing codes; ``config_store`` is the
    only writer of the Server YAML and backs the LAN exposure switch.
    ``lifecycle`` owns the loaded model and the persisted soft-stop; ``None``
    wraps ``transcriber`` in memory so existing tests and throwaway Servers
    keep working. ``log_file`` backs ``GET /admin/logs`` and ``restart`` is the
    supervised self-exit action (tests inject a recorder, production exits).
    """
    app = FastAPI(title="kitsune-whisper server", docs_url=None, redoc_url=None)
    app.state.config = config
    app.state.transcriber = transcriber
    app.state.processor = processor
    app.state.devices = devices if devices is not None else DeviceStore()
    app.state.pairing_codes = pairing_codes if pairing_codes is not None else PairingCodeStore()
    app.state.config_store = config_store
    app.state.certificate = certificate
    app.state.version = version
    app.state.lifecycle = (
        lifecycle if lifecycle is not None else Lifecycle.from_transcriber(transcriber)
    )
    app.state.log_file = Path(log_file).expanduser() if log_file is not None else None
    app.state.restart = restart
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
        timeout = app.state.config.processing.stage_timeout_seconds
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
        if app.state.config.allow_anonymous:
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
        active = app.state.config
        lifecycle = app.state.lifecycle
        info = lifecycle.info
        capability = processor.info
        gpu = gpu_status(active, cuda_device_count=app.state.cuda_device_count)
        certificate = app.state.certificate
        return {
            "status": "ok",
            "version": app.state.version,
            "model": info.model,
            "device": info.device,
            "compute_type": info.compute_type,
            "ready": bool(app.state.ready) and lifecycle.loaded and not lifecycle.stopped,
            "model_state": _model_state(lifecycle),
            "lifecycle": {"stopped": lifecycle.stopped, "loaded": lifecycle.loaded},
            "processing": {
                "enabled": capability.enabled,
                "refine": capability.refine,
                "summarize": capability.summarize,
            },
            "gpu": gpu.as_dict(),
            "tls_fingerprint": certificate.fingerprint if certificate is not None else "",
            "network": {
                "expose_to_network": active.expose_to_network,
                "bind_host": active.bind_host,
                "port": active.port,
            },
            "auth": {
                "anonymous": active.allow_anonymous,
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

    @app.post("/pair", status_code=201)
    async def pair_remote(request: Request, payload: RemotePairRequest) -> dict[str, Any]:
        """Redeem a one-time Pairing code for a Remote Device token.

        The failures stay distinct so the Client can say whether the code was
        wrong, expired, already used, or the peer is asking too often.
        """
        peer = request.client.host if request.client is not None else None
        name = payload.name.strip() or "Device"
        try:
            app.state.pairing_codes.redeem(payload.code, peer)
        except PairingRateLimitedError as exc:
            raise ApiError(
                429,
                "pairing_rate_limited",
                str(exc),
                {"Retry-After": str(exc.retry_after)},
            ) from exc
        except ExpiredPairingCodeError as exc:
            raise ApiError(410, "pairing_code_expired", str(exc)) from exc
        except ReusedPairingCodeError as exc:
            raise ApiError(409, "pairing_code_reused", str(exc)) from exc
        except WrongPairingCodeError as exc:
            raise ApiError(401, "pairing_code_wrong", str(exc)) from exc
        device, token = app.state.devices.create(name, peer)
        certificate = app.state.certificate
        return {
            "device": device.as_dict(),
            "token": token,
            "fingerprint": certificate.fingerprint if certificate is not None else "",
        }

    @app.post("/admin/pairing-code", status_code=201)
    async def create_pairing_code(
        device: TrustedDevice | None = Depends(authenticate),  # noqa: B008
    ) -> dict[str, Any]:
        """Generate one Pairing code for a new Device.

        Any trusted Device may ask, because every Device is already fully
        trusted in v1 (ADR 0008). The local Settings window is the normal path.
        """
        del device  # authentication is the only requirement
        code = app.state.pairing_codes.generate()
        certificate = app.state.certificate
        return {
            "code": code.code,
            "expires_at": code.expires_at,
            "fingerprint": certificate.fingerprint if certificate is not None else "",
        }

    @app.get("/admin/devices")
    async def list_devices(
        device: TrustedDevice | None = Depends(authenticate),  # noqa: B008
    ) -> dict[str, Any]:
        """List trusted Devices with names, timestamps, and last addresses."""
        del device
        return {"devices": [item.as_dict() for item in app.state.devices.list()]}

    @app.delete("/admin/devices/{device_id}", status_code=204)
    async def revoke_device(
        device_id: str,
        device: TrustedDevice | None = Depends(authenticate),  # noqa: B008
    ) -> None:
        """Revoke one Device; its next request is unauthorised."""
        del device
        if not app.state.devices.revoke(device_id):
            raise ApiError(
                404,
                "not_found",
                f"no Device has id {device_id!r}; list Devices and try again",
            )

    @app.post("/admin/exposure")
    async def set_exposure(
        payload: ExposureRequest,
        device: TrustedDevice | None = Depends(authenticate),  # noqa: B008
    ) -> dict[str, Any]:
        """Turn LAN exposure on or off in the Server YAML.

        A change never rebinds the running listener: the response reports that
        a restart is required, so the Server owner sees the effect only after a
        deliberate restart (ADR 0008 and the desktop spec).
        """
        del device
        if payload.expose and not payload.confirm:
            raise ApiError(
                400,
                "confirmation_required",
                "exposing the Server to the LAN requires an explicit confirmation; "
                "send confirm: true after warning the user",
            )
        store = app.state.config_store
        if store is None:
            raise ApiError(
                503,
                "config_unavailable",
                "this Server cannot write its configuration; change "
                "server.expose_to_network in kitsune.yaml and restart the Server",
            )
        try:
            result = store.patch({"expose_to_network": payload.expose})
        except ConfigError as exc:
            raise ApiError(400, "bad_request", str(exc)) from exc
        return {
            "expose_to_network": result.config.expose_to_network,
            "bind_host": result.config.bind_host,
            "changed": bool(result.changed),
            "written": result.written,
            "restart_required": list(result.restart_required),
        }

    @app.get("/admin/config")
    async def read_config(
        device: TrustedDevice | None = Depends(authenticate),  # noqa: B008
    ) -> dict[str, Any]:
        """Read the effective Server configuration with secrets masked."""
        del device
        store = app.state.config_store
        if store is None:
            raise ApiError(
                503,
                "config_unavailable",
                "this Server cannot read its configuration; edit kitsune.yaml on the "
                "Server host instead",
            )
        try:
            payload = _config_payload(store)
        except ConfigError as exc:
            raise ApiError(409, "config_invalid", str(exc)) from exc
        return payload

    @app.patch("/admin/config")
    async def patch_config(
        payload: ConfigPatchRequest,
        device: TrustedDevice | None = Depends(authenticate),  # noqa: B008
    ) -> dict[str, Any]:
        """Validate and atomically write a partial ``server:`` patch.

        Validation is exactly startup's; nothing is written on failure. A
        secret echoed back as the redaction placeholder is dropped, never
        written over the real value. Only hot fields this Server actually
        applies are reported as ``hot_applied``; everything else needs a
        restart.
        """
        del device
        store = app.state.config_store
        if store is None:
            raise ApiError(
                503,
                "config_unavailable",
                "this Server cannot write its configuration; edit kitsune.yaml on the "
                "Server host instead",
            )
        changes = strip_redacted(payload.patch)
        if not changes:
            try:
                payload_out = _config_payload(store)
            except ConfigError as exc:
                raise ApiError(409, "config_invalid", str(exc)) from exc
            return {
                **payload_out,
                "changed": [],
                "hot_applied": [],
                "restart_required": [],
                "written": False,
            }
        try:
            result = store.patch(changes)
        except ConfigError as exc:
            raise ApiError(400, "bad_request", str(exc)) from exc
        app.state.config = result.config
        if "server.log_level" in result.changed:
            logging.getLogger("kitsune").setLevel(result.config.log_level.upper())
        hot = [path for path in result.changed if path in _APPLIED_HOT_FIELDS]
        restart = [path for path in result.changed if path not in _APPLIED_HOT_FIELDS]
        payload_out = _config_payload(store)
        return {
            **payload_out,
            "changed": list(result.changed),
            "hot_applied": hot,
            "restart_required": restart,
            "written": result.written,
        }

    @app.get("/admin/logs")
    async def read_logs(
        tail: Annotated[int, Query(ge=1, le=MAX_LOG_TAIL)] = 200,
        device: TrustedDevice | None = Depends(authenticate),  # noqa: B008
    ) -> dict[str, Any]:
        """Return the last ``tail`` lines of the Server log file."""
        del device
        log_file = app.state.log_file
        if log_file is None:
            raise ApiError(
                503,
                "log_unavailable",
                "this Server is not writing a log file; run it as a supervised "
                "background process or set --log-file",
            )
        try:
            lines, truncated = _tail_lines(log_file, tail)
        except OSError as exc:
            raise ApiError(
                503,
                "log_unavailable",
                f"the Server log {log_file} cannot be read: {exc}",
            ) from exc
        return {
            "path": str(log_file),
            "lines": lines,
            "truncated": truncated,
            "tail": tail,
        }

    @app.post("/admin/restart", status_code=202)
    async def restart_server(
        background: BackgroundTasks,
        device: TrustedDevice | None = Depends(authenticate),  # noqa: B008
    ) -> dict[str, Any]:
        """Answer ``202``, then exit so the supervisor starts the Server again."""
        del device
        action = app.state.restart
        if action is None:
            raise ApiError(
                503,
                "restart_unavailable",
                "this Server cannot restart itself; stop and start it on the Server host",
            )
        background.add_task(action)
        return {"restarting": True}

    @app.post("/admin/stop")
    async def stop_server(
        device: TrustedDevice | None = Depends(authenticate),  # noqa: B008
    ) -> dict[str, Any]:
        """Soft-stop: unload the model, persist the state, keep the API alive."""
        del device
        lifecycle = app.state.lifecycle
        lifecycle.stop()
        logger.warning(
            "Server soft-stopped: the model is unloaded and /transcribe returns 503 "
            "until POST /admin/start"
        )
        return _lifecycle_payload(lifecycle)

    @app.post("/admin/start")
    async def start_server(
        device: TrustedDevice | None = Depends(authenticate),  # noqa: B008
    ) -> dict[str, Any]:
        """Clear a soft-stop and load the model again."""
        del device
        lifecycle = app.state.lifecycle
        try:
            lifecycle.start()
        except LifecycleError as exc:
            raise ApiError(503, "engine_load_failed", str(exc)) from exc
        logger.info("Server started: model %s is loaded", lifecycle.info.model)
        return _lifecycle_payload(lifecycle)

    @app.post("/transcribe", dependencies=[Depends(authenticate)])
    async def transcribe(
        audio: Annotated[UploadFile, File()],
        language: Annotated[str | None, Form()] = None,
        initial_prompt: Annotated[str | None, Form()] = None,
        refine: Annotated[bool, Form()] = False,
        summarize: Annotated[bool, Form()] = False,
    ) -> dict[str, Any]:
        lifecycle = app.state.lifecycle
        try:
            active_transcriber = lifecycle.transcriber
        except LifecycleError as exc:
            code = "stopped" if lifecycle.stopped else "model_not_loaded"
            raise ApiError(503, code, str(exc)) from exc
        active = app.state.config
        if not app.state.semaphore.acquire(blocking=False):
            raise ApiError(503, "unavailable", "server is busy", {"Retry-After": "1"})
        try:
            max_bytes = int(active.max_upload_mb * 1024 * 1024)
            declared_size = getattr(audio, "size", None)
            if declared_size is not None and declared_size > max_bytes:
                raise ApiError(
                    413,
                    "payload_too_large",
                    f"upload exceeds {active.max_upload_mb:g} MB",
                )
            data = await audio.read()
            if len(data) > max_bytes:
                raise ApiError(
                    413,
                    "payload_too_large",
                    f"upload exceeds {active.max_upload_mb:g} MB",
                )
            try:
                clip = await run_in_threadpool(decode_audio, data)
            except AudioDecodeError as exc:
                raise ApiError(415, "unsupported_media", f"could not decode audio: {exc}") from exc
            if clip.duration > active.max_audio_seconds:
                raise ApiError(
                    413,
                    "payload_too_large",
                    f"audio exceeds {active.max_audio_seconds} s",
                )
            result = await run_in_threadpool(
                active_transcriber.transcribe,
                clip,
                language=_effective_language(language, active),
                initial_prompt=_effective_prompt(initial_prompt, active),
            )
        finally:
            app.state.semaphore.release()

        raw_text = result.text
        delivered = raw_text
        applied = {"refine": False, "summarize": False}
        warnings: list[str] = []
        prompt_language = result.language if active.decode.task == "transcribe" else "en"

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
