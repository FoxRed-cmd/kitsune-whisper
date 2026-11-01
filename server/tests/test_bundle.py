from __future__ import annotations

import hashlib
import io
import json
import stat
import tarfile
import zipfile
from collections.abc import Sequence
from pathlib import Path

import pytest
from kitsune_server import __version__, bundle
from kitsune_server.bundle import (
    ArtifactSource,
    BundleError,
    BundleInstaller,
    Layout,
    Manifest,
    RuntimeSource,
    build_bundle,
)


class FakeDownloader:
    """Writes a canned archive to the destination the installer asks for."""

    def __init__(self, payload: bytes) -> None:
        self.payload = payload
        self.calls: list[tuple[str, Path]] = []

    def __call__(self, url: str, destination: Path) -> None:
        self.calls.append((url, destination))
        destination.write_bytes(self.payload)


class RecordingExtractor:
    def __init__(self) -> None:
        self.calls: list[tuple[Path, Path]] = []

    def __call__(self, archive: Path, destination: Path) -> None:
        self.calls.append((archive, destination))
        bundle.extract_tar(archive, destination)


class RecordingRunner:
    """Stands in for pip: records the command and populates the target dir."""

    def __init__(self) -> None:
        self.calls: list[tuple[str, ...]] = []

    def __call__(self, args: Sequence[str]) -> None:
        self.calls.append(tuple(args))
        if "--target" in args:
            target = Path(args[list(args).index("--target") + 1])
            (target / "kitsune_server").mkdir(parents=True, exist_ok=True)
            (target / "kitsune_server" / "__init__.py").write_text("", encoding="utf-8")

    @property
    def commands(self) -> list[str]:
        return [" ".join(call) for call in self.calls]


def layout(tmp_path: Path, os_name: str = "linux") -> Layout:
    if os_name == "windows":
        return Layout.default(
            os_name="windows",
            environ={
                "HOME": str(tmp_path / "home"),
                "APPDATA": str(tmp_path / "home" / "AppData" / "Roaming"),
                "LOCALAPPDATA": str(tmp_path / "home" / "AppData" / "Local"),
            },
        )
    return Layout.default(
        os_name="linux",
        environ={
            "HOME": str(tmp_path / "home"),
            "XDG_DATA_HOME": str(tmp_path / "home" / ".local" / "share"),
            "XDG_CONFIG_HOME": str(tmp_path / "home" / ".config"),
        },
    )


def digest(payload: bytes) -> str:
    return hashlib.sha256(payload).hexdigest()


def add_bytes(archive: tarfile.TarFile, name: str, payload: bytes, mode: int = 0o644) -> None:
    info = tarfile.TarInfo(name)
    info.size = len(payload)
    info.mode = mode
    archive.addfile(info, io.BytesIO(payload))


def bundle_artifact_bytes(
    *,
    version: str = "0.1.0",
    platform: str = "linux",
) -> bytes:
    buffer = io.BytesIO()
    runtime_python = "runtime/python.exe" if platform == "windows" else "runtime/bin/python3"
    with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
        add_bytes(archive, runtime_python, b"#!/bin/sh\n", 0o755)
        add_bytes(archive, "lib/kitsune_server/__init__.py", b"", 0o644)
        manifest = Manifest(
            version=version,
            python="3.11.9",
            platform=platform,
            arch="x86_64",
        )
        add_bytes(archive, "bundle.json", manifest.to_json().encode(), 0o644)
    return buffer.getvalue()


def runtime_archive_bytes() -> bytes:
    """A python-build-standalone style archive with one top-level ``python/``."""
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
        add_bytes(archive, "python/bin/python3", b"#!/bin/sh\n", 0o755)
        add_bytes(archive, "python/lib/python3.11/os.py", b"", 0o644)
    return buffer.getvalue()


def installer(layout_: Layout, **kwargs: object) -> BundleInstaller:
    options: dict[str, object] = {
        "models_dir": layout_.root.parent / "models",
    }
    options.update(kwargs)
    return BundleInstaller(layout_, **options)  # type: ignore[arg-type]


