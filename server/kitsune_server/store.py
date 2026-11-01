"""The only writer of the ``server:`` section of ``kitsune.yaml``.

The store is the seam the Admin API will drive: it reports the effective
:class:`~kitsune_server.config.ServerConfig`, the source of every field
(``default``/``file``/``env``/``cli``), whether a change applies hot or needs a
restart, and patches the file atomically while keeping comments and the
``client:`` section intact.

It validates with exactly the same rules as startup and ``--check-config``;
nothing is written when validation fails.
"""

from __future__ import annotations

import io
import os
import shutil
import tempfile
from collections.abc import Mapping
from dataclasses import dataclass
from pathlib import Path
from typing import Any, Literal

import yaml
from pydantic import BaseModel, ValidationError
from ruamel.yaml import YAML
from ruamel.yaml.comments import CommentedMap
from ruamel.yaml.error import YAMLError as RuamelYAMLError

from .config import (
    CONFIG_FILENAME,
    ENV_PREFIX,
    ConfigError,
    ServerConfig,
    deep_merge,
    env_overrides,
    find_config_file,
    format_errors,
    read_server_section,
    user_config_dir,
)

Source = Literal["default", "file", "env", "cli"]
ChangeImpact = Literal["hot", "restart"]

# Fields the running Server can apply without a restart. Everything else needs
# one: the listener (host/port/exposure), the engine (device/compute_type,
# workers, download_root, offline), TLS/auth, store locations, and the bundled
# catalog.
_HOT_PREFIXES = frozenset(
    {
        "model",
        "idle_unload_seconds",
        "max_audio_seconds",
        "max_upload_mb",
        "decode",
        "processing",
        "log_level",
    }
)


@dataclass(frozen=True)
class FieldState:
    """One leaf setting with where its effective value came from."""

    value: Any
    source: Source
    locked: bool = False
    reason: str | None = None


@dataclass(frozen=True)
class SaveResult:
    """Outcome of a :meth:`ConfigStore.patch` call."""

    config: ServerConfig
    changed: tuple[str, ...] = ()
    hot_applied: tuple[str, ...] = ()
    restart_required: tuple[str, ...] = ()
    written: bool = False
    backup_path: Path | None = None


@dataclass(frozen=True)
class _Document:
    text: str
    root: CommentedMap
    representable: bool


def _leaf_items(value: BaseModel, prefix: str = "") -> list[tuple[str, Any]]:
    """Flatten a pydantic model into ``dotted.path`` leaf values."""
    items: list[tuple[str, Any]] = []
    for name in type(value).model_fields:
        child = getattr(value, name)
        path = f"{prefix}{name}"
        if isinstance(child, BaseModel):
            items.extend(_leaf_items(child, f"{path}."))
        else:
            items.append((path, child))
    return items


def _lookup(values: Mapping[str, Any], path: str) -> tuple[bool, Any]:
    cursor: Any = values
    for part in path.split("."):
        if not isinstance(cursor, Mapping) or part not in cursor:
            return False, None
        cursor = cursor[part]
    return True, cursor


def _nested_model(annotation: Any) -> type[BaseModel] | None:
    if isinstance(annotation, type) and issubclass(annotation, BaseModel):
        return annotation
    return None


def _flatten_patch(
    changes: Mapping[str, Any],
    model: type[BaseModel] = ServerConfig,
    prefix: str = "",
) -> list[tuple[str, Any]]:
    """Expand nested patch values into ``dotted.path`` leaves."""
    leaves: list[tuple[str, Any]] = []
    for name, value in changes.items():
        field_info = model.model_fields.get(name)
        if field_info is None:
            # Unknown keys are rejected by validation before this runs.
            continue
        path = f"{prefix}{name}"
        nested = _nested_model(field_info.annotation)
        if nested is not None and isinstance(value, Mapping):
            leaves.extend(_flatten_patch(value, nested, f"{path}."))
        else:
            leaves.append((path, value))
    return leaves


def _yaml_value(value: Any) -> Any:
    if isinstance(value, Mapping):
        result = CommentedMap()
        for key, child in value.items():
            result[key] = _yaml_value(child)
        return result
    if isinstance(value, list):
        return [_yaml_value(item) for item in value]
    return value


