"""Behaviour of the Linux release installer, exercised in a sandboxed HOME.

The installer is a public seam: its contract is the files it writes, the
commands it runs, and what it prints. These tests put a temporary HOME, a
temporary XDG tree and a fake PATH in front of it, so no real privilege
escalation, service manager, package manager, or release download is touched.
"""

from __future__ import annotations

import hashlib
import io
import os
import shutil
import stat
import subprocess
import tarfile
from pathlib import Path
from typing import Any

import pytest

REPO_ROOT = Path(__file__).resolve().parents[2]
INSTALL_SH = REPO_ROOT / "install.sh"
WAYLAND_BUILDER = REPO_ROOT / "scripts" / "build-wayland-tools.sh"

HELP_FLAGS = (
    "--client-only",
    "--server-only",
    "--from",
    "--force-config",
    "--dry-run",
    "--uninstall",
    "--purge",
    "--remove-models",
    "--remove-server-data",
    "--help",
)

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
    return '#!/bin/sh\necho "server: $*" >> "$CALL_LOG"\nexit ${FAKE_SERVER_EXIT:-0}\n'


def make_artifacts(tmp_path: Path, *, with_server: bool = False) -> Path:
    """Build the artifact set the installer is allowed to consume.

    There is deliberately no Wayland helper archive: a request for one would
    fail the install, so a passing install proves none is fetched.
    """
    artifacts = tmp_path / "artifacts"
    artifacts.mkdir()
    template = "client:\n  hotkey: Ctrl+Shift+Space\n"
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
                "bundle.json": ('{"version": "0.1.0"}\n', 0o644),
                "lib/kitsune_server/__init__.py": ("", 0o644),
                "runtime/bin/python3": (server_runtime_script(), 0o755),
            },
        )
    checksums = []
    for archive in sorted(artifacts.iterdir()):
        checksums.append(f"{digest(archive)}  {archive.name}")
    (artifacts / "checksums.txt").write_text("\n".join(checksums) + "\n", encoding="utf-8")
    return artifacts


def fake_commands(tmp_path: Path) -> Path:
    """Commands that must never run are logged instead of executed."""
    directory = tmp_path / "fake-bin"
    directory.mkdir()
    scripts = {
        "sudo": '#!/bin/sh\necho "sudo $*" >> "$SUDO_LOG"\nexit 0\n',
        "pkexec": '#!/bin/sh\necho "pkexec $*" >> "$SUDO_LOG"\nexit 0\n',
        "systemctl": '#!/bin/sh\necho "systemctl $*" >> "$SYSTEMCTL_LOG"\nexit 0\n',
        "ldconfig": '#!/bin/sh\nprintf "%s\\n" "${FAKE_LDCONFIG:-}"\nexit 0\n',
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
def sandbox(tmp_path: Path) -> dict[str, Any]:
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
    }
    (tmp_path / "runtime").mkdir()
    return {"home": home, "env": env, "tmp": tmp_path, "fake_bin": fake_bin}


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


def assert_nothing_privileged_ran(sandbox: dict[str, Any]) -> None:
    assert read_log(sandbox, "sudo.log") == ""
    assert read_log(sandbox, "systemctl.log") == ""
    calls = read_log(sandbox, "calls.log")
    assert "client: install-autostart" not in calls


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
    # Uninstall legitimately names the Client's own `uninstall-autostart`
    # command; installing autostart is still forbidden.
    body = body.replace("uninstall-autostart", "")
    for forbidden in (
        "sudo",
        "pkexec",
        "/etc/",
        "/usr/",
        "/opt/",
        "modules-load",
        "udev",
        "ydotoold",
        "kitsune-wayland",
        "install-autostart",
        "systemctl",
    ):
        assert forbidden not in body, forbidden


# -- install layouts -------------------------------------------------------