def test_linux_layout_is_per_user_and_keeps_config_separate(tmp_path: Path) -> None:
    paths = layout(tmp_path)

    home = tmp_path / "home"
    assert paths.root == home / ".local" / "share" / "kitsune-whisper" / "server"
    assert paths.bin_dir == home / ".local" / "bin"
    assert paths.launcher == home / ".local" / "bin" / "kitsune-server"
    assert paths.config_dir == home / ".config" / "kitsune-whisper"
    assert paths.python == paths.runtime_dir / "bin" / "python3"
    assert paths.manifest_path == paths.root / "bundle.json"


def test_windows_layout_uses_localappdata_and_a_cmd_launcher(tmp_path: Path) -> None:
    paths = layout(tmp_path, "windows")

    assert "AppData/Local" in paths.root.as_posix()
    assert paths.launcher.name == "kitsune-server.cmd"
    assert paths.python.name == "python.exe"
    assert paths.pythonw.name == "pythonw.exe"


def test_install_from_artifact_verifies_and_lays_out_the_bundle(tmp_path: Path) -> None:
    paths = layout(tmp_path)
    payload = bundle_artifact_bytes(version="1.2.3")
    downloader = FakeDownloader(payload)
    extractor = RecordingExtractor()
    subject = installer(paths, download=downloader, extract=extractor)

    manifest = subject.install(
        ArtifactSource(location="https://example.test/bundle.tar.gz", sha256=digest(payload))
    )

    assert downloader.calls[0][0] == "https://example.test/bundle.tar.gz"
    assert extractor.calls
    assert paths.python.is_file()
    assert (paths.lib_dir / "kitsune_server" / "__init__.py").is_file()
    assert paths.launcher.is_file()
    assert paths.launcher.stat().st_mode & stat.S_IXUSR
    assert paths.launcher.read_text(encoding="utf-8").startswith("#!/bin/sh")
    assert manifest.version == "1.2.3"
    assert manifest.source == "artifact"
    assert manifest.entry == str(paths.launcher)
    assert manifest.config_dir == str(paths.config_dir)
    assert manifest.models_dir == str(paths.root.parent / "models")
    assert Manifest.from_path(paths.manifest_path) == manifest


def test_install_from_artifact_rejects_a_checksum_mismatch(tmp_path: Path) -> None:
    paths = layout(tmp_path)
    payload = bundle_artifact_bytes()
    subject = installer(paths, download=FakeDownloader(payload))

    with pytest.raises(BundleError) as caught:
        subject.install(
            ArtifactSource(location="https://example.test/bundle.tar.gz", sha256="0" * 64)
        )

    assert "sha256" in str(caught.value).lower()
    assert not paths.root.exists()


def test_install_from_artifact_rejects_an_unknown_format(tmp_path: Path) -> None:
    paths = layout(tmp_path)
    buffer = io.BytesIO()
    with tarfile.open(fileobj=buffer, mode="w:gz") as archive:
        add_bytes(archive, "runtime/bin/python3", b"", 0o755)
        add_bytes(archive, "lib/.keep", b"", 0o644)
        add_bytes(
            archive,
            "bundle.json",
            json.dumps({"format": 99, "version": "9.9.9"}).encode(),
            0o644,
        )
    payload = buffer.getvalue()
    subject = installer(paths, download=FakeDownloader(payload))

    with pytest.raises(BundleError, match="format 99"):
        subject.install(
            ArtifactSource(location="https://example.test/bundle.tar.gz", sha256=digest(payload))
        )


def test_install_from_artifact_accepts_a_local_file(tmp_path: Path) -> None:
    paths = layout(tmp_path)
    artifact = tmp_path / "kitsune-server.tar.gz"
    payload = bundle_artifact_bytes()
    artifact.write_bytes(payload)
    downloader = FakeDownloader(b"")
    subject = installer(paths, download=downloader)

    subject.install(ArtifactSource(location=str(artifact), sha256=digest(payload)))

    assert downloader.calls == []
    assert paths.python.is_file()


