"""Server configuration: schema, discovery, precedence, and validation.

The Server reads only the ``server:`` section of ``kitsune.yaml``. Precedence is
``CLI > KITSUNE_SERVER_* env (nesting via ``__``) > file > defaults``. Validation
is strict and fails fast, naming the offending field path.
"""

from __future__ import annotations

import ipaddress
import logging
import os
import sys
from collections.abc import Mapping, MutableMapping, Sequence
from pathlib import Path
from typing import Any, Literal

import yaml
from pydantic import (
    BaseModel,
    ConfigDict,
    Field,
    ValidationError,
    model_validator,
)

logger = logging.getLogger(__name__)

ENV_PREFIX = "KITSUNE_SERVER_"
CONFIG_ENV = "KITSUNE_CONFIG"
CONFIG_FILENAME = "kitsune.yaml"

Device = Literal["auto", "cpu", "cuda"]
ComputeType = Literal[
    "auto",
    "int8",
    "int8_float16",
    "int8_float32",
    "int16",
    "float16",
    "bfloat16",
    "float32",
]
LogLevel = Literal["debug", "info", "warning", "error"]


class ConfigError(Exception):
    """Raised when configuration is missing, unreadable, or invalid."""


class _StrictModel(BaseModel):
    model_config = ConfigDict(extra="forbid")


class DecodeConfig(_StrictModel):
    task: Literal["transcribe", "translate"] = "transcribe"
    language: str = "auto"
    beam_size: int = Field(default=5, ge=1)
    temperature: float = Field(default=0.0, ge=0.0)
    vad_filter: bool = True
    initial_prompt: str = ""


class ProcessingConfig(_StrictModel):
    """Optional Refine/Summarize post-processing over an OpenAI-compatible endpoint.

    The Server is a thin HTTP client: it posts chat completions to
    ``{base_url}/v1/chat/completions``. ``base_url`` defaults to a loopback
    address so processing stays local unless an operator opts into an external
    provider; ``api_key`` is sent as ``Authorization: Bearer``.
    """

    enabled: bool = False
    base_url: str = Field(default="http://127.0.0.1:8080", min_length=1)
    api_key: str | None = None
    model: str = Field(default="Qwen3-0.6B-Q8_0.gguf", min_length=1)
    extra_body: dict[str, Any] = Field(default_factory=dict)
    max_output_tokens: int = Field(default=1024, ge=1)
    stage_timeout_seconds: float = Field(default=30.0, gt=0)


def is_loopback_host(host: str) -> bool:
    """Return True when ``host`` names the loopback interface only."""
    if host == "localhost":
        return True
    try:
        return ipaddress.ip_address(host).is_loopback
    except ValueError:
        return False


class TlsConfig(_StrictModel):
    """TLS settings for the HTTPS listener.

    TLS is on by default. A self-signed certificate is generated at first start
    and persisted in the Server state directory; ``cert_file``/``key_file`` are
    optional overrides and must be set together. The Client pins the reported
    fingerprint. Setting ``enabled: false`` is the explicit insecure-HTTP
    opt-out for advanced setups.
    """

    enabled: bool = True
    cert_file: str | None = None
    key_file: str | None = None

    @model_validator(mode="after")
    def _cert_and_key_travel_together(self) -> TlsConfig:
        if bool(self.cert_file) != bool(self.key_file):
            raise ValueError("cert_file and key_file must be set together or both omitted")
        return self


