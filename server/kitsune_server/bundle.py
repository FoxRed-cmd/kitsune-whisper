"""The native per-user Server bundle: layout, install, build, and uninstall.

The bundle is a pinned standalone Python runtime plus the Server package and
its resolved dependencies, placed under the user's data directory and run by a
launcher outside it. It needs no system Python and no Docker at runtime.

Two install sources exist:

- :class:`ArtifactSource` — a bundle archive produced by release CI (``bundle
  build``), downloaded or read locally and verified against a SHA-256.
- :class:`RuntimeSource` — a pinned standalone-runtime archive fetched by URL
  and verified against a SHA-256, then filled with the Server from a wheelhouse
  of resolved wheels using that runtime's own pip.

Downloading, extraction, and command execution are injected so tests never
touch the network or a real runtime. Configuration and models live outside the
bundle root and are never removed by :meth:`BundleInstaller.uninstall`.
"""

from __future__ import annotations

import contextlib
import hashlib
import json
import os
import platform
import shlex
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.request
from collections.abc import Callable, Iterator, Mapping, Sequence
from dataclasses import asdict, dataclass, replace
from pathlib import Path

from . import __version__
from .config import ServerConfig
from .supervisor import Invocation

BUNDLE_FORMAT = 1
RUNTIME_PYTHON = "3.11"
SERVER_PACKAGE = "kitsune-server"


class BundleError(Exception):
    """The bundle cannot be installed, built, or read."""


@dataclass(frozen=True)
class Layout:
    """Where one per-user installation keeps each piece.

    The bundle root holds the runtime and resolved environment; the launcher
    sits in the user's bin directory so a shell on ``PATH`` finds it. The
    configuration and model cache live elsewhere and survive uninstall.
    """

    os_name: str
    root: Path
    bin_dir: Path
    config_dir: Path

    @classmethod
    def default(
        cls,
        *,
        os_name: str | None = None,
        environ: Mapping[str, str] | None = None,
    ) -> Layout:
        """Resolve the per-user layout from the environment, without creating it."""
        resolved = os_name or current_os()
        environ = os.environ if environ is None else environ
        home = Path(environ.get("HOME") or Path.home())
        if resolved == "windows":
            local = environ.get("LOCALAPPDATA")
            local_dir = Path(local) if local else home / "AppData" / "Local"
            roaming = environ.get("APPDATA")
            roaming_dir = Path(roaming) if roaming else home / "AppData" / "Roaming"
            return cls(
                os_name=resolved,
                root=local_dir / "kitsune-whisper" / "Server",
                bin_dir=local_dir / "kitsune-whisper" / "bin",
                config_dir=roaming_dir / "kitsune-whisper",
            )
        data = environ.get("XDG_DATA_HOME")
        data_dir = Path(data) if data else home / ".local" / "share"
        config = environ.get("XDG_CONFIG_HOME")
        config_dir = Path(config) if config else home / ".config"
        return cls(
            os_name=resolved,
            root=data_dir / "kitsune-whisper" / "server",
            bin_dir=home / ".local" / "bin",
            config_dir=config_dir / "kitsune-whisper",
        )

    @property
    def is_windows(self) -> bool:
        return self.os_name == "windows"

    @property
    def runtime_dir(self) -> Path:
        return self.root / "runtime"

    @property
    def lib_dir(self) -> Path:
        return self.root / "lib"

    @property
    def launcher_module(self) -> Path:
        return self.root / "launcher.py"

    @property
    def manifest_path(self) -> Path:
        return self.root / "bundle.json"

    @property
    def runtime_python(self) -> str:
        """The runtime's executable path, relative to ``runtime/``."""
        return "python.exe" if self.is_windows else "bin/python3"

    @property
    def python(self) -> Path:
        return self.runtime_dir / self.runtime_python

    @property
    def pythonw(self) -> Path:
        """The windowless interpreter the Windows supervisor must run."""
        if self.is_windows:
            return self.runtime_dir / "pythonw.exe"
        return self.python

    @property
    def launcher(self) -> Path:
        name = "kitsune-server.cmd" if self.is_windows else "kitsune-server"
        return self.bin_dir / name


def current_os() -> str:
    if sys.platform == "win32":
        return "windows"
    if sys.platform.startswith("linux"):
        return "linux"
    return sys.platform


def current_arch() -> str:
    return platform.machine() or "unknown"