def _set_path(root: CommentedMap, path: str, value: Any) -> None:
    """Set one dotted path, creating missing mappings without touching others."""
    parts = path.split(".")
    cursor: CommentedMap = root
    for part in parts[:-1]:
        child = cursor.get(part)
        if not isinstance(child, CommentedMap):
            child = CommentedMap()
            cursor[part] = child
        cursor = child
    cursor[parts[-1]] = _yaml_value(value)


def _dump(root: CommentedMap) -> str:
    writer = YAML(typ="rt")
    writer.preserve_quotes = True
    writer.indent(mapping=2, sequence=4, offset=2)
    writer.width = 4096
    stream = io.StringIO()
    writer.dump(root, stream)
    return stream.getvalue()


def _atomic_write(path: Path, text: str) -> None:
    """Write ``text`` via a temp file in the same directory, then ``os.replace``."""
    path.parent.mkdir(parents=True, exist_ok=True)
    try:
        mode = path.stat().st_mode
    except OSError:
        mode = None
    descriptor, temp_name = tempfile.mkstemp(
        dir=path.parent, prefix=f".{path.name}.", suffix=".tmp"
    )
    temp_path = Path(temp_name)
    try:
        with os.fdopen(descriptor, "w", encoding="utf-8", newline="\n") as handle:
            handle.write(text)
            handle.flush()
            os.fsync(handle.fileno())
        if mode is not None:
            os.chmod(temp_path, mode)
        os.replace(temp_path, path)
        _fsync_directory(path.parent)
    except BaseException:
        temp_path.unlink(missing_ok=True)
        raise


def _fsync_directory(directory: Path) -> None:
    try:
        descriptor = os.open(directory, os.O_RDONLY)
    except OSError:
        return
    try:
        os.fsync(descriptor)
    except OSError:
        pass
    finally:
        os.close(descriptor)