class ServerConfig(_StrictModel):
    """Effective Server settings, validated with the same rules as startup."""

    # Network exposure first so ``host`` can validate against it.
    expose_to_network: bool = False
    host: str = Field(default="127.0.0.1", min_length=1)
    port: int = Field(default=8765, ge=1, le=65535)
    # ``model`` is the active model: a faster-whisper size name or a local
    # CTranslate2 directory. It stays the single source of truth for the model
    # the Server loads (the Model catalog metadata lives in ``catalog_file``).
    model: str = Field(default="small", min_length=1)
    device: Device = "auto"
    compute_type: ComputeType = "auto"
    workers: int = Field(default=0, ge=0)
    download_root: str = "~/.cache/kitsune-whisper/models"
    # Catalog metadata is pinned with the release by default; point this at a
    # file to override it.
    catalog_file: str = ""
    offline: bool = False
    # "" = the OS data directory resolved by the Device store (later ticket).
    devices_file: str = ""
    max_audio_seconds: int = Field(default=300, ge=1)
    max_upload_mb: float = Field(default=30.0, gt=0)
    # 0 disables idle unloading: the model stays resident until restarted.
    idle_unload_seconds: int = Field(default=600, ge=0)
    tls: TlsConfig = TlsConfig()
    # Device tokens are required unless an operator opts into anonymous access.
    allow_anonymous: bool = False
    decode: DecodeConfig = DecodeConfig()
    processing: ProcessingConfig = ProcessingConfig()
    log_level: LogLevel = "info"

    @model_validator(mode="after")
    def _legacy_host_is_explicit_exposure(self) -> ServerConfig:
        """Accept a configured non-loopback ``host`` as legacy LAN exposure.

        ``host: 0.0.0.0`` was the documented default before
        ``expose_to_network`` existed, so existing files must keep working.
        Warn once per validation so operators can migrate.
        """
        if (
            "host" in self.model_fields_set
            and not self.expose_to_network
            and not is_loopback_host(self.host)
        ):
            logger.warning(
                "server.host is set to %s without server.expose_to_network; "
                "binding it as given. Prefer expose_to_network: true.",
                self.host,
            )
        return self

    @property
    def bind_host(self) -> str:
        """Address the HTTP listener binds to.

        ``expose_to_network`` decides the LAN switch: on binds all interfaces,
        off keeps the configured address (loopback by default). A non-loopback
        ``host`` without the switch still binds as given, which keeps the legacy
        ``host: 0.0.0.0`` files working and logs a migration warning.
        """
        if self.expose_to_network:
            return "0.0.0.0"
        return self.host


def platform_name(platform: str | None = None) -> str:
    """Map ``sys.platform`` onto the names the bundle and supervisor use."""
    raw = sys.platform if platform is None else platform
    if raw == "win32":
        return "windows"
    if raw.startswith("linux"):
        return "linux"
    return raw


def config_home(env: Mapping[str, str] | None = None, *, os_name: str | None = None) -> Path:
    """Return the OS base directory for per-user configuration."""
    environ = os.environ if env is None else env
    home = Path(environ.get("HOME") or Path.home())
    if (os_name or platform_name()) == "windows":
        base = environ.get("APPDATA")
        return Path(base) if base else home / "AppData" / "Roaming"
    base = environ.get("XDG_CONFIG_HOME")
    return Path(base) if base else home / ".config"


def user_config_dir(env: Mapping[str, str] | None = None, *, os_name: str | None = None) -> Path:
    """Return the OS user-config directory for kitsune-whisper."""
    return config_home(env, os_name=os_name) / "kitsune-whisper"


def user_state_dir(env: Mapping[str, str] | None = None) -> Path:
    """Return the per-user state directory, where logs survive upgrades."""
    environ = os.environ if env is None else env
    home = Path(environ.get("HOME") or Path.home())
    if platform_name() == "windows":
        base = environ.get("LOCALAPPDATA")
        if base:
            return Path(base) / "kitsune-whisper"
        return home / "AppData" / "Local" / "kitsune-whisper"
    base = environ.get("XDG_STATE_HOME")
    if base:
        return Path(base) / "kitsune-whisper"
    return home / ".local" / "state" / "kitsune-whisper"


def find_config_file(
    cli_path: str | None,
    *,
    env: Mapping[str, str] | None = None,
    cwd: Path | None = None,
) -> tuple[Path | None, bool]:
    """Locate ``kitsune.yaml``.

    Returns ``(path, required)``. ``required`` is True for an explicit path
    (``--config`` or ``$KITSUNE_CONFIG``), which must exist.
    """
    environ = os.environ if env is None else env
    if cli_path:
        return Path(cli_path).expanduser(), True
    env_path = environ.get(CONFIG_ENV)
    if env_path:
        return Path(env_path).expanduser(), True
    local = (cwd or Path.cwd()) / CONFIG_FILENAME
    if local.is_file():
        return local, False
    user = user_config_dir(environ) / CONFIG_FILENAME
    if user.is_file():
        return user, False
    return None, False