@dataclass(frozen=True)
class Manifest:
    """``bundle.json``: what was installed, where it came from, and where its data lives."""

    version: str
    format: int = BUNDLE_FORMAT
    python: str = RUNTIME_PYTHON
    platform: str = ""
    arch: str = ""
    source: str = ""
    entry: str = ""
    config_dir: str = ""
    models_dir: str = ""

    def to_json(self) -> str:
        return json.dumps(asdict(self), indent=2, sort_keys=True) + "\n"

    @classmethod
    def from_path(cls, path: Path) -> Manifest:
        try:
            document = json.loads(path.read_text(encoding="utf-8"))
        except FileNotFoundError as exc:
            raise BundleError(f"bundle manifest is missing: {path}") from exc
        except (OSError, json.JSONDecodeError) as exc:
            raise BundleError(f"cannot read bundle manifest {path}: {exc}") from exc
        if not isinstance(document, dict):
            raise BundleError(f"bundle manifest {path} must be a JSON object")
        try:
            manifest = cls(**document)
        except TypeError as exc:
            raise BundleError(f"bundle manifest {path} is invalid: {exc}") from exc
        if manifest.format != BUNDLE_FORMAT:
            raise BundleError(
                f"unsupported bundle format {manifest.format} "
                f"(this Server understands {BUNDLE_FORMAT})"
            )
        if not manifest.version:
            raise BundleError(f"bundle manifest {path} has no version")
        return manifest


@dataclass(frozen=True)
class ArtifactSource:
    """A prebuilt bundle archive from release CI."""

    location: str
    sha256: str


@dataclass(frozen=True)
class RuntimeSource:
    """A pinned standalone Python runtime plus the wheelhouse to install into it."""

    url: str
    sha256: str
    wheelhouse: Path


Downloader = Callable[[str, Path], None]
Extractor = Callable[[Path, Path], None]
CommandRunner = Callable[[Sequence[str]], None]


def download_file(url: str, destination: Path) -> None:
    destination.parent.mkdir(parents=True, exist_ok=True)
    try:
        with urllib.request.urlopen(url) as response, destination.open("wb") as handle:
            shutil.copyfileobj(response, handle)
    except OSError as exc:
        raise BundleError(f"cannot download {url}: {exc}") from exc


def extract_tar(archive: Path, destination: Path) -> None:
    destination.mkdir(parents=True, exist_ok=True)
    try:
        with tarfile.open(archive) as tar:
            tar.extractall(destination, filter="data")
    except tarfile.TarError as exc:
        raise BundleError(f"cannot extract {archive.name}: expected a tar archive ({exc})") from exc


def sha256_file(path: Path) -> str:
    digest = hashlib.sha256()
    with path.open("rb") as handle:
        for chunk in iter(lambda: handle.read(1024 * 1024), b""):
            digest.update(chunk)
    return digest.hexdigest()


def run_checked(args: Sequence[str]) -> None:
    completed = subprocess.run(list(args), capture_output=True, text=True, check=False)
    if completed.returncode != 0:
        output = " ".join((completed.stderr or completed.stdout).split()) or "(no output)"
        raise BundleError(f"{' '.join(args)} failed (exit {completed.returncode}): {output}")


def launcher_script(layout: Layout) -> str:
    """Render the shell/batch launcher that runs the bundle with its own runtime."""
    python = shlex.quote(str(layout.python))
    module = shlex.quote(str(layout.launcher_module))
    if layout.is_windows:
        return f"""@echo off
rem Generated by kitsune-server: runs the bundled Server with its pinned runtime.
"{layout.python}" "{layout.launcher_module}" %*
"""
    return f"""#!/bin/sh
# Generated by kitsune-server: runs the bundled Server with its pinned runtime.
set -eu
exec {python} {module} "$@"
"""


def launcher_module(layout: Layout) -> str:
    """Render ``launcher.py``: prepend the bundle's lib and run the CLI."""
    return f'''"""Generated by kitsune-server: run the Server with the bundle's own libraries."""

import sys

sys.path.insert(0, {str(layout.lib_dir)!r})

from kitsune_server.cli import main

raise SystemExit(main())
'''


def supervisor_invocation(
    layout: Layout,
    *,
    config: str | None = None,
    command: str = "start",
) -> Invocation:
    """The invocation the OS supervisor runs.

    On Windows this is ``pythonw.exe`` so Task Scheduler opens no console
    window; elsewhere it is the bundled ``python3``.
    """
    python = layout.pythonw if layout.is_windows else layout.python
    args = (str(layout.launcher_module), command)
    if config:
        args += ("--config", config)
    return Invocation(str(python), args)