class ConfigStore:
    """Reads, validates, and atomically edits the Server configuration."""

    def __init__(
        self,
        path: str | Path | None = None,
        *,
        env: Mapping[str, str] | None = None,
        cli_overrides: Mapping[str, Any] | None = None,
        cwd: Path | None = None,
    ) -> None:
        self._explicit_path = Path(path).expanduser() if path is not None else None
        self._env = dict(os.environ) if env is None else dict(env)
        self._cli_overrides = dict(cli_overrides or {})
        self._cwd = cwd

    # -- discovery ---------------------------------------------------------

    def _discovered_path(self) -> Path | None:
        if self._explicit_path is not None:
            return self._explicit_path
        found, _ = find_config_file(None, env=self._env, cwd=self._cwd)
        return found

    @property
    def target_path(self) -> Path:
        """The file a save writes: the explicit path or the user config file."""
        discovered = self._discovered_path()
        if discovered is not None:
            return discovered
        return user_config_dir(self._env) / CONFIG_FILENAME

    @property
    def existing_path(self) -> Path | None:
        """The config file currently on disk, if any."""
        discovered = self._discovered_path()
        return discovered if discovered is not None and discovered.is_file() else None

    # -- effective values and sources -------------------------------------

    def _file_values(self) -> dict[str, Any]:
        path = self.existing_path
        if path is None:
            return {}
        return read_server_section(path)

    def _env_values(self) -> dict[str, Any]:
        return env_overrides(self._env)

    def _effective_values(self) -> dict[str, Any]:
        return deep_merge(deep_merge(self._file_values(), self._env_values()), self._cli_overrides)

    def _resolve(self, values: Mapping[str, Any] | None = None) -> ServerConfig:
        candidate = self._effective_values() if values is None else dict(values)
        try:
            return ServerConfig.model_validate(candidate)
        except ValidationError as exc:
            raise ConfigError(format_errors(exc.errors(), prefix="server.")) from exc

    def effective(self) -> ServerConfig:
        """The validated effective configuration."""
        return self._resolve()

    def states(self) -> dict[str, FieldState]:
        """Every leaf setting with its value, source, and lock state."""
        config = self.effective()
        file_values = self._file_values()
        env_values = self._env_values()
        cli_values = self._cli_overrides
        states: dict[str, FieldState] = {}
        for path, value in _leaf_items(config):
            in_cli, _ = _lookup(cli_values, path)
            in_env, _ = _lookup(env_values, path)
            in_file, _ = _lookup(file_values, path)
            if in_cli:
                states[path] = FieldState(value, "cli", True, "set by command line")
            elif in_env:
                env_name = f"{ENV_PREFIX}{path.upper().replace('.', '__')}"
                states[path] = FieldState(value, "env", True, f"set by {env_name}")
            elif in_file:
                states[path] = FieldState(value, "file")
            else:
                states[path] = FieldState(value, "default")
        return states

    def state(self, path: str) -> FieldState:
        """One leaf setting; raises ``KeyError`` for unknown paths."""
        return self.states()[path]

    @staticmethod
    def impact(path: str) -> ChangeImpact:
        """Whether a change to ``path`` applies hot or needs a restart."""
        top = path.split(".", 1)[0]
        return "hot" if top in _HOT_PREFIXES else "restart"

    # -- patching ----------------------------------------------------------

    def patch(self, changes: Mapping[str, Any]) -> SaveResult:
        """Validate and write a set of Server settings atomically.

        ``changes`` nests like the schema (``{"decode": {"beam_size": 3}}``).
        The whole effective configuration is validated first; on any error
        nothing is written. Fields forced by an environment variable or a CLI
        flag cannot be changed through the file.
        """
        if not isinstance(changes, Mapping):
            raise ConfigError("server: patch must be a mapping of field names to values")
        current = self.effective()
        candidate = self._resolve(
            deep_merge(
                deep_merge(
                    deep_merge(self._file_values(), changes),
                    self._env_values(),
                ),
                self._cli_overrides,
            )
        )
        requested = _flatten_patch(changes)
        before = dict(_leaf_items(current))
        after = dict(_leaf_items(candidate))
        states = self.states()
        # The effective values the patch asks for, ignoring env/CLI overrides,
        # so a locked field can be compared with what is being written.
        requested_values = dict(
            _leaf_items(self._resolve(deep_merge(current.model_dump(), changes)))
        )

        for path, _ in requested:
            state = states[path]
            if state.locked and requested_values[path] != state.value:
                raise ConfigError(
                    f"server.{path}: the effective value is {state.value!r} "
                    f"({state.reason}); change the overriding source instead"
                )

        changed = tuple(
            dict.fromkeys(path for path, _ in requested if before.get(path) != after.get(path))
        )
        if not changed:
            return SaveResult(config=current, written=False)

        document = self._load_document()
        root = document.root
        server = root.get("server")
        if not isinstance(server, CommentedMap):
            server = CommentedMap()
            root["server"] = server
        for path in changed:
            _set_path(server, path, _yaml_value(after[path]))

        target = self.target_path
        backup_path: Path | None = None
        if document.text and not document.representable:
            backup_path = target.with_name(f"{target.name}.bak")
            shutil.copyfile(target, backup_path)
        _atomic_write(target, _dump(root))

        hot = tuple(path for path in changed if self.impact(path) == "hot")
        restart = tuple(path for path in changed if self.impact(path) == "restart")
        return SaveResult(
            config=candidate,
            changed=changed,
            hot_applied=hot,
            restart_required=restart,
            written=True,
            backup_path=backup_path,
        )

    def _load_document(self) -> _Document:
        """Read the file for round-tripping, noting whether it can be preserved."""
        path = self.existing_path
        if path is None:
            return _Document("", CommentedMap(), True)
        try:
            text = path.read_text(encoding="utf-8")
        except OSError as exc:
            raise ConfigError(f"cannot read config file {path}: {exc}") from exc
        reader = YAML(typ="rt")
        reader.preserve_quotes = True
        try:
            loaded = reader.load(text)
        except RuamelYAMLError:
            loaded = None
        if isinstance(loaded, CommentedMap):
            return _Document(text, loaded, True)
        try:
            plain = yaml.safe_load(text)
        except yaml.YAMLError as exc:
            raise ConfigError(f"invalid YAML in {path}: {exc}") from exc
        if plain is None:
            return _Document(text, CommentedMap(), False)
        if not isinstance(plain, dict):
            raise ConfigError(f"{path}: top-level document must be a mapping")
        root = _yaml_value(plain)
        assert isinstance(root, CommentedMap)
        return _Document(text, root, False)