def test_install_from_a_pinned_runtime_and_wheelhouse(tmp_path: Path) -> None:
    paths = layout(tmp_path)
    payload = runtime_archive_bytes()
    downloader = FakeDownloader(payload)
    extractor = RecordingExtractor()
    runner = RecordingRunner()
    wheelhouse = tmp_path / "wheelhouse"
    wheelhouse.mkdir()
    subject = installer(
        paths,
        download=downloader,
        extract=extractor,
        run=runner,
    )

    manifest = subject.install(
        RuntimeSource(
            url="https://example.test/python-3.11.9.tar.gz",
            sha256=digest(payload),
            wheelhouse=wheelhouse,
        )
    )

    assert downloader.calls[0][0] == "https://example.test/python-3.11.9.tar.gz"
    assert paths.python.is_file()
    args = runner.calls[0]
    staged_python = Path(args[0])
    assert staged_python.parts[-3:] == ("runtime", "bin", "python3")
    assert "-m pip install" in runner.commands[0]
    assert "--no-index" in args
    assert str(wheelhouse) in args
    staged_lib = Path(args[list(args).index("--target") + 1])
    assert staged_lib.parts[-2:] == ("bundle", "lib")
    assert f"kitsune-server=={__version__}" in args
    assert manifest.source == "runtime"
    assert manifest.python == "3.11"


def test_runtime_install_requires_a_wheelhouse(tmp_path: Path) -> None:
    paths = layout(tmp_path)
    payload = runtime_archive_bytes()
    subject = installer(paths, download=FakeDownloader(payload))

    with pytest.raises(BundleError, match="wheelhouse"):
        subject.install(
            RuntimeSource(
                url="https://example.test/python.tar.gz",
                sha256=digest(payload),
                wheelhouse=tmp_path / "missing",
            )
        )


def test_reinstall_replaces_the_previous_bundle(tmp_path: Path) -> None:
    paths = layout(tmp_path)
    payload = bundle_artifact_bytes()
    source = ArtifactSource(location="https://example.test/bundle.tar.gz", sha256=digest(payload))
    subject = installer(paths, download=FakeDownloader(payload))
    subject.install(source)
    (paths.root / "stale.txt").write_text("old", encoding="utf-8")

    subject.install(source)

    assert not (paths.root / "stale.txt").exists()


def test_uninstall_removes_the_bundle_and_launcher_but_keeps_config_and_models(
    tmp_path: Path,
) -> None:
    paths = layout(tmp_path)
    payload = bundle_artifact_bytes()
    models = paths.root.parent / "models"
    subject = installer(paths, download=FakeDownloader(payload))
    subject.install(
        ArtifactSource(location="https://example.test/bundle.tar.gz", sha256=digest(payload))
    )
    config_file = paths.config_dir / "kitsune.yaml"
    config_file.parent.mkdir(parents=True)
    config_file.write_text("server:\n  model: small\n", encoding="utf-8")
    models.mkdir(parents=True)
    (models / "model.bin").write_bytes(b"weights")

    subject.uninstall()

    assert not paths.root.exists()
    assert not paths.launcher.exists()
    assert config_file.read_text(encoding="utf-8") == "server:\n  model: small\n"
    assert (models / "model.bin").read_bytes() == b"weights"


def test_uninstall_is_idempotent(tmp_path: Path) -> None:
    subject = installer(layout(tmp_path))

    subject.uninstall()
    subject.uninstall()


def test_launcher_module_uses_the_bundled_lib(tmp_path: Path) -> None:
    paths = layout(tmp_path)
    payload = bundle_artifact_bytes()
    subject = installer(paths, download=FakeDownloader(payload))

    subject.install(
        ArtifactSource(location="https://example.test/bundle.tar.gz", sha256=digest(payload))
    )

    module = paths.launcher_module.read_text(encoding="utf-8")
    assert f"sys.path.insert(0, {str(paths.lib_dir)!r})" in module
    assert "from kitsune_server.cli import main" in module
    script = paths.launcher.read_text(encoding="utf-8")
    assert str(paths.python) in script
    assert str(paths.launcher_module) in script


def test_windows_launcher_uses_python_and_the_hidden_invocation_uses_pythonw(
    tmp_path: Path,
) -> None:
    paths = layout(tmp_path, "windows")
    payload = bundle_artifact_bytes(platform="windows")
    subject = installer(paths, download=FakeDownloader(payload))

    subject.install(
        ArtifactSource(location="https://example.test/bundle.tar.gz", sha256=digest(payload))
    )

    assert "@echo off" in paths.launcher.read_text(encoding="utf-8")
    invocation = bundle.supervisor_invocation(paths, config="C:/config/kitsune.yaml")
    assert invocation.command == str(paths.pythonw)
    assert invocation.args == (
        str(paths.launcher_module),
        "start",
        "--config",
        "C:/config/kitsune.yaml",
    )