class BundleInstaller:
    """Installs, upgrades, and removes one per-user bundle."""

    def __init__(
        self,
        layout: Layout,
        *,
        download: Downloader | None = None,
        extract: Extractor | None = None,
        run: CommandRunner | None = None,
        models_dir: Path | None = None,
    ) -> None:
        self.layout = layout
        self._download = download or download_file
        self._extract = extract or extract_tar
        self._run = run or run_checked
        self._models_dir = models_dir

    @property
    def models_dir(self) -> Path:
        if self._models_dir is not None:
            return self._models_dir
        return Path(ServerConfig().download_root).expanduser()

    def install(self, source: ArtifactSource | RuntimeSource) -> Manifest:
        """Install or replace the bundle from ``source`` and return its manifest."""
        if isinstance(source, ArtifactSource):
            return self._install_artifact(source)
        if isinstance(source, RuntimeSource):
            return self._install_runtime(source)
        raise BundleError(f"unknown bundle source: {source!r}")

    def uninstall(self) -> None:
        """Remove the launcher and bundle root, keeping configuration and models.

        Idempotent: a missing installation is not an error.
        """
        _remove_path(self.layout.launcher)
        _remove_path(self.layout.root)

    # -- sources -----------------------------------------------------------

    def _install_artifact(self, source: ArtifactSource) -> Manifest:
        with self._workspace() as workspace:
            archive = self._fetch(source.location, workspace)
            _verify(archive, source.sha256)
            stage = workspace / "bundle"
            self._extract(archive, stage)
            _validate_stage(stage, self.layout)
            self._activate(stage)
        manifest = Manifest.from_path(self.layout.manifest_path)
        return self._finish(manifest, source="artifact")

    def _install_runtime(self, source: RuntimeSource) -> Manifest:
        wheelhouse = Path(source.wheelhouse).expanduser()
        if not wheelhouse.is_dir():
            raise BundleError(
                f"wheelhouse not found: {wheelhouse}; pass the directory of resolved "
                "wheels shipped with the release"
            )
        with self._workspace() as workspace:
            archive = self._fetch(source.url, workspace)
            _verify(archive, source.sha256)
            stage = workspace / "bundle"
            stage.mkdir()
            runtime = stage / "runtime"
            self._extract(archive, runtime)
            _normalise_runtime(runtime, self.layout)
            python = runtime / self.layout.runtime_python
            if not python.is_file():
                raise BundleError(
                    f"runtime archive has no {self.layout.runtime_python}; "
                    "the pinned archive is not a standalone Python runtime"
                )
            if not self.layout.is_windows:
                python.chmod(0o755)
            lib = stage / "lib"
            lib.mkdir()
            self._run(
                [
                    str(python),
                    "-m",
                    "pip",
                    "install",
                    "--no-index",
                    "--find-links",
                    str(wheelhouse),
                    "--target",
                    str(lib),
                    f"{SERVER_PACKAGE}=={__version__}",
                ]
            )
            if not any(lib.iterdir()):
                raise BundleError(
                    f"pip installed nothing into {lib}; the wheelhouse has no "
                    f"{SERVER_PACKAGE}=={__version__}"
                )
            manifest = Manifest(
                version=__version__,
                python=RUNTIME_PYTHON,
                platform=self.layout.os_name,
                arch=current_arch(),
            )
            (stage / "bundle.json").write_text(manifest.to_json(), encoding="utf-8")
            self._activate(stage)
        return self._finish(manifest, source="runtime")

    # -- helpers -----------------------------------------------------------

    @contextlib.contextmanager
    def _workspace(self) -> Iterator[Path]:
        self.layout.root.parent.mkdir(parents=True, exist_ok=True)
        with tempfile.TemporaryDirectory(
            dir=self.layout.root.parent, prefix=".kitsune-server-"
        ) as name:
            yield Path(name)

    def _fetch(self, location: str, workspace: Path) -> Path:
        if location.startswith(("http://", "https://")):
            archive = workspace / "payload"
            self._download(location, archive)
            return archive
        path = Path(location).expanduser()
        if not path.is_file():
            raise BundleError(f"bundle artifact not found: {path}")
        return path

    def _activate(self, stage: Path) -> None:
        """Replace the installed root with a fully staged bundle."""
        _remove_path(self.layout.root)
        os.replace(stage, self.layout.root)

    def _finish(self, manifest: Manifest, *, source: str) -> Manifest:
        installed = replace(
            manifest,
            source=source,
            entry=str(self.layout.launcher),
            config_dir=str(self.layout.config_dir),
            models_dir=str(self.models_dir),
            python=manifest.python or RUNTIME_PYTHON,
        )
        self._write_launchers()
        self.layout.manifest_path.write_text(installed.to_json(), encoding="utf-8")
        return installed

    def _write_launchers(self) -> None:
        self.layout.launcher.parent.mkdir(parents=True, exist_ok=True)
        self.layout.launcher.write_text(
            launcher_script(self.layout), encoding="utf-8", newline="\n"
        )
        self.layout.launcher.chmod(0o755)
        self.layout.launcher_module.write_text(
            launcher_module(self.layout), encoding="utf-8", newline="\n"
        )


