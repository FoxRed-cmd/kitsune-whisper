"""Behaviour of the Linux release installer, exercised in a sandboxed HOME.

The installer is a public seam: its contract is the files it writes, the
commands it runs, and what it prints. These tests put a temporary HOME, a
temporary XDG tree and a fake PATH in front of it, so no real privilege
escalation, service manager, package manager, or release download is touched.
"""

from __future__ import annotations

import hashlib
import io
import json
import os
import re
import stat
import subprocess
import tarfile
from dataclasses import dataclass
from datetime import datetime
from pathlib import Path
from typing import Any

import pytest

REPO_ROOT = Path(__file__).resolve().parents[2]
INSTALL_SH = REPO_ROOT / "install.sh"
RELEASE_PUBLIC_KEY = REPO_ROOT / "packaging" / "keys" / "kitsune-release-public.pem"
WAYLAND_BUILDER = REPO_ROOT / "scripts" / "build-wayland-tools.sh"

HELP_FLAGS = (
    "--client-only",
    "--server-only",
    "--from",
    "--force-config",
    "--force",
    "--dry-run",
    "--uninstall",
    "--purge",
    "--remove-models",
    "--remove-server-data",
    "--version",
    "--check",
    "--insecure",
    "--help",
)


@dataclass(frozen=True)
class ReleaseKey:
    """A throwaway ECDSA P-256 keypair used to sign the fake artifact set."""

    private: Path
    public: Path


def make_release_key(directory: Path) -> ReleaseKey:
    directory.mkdir(parents=True, exist_ok=True)
    private = directory / "private.pem"
    public = directory / "public.pem"
    subprocess.run(
        ["openssl", "ecparam", "-name", "prime256v1", "-genkey", "-noout", "-out", str(private)],
        check=True,
        capture_output=True,
    )
    subprocess.run(
        ["openssl", "ec", "-in", str(private), "-pubout", "-out", str(public)],
        check=True,
        capture_output=True,
    )
    return ReleaseKey(private=private, public=public)


@pytest.fixture(scope="session")
def release_key(tmp_path_factory: pytest.TempPathFactory) -> ReleaseKey:
    return make_release_key(tmp_path_factory.mktemp("release-key"))


RETIRED_FLAGS = (
    "--no-client",
    "--no-server",
    "--no-wayland",
    "--no-elevate",
    "--gui",
    "--cuda",
)


def digest(path: Path) -> str:
    return hashlib.sha256(path.read_bytes()).hexdigest()


def tree_snapshot(root: Path) -> dict[str, str]:
    """Every path under root with its content digest, for change-nothing checks."""
    snapshot: dict[str, str] = {}
    for path in sorted(root.rglob("*")):
        relative = str(path.relative_to(root))
        snapshot[relative] = digest(path) if path.is_file() else "<dir>"
    return snapshot