def test_linux_supervisor_invocation_uses_the_bundled_python(tmp_path: Path) -> None:
    paths = layout(tmp_path)

    invocation = bundle.supervisor_invocation(paths)

    assert invocation.command == str(paths.python)
    assert invocation.args == (str(paths.launcher_module), "start")


def test_build_bundle_stages_runtime_packages_and_manifest(tmp_path: Path) -> None:
    runtime = tmp_path / "python"
    (runtime / "bin").mkdir(parents=True)
    (runtime / "bin" / "python3").write_text("", encoding="utf-8")
    site_packages = tmp_path / "site-packages"
    (site_packages / "kitsune_server").mkdir(parents=True)
    (site_packages / "kitsune_server" / "__init__.py").write_text("", encoding="utf-8")
    (site_packages / "fastapi").mkdir()
    (site_packages / "fastapi" / "__init__.py").write_text("", encoding="utf-8")
    output = tmp_path / "dist" / "kitsune-server_linux_amd64.tar.gz"

    built = build_bundle(output, runtime_dir=runtime, site_packages=[site_packages])

    assert built == output
    with tarfile.open(output) as archive:
        names = set(archive.getnames())
    assert "runtime/bin/python3" in names
    assert "lib/kitsune_server/__init__.py" in names
    assert "lib/fastapi/__init__.py" in names
    assert "bundle.json" in names


def test_built_bundle_manifest_names_the_version_and_layout(tmp_path: Path) -> None:
    runtime = tmp_path / "python"
    (runtime / "bin").mkdir(parents=True)
    (runtime / "bin" / "python3").write_text("", encoding="utf-8")
    site_packages = tmp_path / "site-packages"
    (site_packages / "kitsune_server").mkdir(parents=True)
    output = tmp_path / "bundle.tar.gz"

    build_bundle(
        output,
        runtime_dir=runtime,
        site_packages=[site_packages],
        platform="linux",
        arch="x86_64",
    )

    with tarfile.open(output) as archive:
        manifest = json.loads(archive.extractfile("bundle.json").read())  # type: ignore[union-attr]
    assert manifest["format"] == bundle.BUNDLE_FORMAT
    assert manifest["version"] == __version__
    assert manifest["platform"] == "linux"
    assert manifest["arch"] == "x86_64"


def test_build_bundle_rejects_a_runtime_without_python(tmp_path: Path) -> None:
    runtime = tmp_path / "python"
    runtime.mkdir()
    site_packages = tmp_path / "site-packages"
    site_packages.mkdir()

    with pytest.raises(BundleError, match="runtime"):
        build_bundle(tmp_path / "bundle.tar.gz", runtime_dir=runtime, site_packages=[site_packages])


def test_build_then_install_round_trips(tmp_path: Path) -> None:
    runtime = tmp_path / "python"
    (runtime / "bin").mkdir(parents=True)
    (runtime / "bin" / "python3").write_text("#!/bin/sh\n", encoding="utf-8")
    site_packages = tmp_path / "site-packages"
    (site_packages / "kitsune_server").mkdir(parents=True)
    (site_packages / "kitsune_server" / "__init__.py").write_text("", encoding="utf-8")
    artifact = build_bundle(
        tmp_path / "bundle.tar.gz", runtime_dir=runtime, site_packages=[site_packages]
    )
    paths = layout(tmp_path)
    subject = installer(paths)

    subject.install(ArtifactSource(location=str(artifact), sha256=digest(artifact.read_bytes())))

    assert paths.python.is_file()
    assert Manifest.from_path(paths.manifest_path).version == __version__


def test_zip_artifacts_are_rejected_with_a_clear_message(tmp_path: Path) -> None:
    paths = layout(tmp_path)
    archive = tmp_path / "bundle.zip"
    with zipfile.ZipFile(archive, "w") as zipped:
        zipped.writestr("bundle.json", "{}")
    subject = installer(paths)

    with pytest.raises(BundleError, match="tar"):
        subject.install(ArtifactSource(location=str(archive), sha256=digest(archive.read_bytes())))