def test_client_only_installs_the_client_and_stops_there(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)

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
    artifacts = make_artifacts(tmp_path, with_server=True)

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
    artifacts = make_artifacts(tmp_path, with_server=True)

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
    artifacts = make_artifacts(tmp_path, with_server=True)

    completed = run_installer(sandbox, "--client-only", "--server-only", "--from", str(artifacts))

    assert completed.returncode != 0
    assert not client_binary(sandbox).exists()


def test_a_failed_server_install_is_reported(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, with_server=True)

    completed = run_installer(
        sandbox,
        "--server-only",
        "--from",
        str(artifacts),
        env={"FAKE_SERVER_EXIT": "1"},
    )

    assert completed.returncode != 0


def test_reinstall_keeps_an_existing_configuration(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    config = config_file(sandbox)
    config.parent.mkdir(parents=True)
    config.write_text("client:\n  hotkey: Alt+Space\n", encoding="utf-8")

    completed = run_installer(sandbox, "--client-only", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert "Alt+Space" in config.read_text(encoding="utf-8")


def test_force_config_overwrites_an_existing_configuration(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    config = config_file(sandbox)
    config.parent.mkdir(parents=True)
    config.write_text("client:\n  hotkey: Alt+Space\n", encoding="utf-8")

    completed = run_installer(sandbox, "--client-only", "--force-config", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert "Alt+Space" not in config.read_text(encoding="utf-8")


def test_checksum_mismatch_stops_before_installing(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    archive = artifacts / "kitsune-client_linux_amd64.tar.gz"
    archive.write_bytes(archive.read_bytes() + b"tampered")

    completed = run_installer(sandbox, "--client-only", "--from", str(artifacts))

    assert completed.returncode != 0
    assert "checksum" in completed.stderr.lower()
    assert not client_binary(sandbox).exists()


# -- dry run and piped invocations ----------------------------------------


def test_dry_run_prints_the_plan_and_changes_nothing(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, with_server=True)

    completed = run_installer(sandbox, "--dry-run", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert "Client" in completed.stdout
    assert "Server" in completed.stdout
    assert not (Path(str(sandbox["home"])) / ".local").exists()
    assert not config_file(sandbox).exists()
    assert read_log(sandbox, "calls.log") == ""


def test_dry_run_works_when_the_script_is_piped(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, with_server=True)

    completed = run_piped(sandbox, "--dry-run", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert "Client" in completed.stdout
    assert "Server" in completed.stdout
    assert not (Path(str(sandbox["home"])) / ".local").exists()


def test_a_component_can_be_installed_through_a_pipe(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)

    completed = run_piped(sandbox, "--client-only", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    assert client_binary(sandbox).is_file()


# -- uninstall and purge ---------------------------------------------------


def test_uninstall_keeps_user_data_and_purge_removes_it(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
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
    artifacts = make_artifacts(tmp_path)
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


def xdg_dir(sandbox: dict[str, Any], name: str) -> Path:
    return Path(str(sandbox["env"][name]))


def removal_paths(sandbox: dict[str, Any]) -> dict[str, Path]:
    """Every path either removal mode may touch, or must deliberately keep."""
    home = Path(str(sandbox["home"]))
    data = xdg_dir(sandbox, "XDG_DATA_HOME")
    config = xdg_dir(sandbox, "XDG_CONFIG_HOME")
    cache = xdg_dir(sandbox, "XDG_CACHE_HOME")
    state = xdg_dir(sandbox, "XDG_STATE_HOME")
    runtime = xdg_dir(sandbox, "XDG_RUNTIME_DIR")
    app_id = "io.github.FoxRed-cmd.kitsune-whisper"
    return {
        "client": home / ".local" / "bin" / "kitsune-client",
        "launcher": home / ".local" / "bin" / "kitsune-server",
        "bin_dir": home / ".local" / "bin",
        "local_lib": home / ".local" / "lib" / "kitsune-whisper",
        "server_root": home / ".local" / "lib" / "kitsune-whisper" / "server",
        "config_dir": config / "kitsune-whisper",
        "config_root": config,
        "cache_dir": cache / "kitsune-whisper",
        "state_dir": state / "kitsune-whisper",
        "app_data": data / app_id,
        "client_unit": config / "systemd" / "user" / "kitsune-client.service",
        "server_unit": config / "systemd" / "user" / "kitsune-server.service",
        "server_task": config / "kitsune-whisper" / "kitsune-server-task.xml",
        "menu": data / "applications" / "kitsune-whisper.desktop",
        "portal": data / "applications" / f"{app_id}.desktop",
        "icon_svg": data / "icons" / "hicolor" / "scalable" / "apps" / f"{app_id}.svg",
        "icon_png": data / "icons" / "hicolor" / "256x256" / "apps" / f"{app_id}.png",
        "runtime_socket": runtime / "kitsune-whisper.sock",
        "runtime_lock": runtime / "kitsune-whisper.lock",
    }


def seed_component_files(sandbox: dict[str, Any]) -> dict[str, Path]:
    """Write what a real Client and Server install leave behind.

    The fake artifacts only exercise the installer's commands, so the files the
    real Client binary and bundle launcher would create are written here.
    """
    paths = removal_paths(sandbox)
    paths["launcher"].write_text(
        '#!/bin/sh\necho "server: $*" >> "$CALL_LOG"\nexit 0\n', encoding="utf-8"
    )
    paths["launcher"].chmod(0o755)
    paths["server_root"].mkdir(parents=True, exist_ok=True)
    (paths["server_root"] / "bundle.json").write_text("{}\n", encoding="utf-8")
    for key in ("client_unit", "server_unit", "menu", "portal"):
        paths[key].parent.mkdir(parents=True, exist_ok=True)
        paths[key].write_text("[Unit]\n", encoding="utf-8")
    for key in ("icon_svg", "icon_png"):
        paths[key].parent.mkdir(parents=True, exist_ok=True)
        paths[key].write_text("icon", encoding="utf-8")
    return paths


def seed_user_data(paths: dict[str, Path]) -> dict[str, Path]:
    files = {
        "config": paths["config_dir"] / "kitsune.yaml",
        "tokens": paths["config_dir"] / "tokens" / "local.json",
        "wizard": paths["config_dir"] / "wizard.json",
        "model": paths["cache_dir"] / "models" / "small.bin",
        "spool": paths["cache_dir"] / "spool" / "failed.wav",
        "client_log": paths["cache_dir"] / "client.log",
        "certificate": paths["state_dir"] / "tls" / "server.crt",
        "devices": paths["state_dir"] / "devices.json",
        "server_log": paths["state_dir"] / "server.log",
        "webview": paths["app_data"] / "Cache" / "data",
    }
    for path in files.values():
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text("x", encoding="utf-8")
    return files


def install_both_components(sandbox: dict[str, Any], tmp_path: Path) -> None:
    artifacts = make_artifacts(tmp_path, with_server=True)
    completed = run_installer(sandbox, "--from", str(artifacts))
    assert completed.returncode == 0, completed.stderr


def test_uninstall_removes_our_files_and_keeps_user_data(sandbox, tmp_path):
    install_both_components(sandbox, tmp_path)
    paths = seed_component_files(sandbox)
    kept = seed_user_data(paths)

    completed = run_installer(sandbox, "--uninstall")

    assert completed.returncode == 0, completed.stderr
    for name in (
        "client",
        "launcher",
        "server_root",
        "client_unit",
        "server_unit",
        "menu",
        "portal",
        "icon_svg",
        "icon_png",
    ):
        assert not paths[name].exists(), name
    for name, path in kept.items():
        assert path.is_file(), name
    calls = read_log(sandbox, "calls.log")
    assert "client: uninstall-autostart" in calls
    assert "client: uninstall-desktop" in calls
    assert "server: uninstall" in calls
    assert "secret-tool" not in calls
    assert_nothing_privileged_ran(sandbox)


def test_purge_removes_every_trace_and_leaves_other_files(sandbox, tmp_path):
    install_both_components(sandbox, tmp_path)
    paths = seed_component_files(sandbox)
    seed_user_data(paths)
    for key in ("runtime_socket", "runtime_lock"):
        paths[key].write_text("x", encoding="utf-8")

    other_tool = paths["bin_dir"] / "other-tool"
    other_tool.write_text("x", encoding="utf-8")
    other_config = paths["config_root"] / "other-app" / "settings"
    other_config.parent.mkdir(parents=True, exist_ok=True)
    other_config.write_text("x", encoding="utf-8")
    other_unit = paths["config_root"] / "systemd" / "user" / "other.service"
    other_unit.write_text("x", encoding="utf-8")
    other_data = paths["app_data"].parent / "other-app" / "file"
    other_data.parent.mkdir(parents=True, exist_ok=True)
    other_data.write_text("x", encoding="utf-8")

    completed = run_installer(sandbox, "--uninstall", "--purge")

    assert completed.returncode == 0, completed.stderr
    for name in (
        "client",
        "launcher",
        "local_lib",
        "config_dir",
        "cache_dir",
        "state_dir",
        "app_data",
        "client_unit",
        "server_unit",
        "menu",
        "portal",
        "icon_svg",
        "icon_png",
        "runtime_socket",
        "runtime_lock",
    ):
        assert not paths[name].exists(), name
    for path in (other_tool, other_config, other_unit, other_data):
        assert path.is_file(), path
    assert paths["bin_dir"].is_dir()
    calls = read_log(sandbox, "calls.log")
    assert "secret-tool clear service kitsune-whisper username local" in calls
    assert "secret-tool clear service kitsune-whisper username work" in calls
    assert_nothing_privileged_ran(sandbox)


def test_uninstall_falls_back_to_registration_files_without_the_commands(sandbox, tmp_path):
    paths = removal_paths(sandbox)
    for key in (
        "client_unit",
        "server_unit",
        "server_task",
        "menu",
        "portal",
        "icon_svg",
        "icon_png",
    ):
        paths[key].parent.mkdir(parents=True, exist_ok=True)
        paths[key].write_text("x", encoding="utf-8")
    paths["server_root"].mkdir(parents=True, exist_ok=True)
    (paths["server_root"] / "bundle.json").write_text("{}\n", encoding="utf-8")

    completed = run_installer(sandbox, "--uninstall")

    assert completed.returncode == 0, completed.stderr
    for key in (
        "server_root",
        "client_unit",
        "server_unit",
        "server_task",
        "menu",
        "portal",
        "icon_svg",
        "icon_png",
    ):
        assert not paths[key].exists(), key
    assert read_log(sandbox, "calls.log") == ""
    assert_nothing_privileged_ran(sandbox)


def test_removal_is_idempotent(sandbox, tmp_path):
    install_both_components(sandbox, tmp_path)
    seed_component_files(sandbox)

    assert run_installer(sandbox, "--uninstall").returncode == 0
    assert run_installer(sandbox, "--uninstall").returncode == 0
    assert run_installer(sandbox, "--uninstall", "--purge").returncode == 0
    assert run_installer(sandbox, "--uninstall", "--purge").returncode == 0


def test_removal_calls_no_package_manager_and_no_system_tool(sandbox, tmp_path):
    install_both_components(sandbox, tmp_path)
    seed_component_files(sandbox)
    log = tmp_path / "system-commands.log"
    fake_bin = Path(str(sandbox["env"]["PATH"]).split(":")[0])
    for name in ("apt-get", "dnf", "pacman", "zypper", "apk", "udevadm", "modprobe"):
        script = fake_bin / name
        script.write_text(f'#!/bin/sh\necho "{name} $*" >> "{log}"\nexit 0\n', encoding="utf-8")
        script.chmod(0o755)

    assert run_installer(sandbox, "--uninstall").returncode == 0
    assert run_installer(sandbox, "--uninstall", "--purge").returncode == 0

    assert not log.exists() or log.read_text(encoding="utf-8") == ""
    assert read_log(sandbox, "sudo.log") == ""
    assert read_log(sandbox, "systemctl.log") == ""


def test_piped_uninstall_keeps_data_unless_purge_is_explicit(sandbox, tmp_path):
    install_both_components(sandbox, tmp_path)
    paths = seed_component_files(sandbox)
    kept = seed_user_data(paths)

    completed = run_piped(sandbox, "--uninstall")

    assert completed.returncode == 0, completed.stderr
    for name, path in kept.items():
        assert path.is_file(), name

    completed = run_piped(sandbox, "--uninstall", "--purge")

    assert completed.returncode == 0, completed.stderr
    for name in ("config_dir", "cache_dir", "state_dir", "app_data"):
        assert not paths[name].exists(), name


# -- release pipeline ------------------------------------------------------


def test_release_workflow_has_no_wayland_helper_job() -> None:
    import yaml

    workflow_path = REPO_ROOT / ".github" / "workflows" / "release.yml"
    workflow = yaml.safe_load(workflow_path.read_text(encoding="utf-8"))
    assert "wayland-tools" not in workflow["jobs"]
    assert "windows-installer" not in workflow["jobs"]
    assert set(workflow["jobs"]) == {
        "verify-version",
        "client-linux",
        "client-windows",
        "server-bundle",
        "server-bundle-windows",
        "release",
    }

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

    # The setup executable, its Inno definition, and the job that built it are
    # gone: the Windows installer is the checked-out install.ps1.
    for retired in (
        "kitsune-whisper_windows_amd64_setup.exe",
        "build-" + "installer.ps1",
        "kitsune-whisper" + ".iss",
        "windows-installer",
        "inno" + "setup",
    ):
        assert retired not in text, retired

    for asset in (
        "kitsune-client_linux_amd64.tar.gz",
        "kitsune-client_linux_amd64-legacy.tar.gz",
        "kitsune-client_windows_amd64.zip",
        "kitsune-server_linux_amd64.tar.gz",
        "kitsune-server_windows_amd64.tar.gz",
        "install.sh",
        "install.ps1",
        "checksums.txt",
    ):
        assert asset in text, asset
    assert "sha256sum" in text

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


# -- dependency reporting --------------------------------------------------
#
# The installer checks the runtime surface after the binaries are in place and
# only warns: a missing library never fails an install that placed its files.
# These tests replace ldconfig with a fake listing and pin the glibc version,
# so they do not depend on the machine running the suite.

LIBRARY_LINES = {
    "libgtk-4.so.1": "libgtk-4.so.1 (libc6,x86-64) => /lib/x86_64-linux-gnu/libgtk-4.so.1",
    "libwebkitgtk-6.0.so.4": (
        "libwebkitgtk-6.0.so.4 (libc6,x86-64) => /lib/x86_64-linux-gnu/libwebkitgtk-6.0.so.4"
    ),
    "libasound.so.2": "libasound.so.2 (libc6,x86-64) => /lib/x86_64-linux-gnu/libasound.so.2",
    "libX11.so.6": "libX11.so.6 (libc6,x86-64) => /lib/x86_64-linux-gnu/libX11.so.6",
}

CLIENT_DEPENDENCY_IDS = (
    "gtk4",
    "webkitgtk-6.0",
    "alsa",
    "libx11",
    "glibc-client",
    "wayland-injection",
)

# Enough of the userland for install.sh to run without the real PATH, whose
# contents (a wtype or ydotool installed on the developer machine) would make
# the injection checks ambiguous.
BASE_TOOLS = (
    "awk",
    "bash",
    "cat",
    "chmod",
    "cp",
    "dirname",
    "getconf",
    "grep",
    "gzip",
    "install",
    "ldd",
    "mkdir",
    "mktemp",
    "rm",
    "rmdir",
    "sha256sum",
    "sort",
    "tar",
    "uname",
)


def ldconfig_listing(*missing: str) -> str:
    lines = ["libc.so.6 (libc6,x86-64) => /lib/x86_64-linux-gnu/libc.so.6"]
    for soname, line in LIBRARY_LINES.items():
        if soname not in missing:
            lines.append(line)
    return "\n".join(lines)


def minimal_path(sandbox: dict[str, Any]) -> str:
    directory = Path(str(sandbox["tmp"])) / "min-bin"
    if not directory.exists():
        directory.mkdir()
        for name in BASE_TOOLS:
            found = shutil.which(name)
            if found is not None:
                (directory / name).symlink_to(found)
    return f"{sandbox['fake_bin']}:{directory}"


def dependency_env(
    sandbox: dict[str, Any],
    *,
    glibc: str = "2.39",
    session: str | None = "x11",
    missing: tuple[str, ...] = (),
    extra_path: str | None = None,
) -> dict[str, str]:
    path = minimal_path(sandbox)
    if extra_path is not None:
        path = f"{extra_path}:{path}"
    env = {
        "PATH": path,
        "FAKE_LDCONFIG": ldconfig_listing(*missing),
        "KITSUNE_GLIBC_VERSION": glibc,
    }
    if session is not None:
        env["XDG_SESSION_TYPE"] = session
    return env


def add_injection_tool(sandbox: dict[str, Any], name: str) -> str:
    directory = Path(str(sandbox["tmp"])) / "tool-bin"
    directory.mkdir(exist_ok=True)
    tool = directory / name
    tool.write_text("#!/bin/sh\nexit 0\n", encoding="utf-8")
    tool.chmod(0o755)
    return str(directory)


def assert_warning_names(completed: subprocess.CompletedProcess[str], identifier: str) -> None:
    assert identifier in completed.stderr, completed.stderr
    assert "README" in completed.stderr, completed.stderr


@pytest.mark.parametrize(
    ("args", "expect_client", "expect_server"),
    [
        (("--client-only",), True, False),
        (("--server-only",), False, True),
        ((), True, True),
    ],
)
def test_component_selection_installs_exactly_that_tree(
    sandbox, tmp_path, args, expect_client, expect_server
):
    artifacts = make_artifacts(tmp_path, with_server=True)

    completed = run_installer(sandbox, *args, "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    calls = read_log(sandbox, "calls.log")
    home = Path(str(sandbox["home"]))

    if expect_client:
        assert client_binary(sandbox).is_file()
        assert config_file(sandbox).is_file()
        assert "client: install-desktop" in calls
    else:
        assert not client_binary(sandbox).exists()
        assert not config_file(sandbox).exists()
        assert "client:" not in calls

    if expect_server:
        assert "bootstrap.py install --no-start" in calls
        assert "kitsune-server_linux_amd64.tar.gz" in calls
    else:
        assert "bootstrap.py" not in calls
        assert not (home / ".local" / "lib" / "kitsune-whisper" / "server").exists()
        assert not server_launcher(sandbox).exists()

    assert_nothing_privileged_ran(sandbox)
    assert_no_helpers_installed(sandbox)


@pytest.mark.parametrize(
    ("identifier", "soname"),
    [
        ("gtk4", "libgtk-4.so.1"),
        ("webkitgtk-6.0", "libwebkitgtk-6.0.so.4"),
        ("alsa", "libasound.so.2"),
        ("libx11", "libX11.so.6"),
    ],
)
def test_a_missing_client_library_is_named_in_a_warning(sandbox, tmp_path, identifier, soname):
    artifacts = make_artifacts(tmp_path)

    completed = run_installer(
        sandbox,
        "--client-only",
        "--from",
        str(artifacts),
        env=dependency_env(sandbox, missing=(soname,)),
    )

    assert completed.returncode == 0, completed.stderr
    assert_warning_names(completed, identifier)


def test_an_old_glibc_tells_the_user_only_the_server_can_run(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)

    completed = run_installer(
        sandbox,
        "--client-only",
        "--from",
        str(artifacts),
        env=dependency_env(sandbox, glibc="2.36"),
    )

    assert completed.returncode == 0, completed.stderr
    assert_warning_names(completed, "glibc-client")
    assert "only the server" in completed.stderr.lower(), completed.stderr


def test_a_host_below_both_glibc_floors_does_not_promise_the_server(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)

    completed = run_installer(
        sandbox,
        "--client-only",
        "--from",
        str(artifacts),
        env=dependency_env(sandbox, glibc="2.30"),
    )

    assert completed.returncode == 0, completed.stderr
    assert_warning_names(completed, "glibc-client")
    assert "only the server" not in completed.stderr.lower(), completed.stderr


def test_server_only_reports_server_dependencies_but_not_client_ones(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, with_server=True)

    completed = run_installer(
        sandbox,
        "--server-only",
        "--from",
        str(artifacts),
        env=dependency_env(
            sandbox,
            glibc="2.30",
            session="wayland",
            missing=tuple(LIBRARY_LINES),
        ),
    )

    assert completed.returncode == 0, completed.stderr
    assert_warning_names(completed, "glibc-server")
    for identifier in CLIENT_DEPENDENCY_IDS:
        assert identifier not in completed.stderr, identifier


def test_client_only_does_not_report_the_server_glibc_floor(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)

    completed = run_installer(
        sandbox,
        "--client-only",
        "--from",
        str(artifacts),
        env=dependency_env(sandbox, glibc="2.34"),
    )

    assert completed.returncode == 0, completed.stderr
    assert "glibc-server" not in completed.stderr, completed.stderr


def test_wayland_without_an_injection_tool_warns_about_the_clipboard(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)

    completed = run_installer(
        sandbox,
        "--client-only",
        "--from",
        str(artifacts),
        env=dependency_env(sandbox, session="wayland"),
    )

    assert completed.returncode == 0, completed.stderr
    assert_warning_names(completed, "wayland-injection")
    assert "clipboard" in completed.stderr.lower(), completed.stderr


@pytest.mark.parametrize("tool", ["wtype", "ydotool"])
def test_wayland_with_an_injection_tool_is_not_warned(sandbox, tmp_path, tool):
    artifacts = make_artifacts(tmp_path)
    tool_bin = add_injection_tool(sandbox, tool)

    completed = run_installer(
        sandbox,
        "--client-only",
        "--from",
        str(artifacts),
        env=dependency_env(sandbox, session="wayland", extra_path=tool_bin),
    )

    assert completed.returncode == 0, completed.stderr
    assert "wayland-injection" not in completed.stderr, completed.stderr


def test_x11_needs_no_external_injection_tool(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)

    completed = run_installer(
        sandbox,
        "--client-only",
        "--from",
        str(artifacts),
        env=dependency_env(sandbox, session="x11"),
    )

    assert completed.returncode == 0, completed.stderr
    assert "wayland-injection" not in completed.stderr, completed.stderr


def test_warnings_never_print_a_package_manager_command(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, with_server=True)

    completed = run_installer(
        sandbox,
        "--from",
        str(artifacts),
        env=dependency_env(
            sandbox,
            glibc="2.30",
            session="wayland",
            missing=tuple(LIBRARY_LINES),
        ),
    )

    assert completed.returncode == 0, completed.stderr
    output = (completed.stdout + completed.stderr).lower()
    for forbidden in (
        "sudo",
        "apt-get",
        "apt install",
        "dnf install",
        "pacman",
        "zypper",
        "pip install",
        "libgtk-4-1",
        "libasound2",
        "libwebkit2gtk",
    ):
        assert forbidden not in output, forbidden


def test_dependency_warnings_do_not_change_the_exit_status(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, with_server=True)

    completed = run_installer(
        sandbox,
        "--from",
        str(artifacts),
        env=dependency_env(
            sandbox,
            glibc="2.30",
            session="wayland",
            missing=tuple(LIBRARY_LINES),
        ),
    )

    assert completed.returncode == 0, completed.stderr
    assert client_binary(sandbox).is_file()
    assert "bootstrap.py install --no-start" in read_log(sandbox, "calls.log")
    for identifier in (*CLIENT_DEPENDENCY_IDS, "glibc-server"):
        assert identifier in completed.stderr, identifier