def resolve_config_file(
    cli_path: str | None,
    *,
    env: Mapping[str, str] | None = None,
    cwd: Path | None = None,
) -> Path | None:
    """The absolute file startup reads, or None when no file is involved.

    Discovery is exactly :func:`find_config_file`'s, so an installer can pin the
    same file the foreground process would read. A supervisor starts outside the
    shell's working directory, so a relative path is always made absolute.
    """
    path, _ = find_config_file(cli_path, env=env, cwd=cwd)
    return path.resolve() if path is not None else None


def read_server_section(path: Path) -> dict[str, Any]:
    try:
        raw = path.read_text(encoding="utf-8")
    except OSError as exc:
        raise ConfigError(f"cannot read config file {path}: {exc}") from exc
    try:
        document = yaml.safe_load(raw)
    except yaml.YAMLError as exc:
        raise ConfigError(f"invalid YAML in {path}: {exc}") from exc
    if document is None:
        return {}
    if not isinstance(document, dict):
        raise ConfigError(f"{path}: top-level document must be a mapping")
    section = document.get("server", {})
    if section is None:
        return {}
    if not isinstance(section, dict):
        raise ConfigError(f"{path}: 'server' must be a mapping")
    return section


def env_overrides(env: Mapping[str, str]) -> dict[str, Any]:
    overrides: dict[str, Any] = {}
    for key, value in env.items():
        if not key.startswith(ENV_PREFIX) or key == ENV_PREFIX:
            continue
        path = key[len(ENV_PREFIX) :].lower().split("__")
        if any(not part for part in path):
            continue
        cursor: MutableMapping[str, Any] = overrides
        for part in path[:-1]:
            child = cursor.get(part)
            if not isinstance(child, MutableMapping):
                child = {}
                cursor[part] = child
            cursor = child
        cursor[path[-1]] = value
    return overrides


def deep_merge(base: Mapping[str, Any], override: Mapping[str, Any]) -> dict[str, Any]:
    merged = dict(base)
    for key, value in override.items():
        existing = merged.get(key)
        if isinstance(existing, Mapping) and isinstance(value, Mapping):
            merged[key] = deep_merge(existing, value)
        else:
            merged[key] = value
    return merged


def format_errors(
    errors: Sequence[Mapping[str, Any]],
    *,
    prefix: str = "",
    drop: int = 0,
) -> str:
    """Render pydantic-style errors as ``path: message`` joined by ``; ``."""
    parts = []
    for error in errors:
        path = ".".join(str(item) for item in error["loc"][drop:]) or "body"
        parts.append(f"{prefix}{path}: {error['msg']}")
    return "; ".join(parts)


def load_config(
    *,
    cli_config: str | None = None,
    cli_overrides: Mapping[str, Any] | None = None,
    env: Mapping[str, str] | None = None,
    cwd: Path | None = None,
) -> ServerConfig:
    """Resolve and validate the effective Server configuration."""
    environ = os.environ if env is None else env
    path, required = find_config_file(cli_config, env=environ, cwd=cwd)
    file_values: dict[str, Any] = {}
    if path is not None:
        if not path.is_file():
            if required:
                raise ConfigError(f"config file not found: {path}")
        else:
            file_values = read_server_section(path)

    effective = deep_merge(file_values, env_overrides(environ))
    effective = deep_merge(effective, dict(cli_overrides or {}))

    try:
        return ServerConfig.model_validate(effective)
    except ValidationError as exc:
        raise ConfigError(format_errors(exc.errors(), prefix="server.")) from exc


def dump_config(config: ServerConfig) -> str:
    """Render the effective config as YAML under a single ``server:`` key."""
    return yaml.safe_dump(
        {"server": config.model_dump()}, sort_keys=False, default_flow_style=False
    )
