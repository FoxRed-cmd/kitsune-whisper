"""Server configuration: schema, discovery, precedence, and validation.

The Server reads only the ``server:`` section of ``kitsune.yaml``. Precedence is
``CLI > KITSUNE_SERVER_* env (nesting via ``__``) > file > defaults``. Validation
is strict and fails fast, naming the offending field path.
"""

from __future__ import annotations

import os
import sys
from collections.abc import Mapping, MutableMapping
from pathlib import Path
from typing import Any, Literal

import yaml
from pydantic import BaseModel, ConfigDict, Field, ValidationError

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
    vad_filter: bool = False
    initial_prompt: str = ""


class ServerConfig(_StrictModel):
    host: str = Field(default="0.0.0.0", min_length=1)
    port: int = Field(default=8000, ge=1, le=65535)
    model: str = Field(default="small", min_length=1)
    device: Device = "auto"
    compute_type: ComputeType = "auto"
    workers: int = Field(default=0, ge=0)
    download_root: str = "~/.cache/kitsune-whisper/models"
    offline: bool = False
    max_audio_seconds: int = Field(default=300, ge=1)
    max_upload_mb: float = Field(default=30.0, gt=0)
    decode: DecodeConfig = DecodeConfig()
    log_level: LogLevel = "info"


def user_config_dir(env: Mapping[str, str] | None = None) -> Path:
    """Return the OS user-config directory for kitsune-whisper."""
    environ = os.environ if env is None else env
    if sys.platform == "win32":
        base = environ.get("APPDATA")
        if base:
            return Path(base) / "kitsune-whisper"
        return Path.home() / "AppData" / "Roaming" / "kitsune-whisper"
    base = environ.get("XDG_CONFIG_HOME") or str(Path.home() / ".config")
    return Path(base) / "kitsune-whisper"


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


def _read_server_section(path: Path) -> dict[str, Any]:
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


def _env_overrides(env: Mapping[str, str]) -> dict[str, Any]:
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


def _deep_merge(base: Mapping[str, Any], override: Mapping[str, Any]) -> dict[str, Any]:
    merged = dict(base)
    for key, value in override.items():
        existing = merged.get(key)
        if isinstance(existing, Mapping) and isinstance(value, Mapping):
            merged[key] = _deep_merge(existing, value)
        else:
            merged[key] = value
    return merged


def _format_validation_error(exc: ValidationError) -> str:
    parts = []
    for error in exc.errors():
        path = "server." + ".".join(str(item) for item in error["loc"])
        parts.append(f"{path}: {error['msg']}")
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
            file_values = _read_server_section(path)

    effective = _deep_merge(file_values, _env_overrides(environ))
    effective = _deep_merge(effective, dict(cli_overrides or {}))

    try:
        return ServerConfig.model_validate(effective)
    except ValidationError as exc:
        raise ConfigError(_format_validation_error(exc)) from exc


def dump_config(config: ServerConfig) -> str:
    """Render the effective config as YAML under a single ``server:`` key."""
    return yaml.safe_dump(
        {"server": config.model_dump()}, sort_keys=False, default_flow_style=False
    )
