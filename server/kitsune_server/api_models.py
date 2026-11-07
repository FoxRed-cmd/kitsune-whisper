"""The Server-owned wire contract.

Every JSON response the API returns has a model here, and every route declares
it with ``response_model``. ``mise run spec`` writes these models' OpenAPI
schema to ``server/openapi.json``; the Client's ``internal/api`` types are
generated from that file. See ``docs/adr/0014-wire-contract-owned-by-server.md``.

The models are deliberately strict: unknown keys raise, so a forgotten field in
a handler fails the contract test instead of being silently filtered by
FastAPI's ``response_model``. Genuinely open-ended payloads (the effective
configuration behind ``/admin/config``) keep ``additionalProperties: true``
through ``dict[str, Any]``.
"""

from __future__ import annotations

from typing import Annotated, Any

from pydantic import BaseModel, ConfigDict, Field

# OpenAPI has no default width for numbers. Name the widths the Client already
# uses so generated Go types are int64/float64 rather than int/float32.
Int64 = Annotated[int, Field(json_schema_extra={"format": "int64"})]
Double = Annotated[float, Field(json_schema_extra={"format": "double"})]


class ApiModel(BaseModel):
    """Base for every wire model: an undeclared payload key is an error."""

    model_config = ConfigDict(extra="forbid")


class ApiError(ApiModel):
    """The stable error code and human-readable message inside the envelope."""

    code: str
    message: str


class ErrorEnvelope(ApiModel):
    """Every non-2xx JSON response has this shape."""

    error: ApiError


class AppliedFlags(ApiModel):
    """Which processing stages actually ran on the delivered text."""

    refine: bool
    summarize: bool


class TranscribeResponse(ApiModel):
    """The result of one transcription, after any requested processing."""

    text: str
    raw_text: str
    language: str
    language_probability: Double
    duration: Double
    applied: AppliedFlags
    warnings: list[str]


class HealthResponse(ApiModel):
    """Liveness, version, and whether a model is active: reachable before pairing."""

    status: str
    version: str
    model: str
    model_state: str
    loaded: bool
    stopped: bool


class LifecycleFlags(ApiModel):
    """The backward-compatible nested model flags of ``GET /status``."""

    stopped: bool
    loaded: bool


class ServerLifecycle(ApiModel):
    """The one model payload every lifecycle endpoint and ``GET /status`` reports."""

    stopped: bool
    loaded: bool
    model: str
    model_state: str
    model_path: str
    idle_unload_seconds: int
    model_error: str
    restarting: bool


class GpuStatus(ApiModel):
    """CUDA availability as reported by ``GET /status``."""

    available: bool
    device_count: int
    device: str


class ProcessingStatus(ApiModel):
    """Which text-processing stages this Server can run."""

    enabled: bool
    refine: bool
    summarize: bool


class NetworkStatus(ApiModel):
    """The listener the Server reports: LAN exposure, bind address, and port."""

    expose_to_network: bool
    bind_host: str
    port: int


class AuthStatus(ApiModel):
    """Whether anonymous access is allowed and which Device authenticated."""

    anonymous: bool
    device: str | None


class StatusResponse(ServerLifecycle):
    """Authenticated status for the Tray, Settings, and diagnostics."""

    status: str
    version: str
    device: str
    compute_type: str
    ready: bool
    lifecycle: LifecycleFlags
    processing: ProcessingStatus
    gpu: GpuStatus | None = None
    tls_fingerprint: str
    network: NetworkStatus | None = None
    auth: AuthStatus


class Device(ApiModel):
    """A trusted Device as shown to the Server owner; the token hash stays private."""

    id: str
    name: str
    created_at: Double
    last_seen: Double | None
    last_address: str
    # Current marks the Device that made an Admin API request. It is false in
    # pairing responses, which are about the Device that was just created.
    current: bool


class DeviceListResponse(ApiModel):
    """The trusted Devices of one Server."""

    devices: list[Device]


class Pairing(ApiModel):
    """The result of a successful pair: the token, the Device, and the pin."""

    device: Device
    token: str
    fingerprint: str
    version: str


class PairingCode(ApiModel):
    """A one-time Pairing code generated for a new Device."""

    code: str
    expires_at: Double
    fingerprint: str


class ExposureResult(ApiModel):
    """One LAN exposure change, including whether a restart is required."""

    expose_to_network: bool
    bind_host: str
    changed: bool
    written: bool
    restart_required: list[str]


class ConfigField(ApiModel):
    """One Server setting: its effective value and where it came from.

    Per-field metadata is an extension point: a future Server may report an
    attribute this Client does not know yet, so extra keys survive here just
    as they do in ``config`` itself.
    """

    model_config = ConfigDict(extra="allow")

    value: Any
    source: str
    locked: bool
    reason: str
    mode: str


class ServerConfig(ApiModel):
    """The redacted effective Server configuration.

    ``config`` is deliberately open-ended: its keys come from the user's YAML,
    so it stays ``additionalProperties: true`` and is guarded by the redaction
    tests rather than by a fixed model.
    """

    path: str
    config: dict[str, Any]
    fields: dict[str, ConfigField]
    redacted: list[str]


class ServerConfigPatch(ServerConfig):
    """The result of one validated configuration patch."""

    changed: list[str]
    hot_applied: list[str]
    restart_required: list[str]
    written: bool


class ServerLog(ApiModel):
    """The tail of a Server log file."""

    path: str
    lines: list[str]
    truncated: bool
    tail: int


class ModelDownload(ApiModel):
    """One model download's progress and outcome."""

    model: str
    state: str
    received_bytes: Int64
    total_bytes: Int64
    error: str


class Model(ApiModel):
    """One entry in the Model catalog as the Server reports it."""

    name: str
    disk_bytes: Int64
    memory_bytes: Int64
    speed: str
    quality: str
    recommended: bool
    installed: bool
    installed_bytes: Int64
    download: ModelDownload | None


class ModelsView(ApiModel):
    """The Server's Model catalog and lifecycle state."""

    device: str
    recommended: str
    state: str
    active_model: str
    active_path: str
    error: str
    idle_unload_seconds: int
    models: list[Model]
    # Warning reports a change that applied but could not be persisted.
    warning: str


__all__ = [
    "ApiError",
    "ApiModel",
    "AppliedFlags",
    "AuthStatus",
    "ConfigField",
    "Device",
    "DeviceListResponse",
    "Double",
    "ErrorEnvelope",
    "ExposureResult",
    "GpuStatus",
    "HealthResponse",
    "Int64",
    "LifecycleFlags",
    "Model",
    "ModelDownload",
    "ModelsView",
    "NetworkStatus",
    "Pairing",
    "PairingCode",
    "ProcessingStatus",
    "ServerConfig",
    "ServerConfigPatch",
    "ServerLifecycle",
    "ServerLog",
    "StatusResponse",
    "TranscribeResponse",
]