def write_tar(path: Path, files: dict[str, tuple[str, int]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with tarfile.open(path, "w:gz") as archive:
        for name, (content, mode) in files.items():
            data = content.encode("utf-8")
            info = tarfile.TarInfo(name)
            info.size = len(data)
            info.mode = mode
            archive.addfile(info, io.BytesIO(data))


def client_script() -> str:
    return '#!/bin/sh\necho "client: $*" >> "$CALL_LOG"\nexit 0\n'


def server_runtime_script() -> str:
    return (
        "#!/bin/sh\n"
        'echo "server: $*" >> "$CALL_LOG"\n'
        'if [ "${FAKE_SERVER_EXIT:-0}" != 0 ]; then exit "$FAKE_SERVER_EXIT"; fi\n'
        # The real bootstrap.py leaves a launcher behind; the manifest records
        # components from the installed tree, so the double must do the same.
        'mkdir -p "$HOME/.local/bin" "$HOME/.local/lib/kitsune-whisper/server"\n'
        ': > "$HOME/.local/bin/kitsune-server"\n'
        'chmod 0755 "$HOME/.local/bin/kitsune-server"\n'
        "exit 0\n"
    )


def sign_checksums(artifacts: Path, key: ReleaseKey) -> None:
    subprocess.run(
        [
            "openssl",
            "dgst",
            "-sha256",
            "-sign",
            str(key.private),
            "-out",
            str(artifacts / "checksums.txt.sig"),
            str(artifacts / "checksums.txt"),
        ],
        check=True,
        capture_output=True,
    )


def make_artifacts(
    tmp_path: Path,
    key: ReleaseKey,
    *,
    with_server: bool = False,
    version: str = "1.0.0",
    name: str = "artifacts",
) -> Path:
    """Build and sign the artifact set the installer is allowed to consume.

    There is deliberately no Wayland helper archive: a request for one would
    fail the install, so a passing install proves none is fetched.
    """
    artifacts = tmp_path / name
    artifacts.mkdir()
    template = "client:\n  hotkey: Ctrl+Shift+Space\n"
    (artifacts / "VERSION").write_text(f"{version}\n", encoding="utf-8")
    write_tar(
        artifacts / "kitsune-client_linux_amd64.tar.gz",
        {
            "kitsune-client": (client_script(), 0o755),
            "kitsune.example.yaml": (template, 0o644),
        },
    )
    if with_server:
        write_tar(
            artifacts / "kitsune-server_linux_amd64.tar.gz",
            {
                "bootstrap.py": ("import sys\n", 0o644),
                "bundle.json": (f'{{"version": "{version}"}}\n', 0o644),
                "lib/kitsune_server/__init__.py": ("", 0o644),
                "runtime/bin/python3": (server_runtime_script(), 0o755),
            },
        )
    checksums = []
    for archive in sorted(artifacts.glob("*.tar.gz")):
        checksums.append(f"{digest(archive)}  {archive.name}")
    (artifacts / "checksums.txt").write_text("\n".join(checksums) + "\n", encoding="utf-8")
    sign_checksums(artifacts, key)
    return artifacts


def fake_commands(tmp_path: Path) -> Path:
    """Commands that must never run are logged instead of executed."""
    directory = tmp_path / "fake-bin"
    directory.mkdir()
    scripts = {
        "sudo": '#!/bin/sh\necho "sudo $*" >> "$SUDO_LOG"\nexit 0\n',
        "pkexec": '#!/bin/sh\necho "pkexec $*" >> "$SUDO_LOG"\nexit 0\n',
        "systemctl": '#!/bin/sh\necho "systemctl $*" >> "$SYSTEMCTL_LOG"\nexit 0\n',
        "secret-tool": (
            "#!/bin/sh\n"
            'echo "secret-tool $*" >> "$CALL_LOG"\n'
            'if [ "$1" = "search" ]; then\n'
            "  printf 'attribute.username = local\\nattribute.username = work\\n' >&2\n"
            "fi\n"
            "exit 0\n"
        ),
    }
    for name, content in scripts.items():
        path = directory / name
        path.write_text(content, encoding="utf-8")
        path.chmod(path.stat().st_mode | stat.S_IXUSR)
    return directory


@pytest.fixture
def sandbox(tmp_path: Path, release_key: ReleaseKey) -> dict[str, Any]:
    home = tmp_path / "home"
    home.mkdir()
    fake_bin = fake_commands(tmp_path)
    env = {
        "HOME": str(home),
        "XDG_CONFIG_HOME": str(tmp_path / "config"),
        "XDG_CACHE_HOME": str(tmp_path / "cache"),
        "XDG_STATE_HOME": str(tmp_path / "state"),
        "XDG_DATA_HOME": str(tmp_path / "data"),
        "XDG_RUNTIME_DIR": str(tmp_path / "runtime"),
        "PATH": f"{fake_bin}:{os.environ['PATH']}",
        "CALL_LOG": str(tmp_path / "calls.log"),
        "SUDO_LOG": str(tmp_path / "sudo.log"),
        "SYSTEMCTL_LOG": str(tmp_path / "systemctl.log"),
        # Production pins the committed release key; tests pin a throwaway key.
        "KITSUNE_RELEASE_PUBLIC_KEY": str(release_key.public),
    }
    (tmp_path / "runtime").mkdir()
    return {"home": home, "env": env, "tmp": tmp_path, "release_key": release_key}


def merged_env(sandbox: dict[str, Any], env: dict[str, str] | None) -> dict[str, str]:
    merged = dict(sandbox["env"])  # type: ignore[arg-type]
    if env:
        merged.update(env)
    return merged


def run_installer(sandbox: dict[str, Any], *args: str, env: dict[str, str] | None = None):
    return subprocess.run(
        ["bash", str(INSTALL_SH), *args],
        env=merged_env(sandbox, env),
        capture_output=True,
        text=True,
        timeout=60,
    )


def run_piped(sandbox: dict[str, Any], *args: str, env: dict[str, str] | None = None):
    """Run the script the way `curl ... | bash` does: source on stdin."""
    return subprocess.run(
        ["bash", "-s", "--", *args],
        input=INSTALL_SH.read_text(encoding="utf-8"),
        env=merged_env(sandbox, env),
        capture_output=True,
        text=True,
        timeout=60,
    )


def read_log(sandbox: dict[str, Any], name: str) -> str:
    path = Path(str(sandbox["tmp"])) / name
    return path.read_text(encoding="utf-8") if path.exists() else ""


def client_binary(sandbox: dict[str, Any]) -> Path:
    return Path(str(sandbox["home"])) / ".local" / "bin" / "kitsune-client"


def server_launcher(sandbox: dict[str, Any]) -> Path:
    return Path(str(sandbox["home"])) / ".local" / "bin" / "kitsune-server"


def config_file(sandbox: dict[str, Any]) -> Path:
    return Path(str(sandbox["env"]["XDG_CONFIG_HOME"])) / "kitsune-whisper" / "kitsune.yaml"


def manifest_file(sandbox: dict[str, Any]) -> Path:
    return Path(str(sandbox["env"]["XDG_STATE_HOME"])) / "kitsune-whisper" / "install.json"


def read_manifest(sandbox: dict[str, Any]) -> dict[str, Any]:
    return json.loads(manifest_file(sandbox).read_text(encoding="utf-8"))


def assert_nothing_installed(sandbox: dict[str, Any]) -> None:
    home = Path(str(sandbox["home"]))
    assert not (home / ".local").exists()
    assert not manifest_file(sandbox).exists()
    assert not config_file(sandbox).exists()


def assert_nothing_privileged_ran(sandbox: dict[str, Any]) -> None:
    assert read_log(sandbox, "sudo.log") == ""
    assert read_log(sandbox, "systemctl.log") == ""
    calls = read_log(sandbox, "calls.log")
    assert "install-autostart" not in calls
    assert "uninstall-autostart" not in calls


def assert_no_helpers_installed(sandbox: dict[str, Any]) -> None:
    home = Path(str(sandbox["home"]))
    for name in ("ydotool", "ydotoold", "wtype"):
        assert not (home / ".local" / "bin" / name).exists()
    assert not (home / ".local" / "lib" / "kitsune-whisper" / "wayland").exists()
    config_root = Path(str(sandbox["env"]["XDG_CONFIG_HOME"]))
    assert not (config_root / "systemd" / "user" / "kitsune-ydotoold.service").exists()


# -- the script itself -----------------------------------------------------


def test_installer_is_valid_bash() -> None:
    completed = subprocess.run(["bash", "-n", str(INSTALL_SH)], capture_output=True, text=True)
    assert completed.returncode == 0, completed.stderr


def test_help_lists_every_remaining_flag_and_no_retired_one() -> None:
    completed = subprocess.run(["bash", str(INSTALL_SH), "--help"], capture_output=True, text=True)

    assert completed.returncode == 0, completed.stderr
    for flag in HELP_FLAGS:
        assert flag in completed.stdout, flag
    for flag in RETIRED_FLAGS:
        assert flag not in completed.stdout, flag


def test_help_works_when_the_script_is_piped() -> None:
    completed = subprocess.run(
        ["bash", "-s", "--", "--help"],
        input=INSTALL_SH.read_text(encoding="utf-8"),
        capture_output=True,
        text=True,
        timeout=60,
    )

    assert completed.returncode == 0, completed.stderr
    assert "Usage" in completed.stdout
    for flag in HELP_FLAGS:
        assert flag in completed.stdout, flag
    assert "cannot read" not in completed.stderr.lower()
    assert "sed:" not in completed.stderr.lower()


@pytest.mark.parametrize("flag", RETIRED_FLAGS)
def test_retired_flags_are_rejected(sandbox, flag):
    completed = run_installer(sandbox, flag)

    assert completed.returncode != 0
    assert flag in completed.stderr
    assert not (Path(str(sandbox["home"])) / ".local").exists()


def test_the_script_names_no_privileged_or_helper_step() -> None:
    source = INSTALL_SH.read_text(encoding="utf-8")
    body = "\n".join(line for line in source.splitlines() if not line.startswith("#!"))
    for forbidden in (
        "sudo",
        "pkexec",
        "/etc/",
        "/usr/",
        "/opt/",
        "modules-load",
        "udev",
        "ydotool",
        "ydotoold",
        "wtype",
        "kitsune-wayland",
        "install-autostart",
        "systemctl",
    ):
        assert forbidden not in body, forbidden


# -- install layouts -------------------------------------------------------


def test_client_only_installs_the_client_and_stops_there(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"])

    completed = run_installer(sandbox, "--client-only", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    binary = client_binary(sandbox)
    assert binary.is_file()
    assert binary.stat().st_mode & stat.S_IXUSR
    assert config_file(sandbox).is_file()
    calls = read_log(sandbox, "calls.log")
    assert "client: migrate-config" in calls
    assert "client: install-desktop" in calls
    assert "bootstrap.py" not in calls
    assert not server_launcher(sandbox).exists()
    assert_nothing_privileged_ran(sandbox)
    assert_no_helpers_installed(sandbox)


def test_server_only_installs_the_server_without_starting_it(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"], with_server=True)

    completed = run_installer(sandbox, "--server-only", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    calls = read_log(sandbox, "calls.log")
    assert "bootstrap.py install --no-start" in calls
    assert "--artifact" in calls
    assert "kitsune-server_linux_amd64.tar.gz" in calls
    assert "--sha256" in calls
    assert "client:" not in calls
    assert not client_binary(sandbox).exists()
    assert not config_file(sandbox).exists()
    assert_nothing_privileged_ran(sandbox)
    assert_no_helpers_installed(sandbox)


def test_both_components_install_by_default(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"], with_server=True)

    completed = run_installer(sandbox, "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert client_binary(sandbox).is_file()
    assert config_file(sandbox).is_file()
    calls = read_log(sandbox, "calls.log")
    assert "client: install-desktop" in calls
    assert "bootstrap.py install --no-start" in calls
    assert_nothing_privileged_ran(sandbox)
    assert_no_helpers_installed(sandbox)


def test_client_only_and_server_only_are_mutually_exclusive(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"], with_server=True)

    completed = run_installer(sandbox, "--client-only", "--server-only", "--from", str(artifacts))

    assert completed.returncode != 0
    assert not client_binary(sandbox).exists()


def test_a_failed_server_install_is_reported(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"], with_server=True)

    completed = run_installer(
        sandbox,
        "--server-only",
        "--from",
        str(artifacts),
        env={"FAKE_SERVER_EXIT": "1"},
    )

    assert completed.returncode != 0


def test_reinstall_keeps_an_existing_configuration(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"])
    config = config_file(sandbox)
    config.parent.mkdir(parents=True)
    config.write_text("client:\n  hotkey: Alt+Space\n", encoding="utf-8")

    completed = run_installer(sandbox, "--client-only", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert "Alt+Space" in config.read_text(encoding="utf-8")


def test_force_config_overwrites_an_existing_configuration(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"])
    config = config_file(sandbox)
    config.parent.mkdir(parents=True)
    config.write_text("client:\n  hotkey: Alt+Space\n", encoding="utf-8")

    completed = run_installer(sandbox, "--client-only", "--force-config", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert "Alt+Space" not in config.read_text(encoding="utf-8")


def test_checksum_mismatch_stops_before_installing(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"])
    archive = artifacts / "kitsune-client_linux_amd64.tar.gz"
    archive.write_bytes(archive.read_bytes() + b"tampered")

    completed = run_installer(sandbox, "--client-only", "--from", str(artifacts))

    assert completed.returncode != 0
    assert "checksum" in completed.stderr.lower()
    assert_nothing_installed(sandbox)


# -- release signature -----------------------------------------------------


def test_the_pinned_key_is_the_committed_release_key() -> None:
    source = INSTALL_SH.read_text(encoding="utf-8")
    match = re.search(
        r"-----BEGIN PUBLIC KEY-----.*?-----END PUBLIC KEY-----",
        source,
        re.DOTALL,
    )
    assert match is not None
    assert match.group(0).strip() == RELEASE_PUBLIC_KEY.read_text(encoding="utf-8").strip()


def test_a_valid_signature_allows_the_install(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"])

    completed = run_installer(sandbox, "--client-only", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert "signature" in completed.stdout.lower()
    assert client_binary(sandbox).is_file()


def test_a_modified_checksums_file_aborts_before_installing(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"])
    checksums = artifacts / "checksums.txt"
    checksums.write_text(checksums.read_text(encoding="utf-8") + "\n", encoding="utf-8")

    completed = run_installer(sandbox, "--client-only", "--from", str(artifacts))

    assert completed.returncode != 0
    assert "signature" in completed.stderr.lower()
    assert_nothing_installed(sandbox)


def test_a_missing_signature_aborts_before_installing(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"])
    (artifacts / "checksums.txt.sig").unlink()

    completed = run_installer(sandbox, "--client-only", "--from", str(artifacts))

    assert completed.returncode != 0
    assert ".sig" in completed.stderr
    assert_nothing_installed(sandbox)


def test_an_invalid_signature_aborts_before_installing(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"])
    (artifacts / "checksums.txt.sig").write_bytes(b"not a signature")

    completed = run_installer(sandbox, "--client-only", "--from", str(artifacts))

    assert completed.returncode != 0
    assert "signature" in completed.stderr.lower()
    assert_nothing_installed(sandbox)


def test_a_signature_from_another_key_aborts_before_installing(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"])
    sign_checksums(artifacts, make_release_key(tmp_path / "other-key"))

    completed = run_installer(sandbox, "--client-only", "--from", str(artifacts))

    assert completed.returncode != 0
    assert "signature" in completed.stderr.lower()
    assert_nothing_installed(sandbox)


def test_insecure_skips_only_the_signature_check(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"])
    (artifacts / "checksums.txt.sig").unlink()

    completed = run_installer(sandbox, "--client-only", "--insecure", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert "insecure" in completed.stderr.lower()
    assert client_binary(sandbox).is_file()

    tampered = make_artifacts(tmp_path, sandbox["release_key"], name="tampered")
    archive = tampered / "kitsune-client_linux_amd64.tar.gz"
    archive.write_bytes(archive.read_bytes() + b"tampered")
    rejected = run_installer(sandbox, "--client-only", "--insecure", "--from", str(tampered))

    assert rejected.returncode != 0
    assert "checksum" in rejected.stderr.lower()


# -- manifest, version pinning, and the downgrade guard --------------------


def test_manifest_records_version_components_and_timestamp(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"], with_server=True, version="1.2.3")

    completed = run_installer(sandbox, "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    manifest = read_manifest(sandbox)
    assert manifest["version"] == "1.2.3"
    assert manifest["components"] == ["client", "server"]
    timestamp = manifest["installed_at"]
    assert re.fullmatch(r"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}Z", timestamp)
    datetime.strptime(timestamp, "%Y-%m-%dT%H:%M:%SZ")
    assert str(manifest_file(sandbox)) in completed.stdout


def test_version_flag_pins_the_manifest_version(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"], version="1.0.0")

    completed = run_installer(
        sandbox, "--client-only", "--version", "2.0.0", "--from", str(artifacts)
    )

    assert completed.returncode == 0, completed.stderr
    assert read_manifest(sandbox)["version"] == "2.0.0"


def test_reinstall_upgrades_in_place_and_keeps_configuration(sandbox, tmp_path):
    older = make_artifacts(tmp_path, sandbox["release_key"], version="1.0.0", name="older")
    assert run_installer(sandbox, "--client-only", "--from", str(older)).returncode == 0
    config = config_file(sandbox)
    config.write_text("client:\n  hotkey: Alt+Space\n", encoding="utf-8")

    newer = make_artifacts(tmp_path, sandbox["release_key"], version="1.1.0", name="newer")
    completed = run_installer(sandbox, "--client-only", "--from", str(newer))

    assert completed.returncode == 0, completed.stderr
    assert read_manifest(sandbox)["version"] == "1.1.0"
    assert "Alt+Space" in config.read_text(encoding="utf-8")


def test_reinstalling_the_same_version_is_allowed(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"], version="1.0.0")

    assert run_installer(sandbox, "--client-only", "--from", str(artifacts)).returncode == 0
    completed = run_installer(sandbox, "--client-only", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert read_manifest(sandbox)["version"] == "1.0.0"


def test_downgrade_is_refused_without_force_and_allowed_with_force(sandbox, tmp_path):
    newer = make_artifacts(tmp_path, sandbox["release_key"], version="2.0.0", name="newer")
    assert run_installer(sandbox, "--client-only", "--from", str(newer)).returncode == 0

    older = make_artifacts(tmp_path, sandbox["release_key"], version="1.0.0", name="older")
    refused = run_installer(sandbox, "--client-only", "--from", str(older))

    assert refused.returncode != 0
    assert "downgrade" in refused.stderr.lower()
    assert read_manifest(sandbox)["version"] == "2.0.0"

    forced = run_installer(sandbox, "--client-only", "--force", "--from", str(older))

    assert forced.returncode == 0, forced.stderr
    assert read_manifest(sandbox)["version"] == "1.0.0"


def test_force_does_not_overwrite_configuration(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"], version="1.0.0")
    assert run_installer(sandbox, "--client-only", "--from", str(artifacts)).returncode == 0
    config = config_file(sandbox)
    config.write_text("client:\n  hotkey: Alt+Space\n", encoding="utf-8")

    completed = run_installer(sandbox, "--client-only", "--force", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert "Alt+Space" in config.read_text(encoding="utf-8")


def test_an_unknown_local_version_skips_the_guard_and_says_so(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"], version="1.0.0")
    (artifacts / "VERSION").unlink()

    completed = run_installer(sandbox, "--client-only", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert "cannot determine" in completed.stderr.lower()
    assert read_manifest(sandbox)["version"] == "unknown"


def test_a_local_version_falls_back_to_the_server_bundle_manifest(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"], with_server=True, version="3.2.1")
    (artifacts / "VERSION").unlink()

    completed = run_installer(sandbox, "--server-only", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert read_manifest(sandbox)["version"] == "3.2.1"


# -- check mode ------------------------------------------------------------


def test_check_reports_versions_and_changes_nothing(sandbox, tmp_path):
    available = make_artifacts(tmp_path, sandbox["release_key"], version="1.1.0", name="available")

    before_install = run_installer(sandbox, "--check", "--from", str(available))

    assert before_install.returncode == 0, before_install.stderr
    assert "not installed" in before_install.stdout.lower()
    assert "1.1.0" in before_install.stdout
    assert_nothing_installed(sandbox)

    installed = make_artifacts(tmp_path, sandbox["release_key"], version="1.0.0", name="installed")
    assert run_installer(sandbox, "--client-only", "--from", str(installed)).returncode == 0

    snapshot = tree_snapshot(tmp_path)
    checked = run_installer(sandbox, "--check", "--from", str(available))

    assert checked.returncode == 0, checked.stderr
    assert "1.0.0" in checked.stdout
    assert "1.1.0" in checked.stdout
    assert "client" in checked.stdout
    assert tree_snapshot(tmp_path) == snapshot


def test_check_works_when_the_script_is_piped(sandbox, tmp_path):
    installed = make_artifacts(tmp_path, sandbox["release_key"], version="1.0.0", name="installed")
    assert run_installer(sandbox, "--client-only", "--from", str(installed)).returncode == 0
    available = make_artifacts(tmp_path, sandbox["release_key"], version="1.1.0", name="available")

    completed = run_piped(sandbox, "--check", "--from", str(available))

    assert completed.returncode == 0, completed.stderr
    assert "1.0.0" in completed.stdout
    assert "1.1.0" in completed.stdout


def test_check_resolves_the_latest_release_tag(sandbox):
    fake_bin = Path(str(sandbox["env"]["PATH"]).split(os.pathsep)[0])
    fake_curl = fake_bin / "curl"
    fake_curl.write_text(
        '#!/bin/sh\nprintf \'{"tag_name": "v4.5.6"}\\n\'\n',
        encoding="utf-8",
    )
    fake_curl.chmod(0o755)

    completed = run_installer(sandbox, "--check")

    assert completed.returncode == 0, completed.stderr
    assert "4.5.6" in completed.stdout
    assert_nothing_installed(sandbox)


# -- dry run and piped invocations ----------------------------------------


def test_dry_run_prints_the_plan_and_changes_nothing(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"], with_server=True)

    completed = run_installer(sandbox, "--dry-run", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert "Client" in completed.stdout
    assert "Server" in completed.stdout
    assert not (Path(str(sandbox["home"])) / ".local").exists()
    assert not config_file(sandbox).exists()
    assert read_log(sandbox, "calls.log") == ""


def test_dry_run_works_when_the_script_is_piped(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"], with_server=True)

    completed = run_piped(sandbox, "--dry-run", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert "Client" in completed.stdout
    assert "Server" in completed.stdout
    assert not (Path(str(sandbox["home"])) / ".local").exists()


def test_a_component_can_be_installed_through_a_pipe(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"])

    completed = run_piped(sandbox, "--client-only", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert client_binary(sandbox).is_file()


# -- uninstall and purge ---------------------------------------------------


def test_uninstall_keeps_user_data_and_purge_removes_it(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"])
    assert run_installer(sandbox, "--client-only", "--from", str(artifacts)).returncode == 0
    home = Path(str(sandbox["home"]))
    config = Path(str(sandbox["env"]["XDG_CONFIG_HOME"])) / "kitsune-whisper"
    cache = Path(str(sandbox["env"]["XDG_CACHE_HOME"])) / "kitsune-whisper"
    state = Path(str(sandbox["env"]["XDG_STATE_HOME"])) / "kitsune-whisper"
    webview = Path(str(sandbox["env"]["XDG_DATA_HOME"])) / "io.github.FoxRed-cmd.kitsune-whisper"
    for directory in (config, cache, state, webview):
        directory.mkdir(parents=True, exist_ok=True)
        (directory / "kept").write_text("x", encoding="utf-8")

    uninstalled = run_installer(sandbox, "--uninstall")
    assert uninstalled.returncode == 0, uninstalled.stderr
    assert not (home / ".local" / "bin" / "kitsune-client").exists()
    assert (config / "kept").is_file()
    assert "client: uninstall-desktop" in read_log(sandbox, "calls.log")
    assert_nothing_privileged_ran(sandbox)

    assert run_installer(sandbox, "--client-only", "--from", str(artifacts)).returncode == 0
    purged = run_installer(sandbox, "--uninstall", "--purge")
    assert purged.returncode == 0, purged.stderr
    assert not config.exists()
    assert not cache.exists()
    assert not state.exists()
    assert not webview.exists()
    assert_nothing_privileged_ran(sandbox)
    calls = read_log(sandbox, "calls.log")
    assert "secret-tool clear service kitsune-whisper username local" in calls
    assert "secret-tool clear service kitsune-whisper username work" in calls


def test_remove_models_and_server_data_are_separate_choices(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, sandbox["release_key"])
    assert run_installer(sandbox, "--client-only", "--from", str(artifacts)).returncode == 0
    config = Path(str(sandbox["env"]["XDG_CONFIG_HOME"])) / "kitsune-whisper"
    cache = Path(str(sandbox["env"]["XDG_CACHE_HOME"])) / "kitsune-whisper"
    state = Path(str(sandbox["env"]["XDG_STATE_HOME"])) / "kitsune-whisper"
    config.mkdir(parents=True, exist_ok=True)
    (config / "kitsune.yaml").write_text("client: {}\n", encoding="utf-8")
    (cache / "models").mkdir(parents=True, exist_ok=True)
    (cache / "models" / "small.bin").write_text("model", encoding="utf-8")
    (cache / "spool").mkdir(parents=True, exist_ok=True)
    (cache / "spool" / "failed.wav").write_text("audio", encoding="utf-8")
    (state / "tls").mkdir(parents=True, exist_ok=True)
    (state / "tls" / "server.crt").write_text("cert", encoding="utf-8")
    (state / "devices.json").write_text("{}", encoding="utf-8")

    removed = run_installer(sandbox, "--uninstall", "--remove-models")
    assert removed.returncode == 0, removed.stderr
    assert not (cache / "models").exists()
    assert (cache / "spool" / "failed.wav").is_file()
    assert (state / "devices.json").is_file()
    assert (config / "kitsune.yaml").is_file()

    assert run_installer(sandbox, "--client-only", "--from", str(artifacts)).returncode == 0
    (cache / "models").mkdir(parents=True, exist_ok=True)
    (cache / "models" / "small.bin").write_text("model", encoding="utf-8")
    (state / "devices.json").write_text("{}", encoding="utf-8")
    removed = run_installer(sandbox, "--uninstall", "--remove-server-data")
    assert removed.returncode == 0, removed.stderr
    assert not state.exists()
    assert (cache / "models" / "small.bin").is_file()
    assert (config / "kitsune.yaml").is_file()
    assert_nothing_privileged_ran(sandbox)


# -- release pipeline ------------------------------------------------------


def test_release_workflow_has_no_wayland_helper_job() -> None:
    import yaml

    workflow_path = REPO_ROOT / ".github" / "workflows" / "release.yml"
    workflow = yaml.safe_load(workflow_path.read_text(encoding="utf-8"))
    assert "wayland-tools" not in workflow["jobs"]

    text = workflow_path.read_text(encoding="utf-8")
    for retired in (
        "build-wayland-tools.sh",
        "kitsune-wayland",
        "--no-wayland",
        "ydotool",
        "ydotoold",
        "wtype",
    ):
        assert retired not in text, retired

    # The publish job still ships both installers and their signature.
    assert "dist/install.sh" in text
    assert "dist/install.ps1" in text
    assert "checksums.txt.sig" in text


def test_the_wayland_builder_is_deleted() -> None:
    assert not WAYLAND_BUILDER.exists()


def test_the_linux_artifact_builder_has_no_wayland_surface() -> None:
    script = REPO_ROOT / "scripts" / "build-linux-artifacts.sh"
    completed = subprocess.run(["bash", "-n", str(script)], capture_output=True, text=True)
    assert completed.returncode == 0, completed.stderr

    help_output = subprocess.run(["bash", str(script), "--help"], capture_output=True, text=True)
    assert help_output.returncode == 0, help_output.stderr
    source = script.read_text(encoding="utf-8")
    for retired in (
        "--no-wayland",
        "--wayland-dir",
        "build_wayland",
        "kitsune-wayland",
        "ydotool",
        "ydotoold",
        "wtype",
    ):
        assert retired not in source, retired
        assert retired not in help_output.stdout, retired
    assert "--no-client" in source
    assert "--no-server" in source
