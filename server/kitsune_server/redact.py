"""Redaction for the Admin API's configuration read and patch.

The Client may read a Server's effective settings, but a secret never crosses
the network. ``server.processing.api_key`` is the only secret in the Server
YAML today; Device token hashes live in the Device store and are not part of
the configuration at all.

A read replaces every secret with :data:`REDACTED`. A patch that echoes that
sentinel back (because the Settings window saved a form it had loaded) is
dropped instead of writing the literal marker over the real value.
"""

from __future__ import annotations

from collections.abc import Mapping
from typing import Any

from .config import ServerConfig

REDACTED = "***"

# Absolute ``server.*`` paths whose values never leave the Server.
SECRET_PATHS = frozenset({"server.processing.api_key"})


def is_secret(path: str) -> bool:
    """Whether an absolute field path holds a secret."""
    return path in SECRET_PATHS


def redact_value(path: str, value: Any) -> Any:
    """Return the placeholder for a secret that has a value, else the value."""
    if is_secret(path) and value is not None:
        return REDACTED
    return value


def redact_config(config: ServerConfig) -> tuple[dict[str, Any], list[str]]:
    """Render the effective config with secrets masked, and list the paths."""
    document = config.model_dump()
    redacted: list[str] = []
    for path in sorted(SECRET_PATHS):
        value = _lookup(document, path)
        if value is not _MISSING and value is not None:
            _set(document, path, REDACTED)
            redacted.append(path)
    return document, redacted


def strip_redacted(changes: Mapping[str, Any]) -> dict[str, Any]:
    """Drop secret leaves whose value is the read-time placeholder.

    ``changes`` nests like the schema under ``server:``; the returned patch is
    the same shape with those leaves removed. An empty mapping is a no-op.
    """
    return _strip(changes, "server.")


class _Missing:
    pass


_MISSING = _Missing()


def _lookup(document: Mapping[str, Any], path: str) -> Any:
    cursor: Any = document
    for part in path.removeprefix("server.").split("."):
        if not isinstance(cursor, Mapping) or part not in cursor:
            return _MISSING
        cursor = cursor[part]
    return cursor


def _set(document: dict[str, Any], path: str, value: Any) -> None:
    parts = path.removeprefix("server.").split(".")
    cursor = document
    for part in parts[:-1]:
        child = cursor.get(part)
        if not isinstance(child, dict):
            child = {}
            cursor[part] = child
        cursor = child
    cursor[parts[-1]] = value


def _strip(changes: Mapping[str, Any], prefix: str) -> dict[str, Any]:
    stripped: dict[str, Any] = {}
    for name, value in changes.items():
        path = f"{prefix}{name}"
        if isinstance(value, Mapping):
            nested = _strip(value, f"{path}.")
            if nested:
                stripped[name] = nested
            continue
        if is_secret(path) and value == REDACTED:
            continue
        stripped[name] = value
    return stripped


__all__ = [
    "REDACTED",
    "SECRET_PATHS",
    "is_secret",
    "redact_config",
    "redact_value",
    "strip_redacted",
]