def _verify(archive: Path, expected: str) -> None:
    actual = sha256_file(archive)
    if actual != expected.strip().lower():
        raise BundleError(
            f"sha256 mismatch for {archive.name}: expected {expected.strip().lower()}, got {actual}"
        )


def _validate_stage(stage: Path, layout: Layout) -> None:
    Manifest.from_path(stage / "bundle.json")
    if not (stage / "runtime" / layout.runtime_python).is_file():
        raise BundleError(
            f"bundle artifact is missing its pinned runtime (runtime/{layout.runtime_python})"
        )
    lib = stage / "lib"
    if not lib.is_dir() or not any(lib.iterdir()):
        raise BundleError("bundle artifact is missing its resolved environment (lib/)")


def _normalise_runtime(runtime: Path, layout: Layout) -> None:
    """Flatten a single top-level directory (python-build-standalone layout)."""
    if (runtime / layout.runtime_python).exists():
        return
    children = [child for child in runtime.iterdir() if child.is_dir()]
    if len(children) != 1:
        return
    inner = children[0]
    for child in list(inner.iterdir()):
        shutil.move(str(child), str(runtime / child.name))
    inner.rmdir()


def _remove_path(path: Path) -> None:
    if path.is_symlink() or path.is_file():
        path.unlink(missing_ok=True)
    elif path.is_dir():
        shutil.rmtree(path)


def build_bundle(
    output: Path,
    *,
    runtime_dir: Path,
    site_packages: Sequence[Path],
    platform: str | None = None,
    arch: str | None = None,
    python: str = RUNTIME_PYTHON,
    version: str | None = None,
) -> Path:
    """Assemble a release bundle archive from a resolved environment.

    ``runtime_dir`` is a pinned standalone Python tree (``bin/python3`` on Linux,
    ``python.exe`` on Windows); each ``site_packages`` directory contributes its
    children to the bundle's ``lib/``. Release CI calls this after resolving the
    Server's dependencies, then publishes the archive with a checksum.
    """
    resolved_platform = platform or current_os()
    runtime_dir = Path(runtime_dir)
    relative_python = "python.exe" if resolved_platform == "windows" else "bin/python3"
    if not (runtime_dir / relative_python).is_file():
        raise BundleError(
            f"runtime directory {runtime_dir} has no {relative_python}; "
            "point --runtime at a standalone Python tree"
        )
    output = Path(output)
    output.parent.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(
        dir=output.parent, prefix=".kitsune-server-build-"
    ) as workspace_name:
        stage = Path(workspace_name) / "bundle"
        stage.mkdir()
        shutil.copytree(runtime_dir, stage / "runtime", symlinks=True)
        lib = stage / "lib"
        lib.mkdir()
        for directory in site_packages:
            source = Path(directory)
            if not source.is_dir():
                raise BundleError(f"site-packages directory not found: {source}")
            for child in source.iterdir():
                target = lib / child.name
                if target.exists():
                    continue
                if child.is_dir():
                    shutil.copytree(child, target, symlinks=True)
                else:
                    shutil.copy2(child, target)
        manifest = Manifest(
            version=version or __version__,
            python=python,
            platform=resolved_platform,
            arch=arch or current_arch(),
        )
        (stage / "bundle.json").write_text(manifest.to_json(), encoding="utf-8")
        with tarfile.open(output, "w:gz") as archive:
            for child in sorted(stage.iterdir()):
                archive.add(child, arcname=child.name)
    return output


__all__ = [
    "BUNDLE_FORMAT",
    "RUNTIME_PYTHON",
    "ArtifactSource",
    "BundleError",
    "BundleInstaller",
    "Layout",
    "Manifest",
    "RuntimeSource",
    "build_bundle",
    "current_os",
    "download_file",
    "extract_tar",
    "launcher_module",
    "launcher_script",
    "run_checked",
    "sha256_file",
    "supervisor_invocation",
]
