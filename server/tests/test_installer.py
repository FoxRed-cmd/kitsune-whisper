"""Behaviour of the Linux release installer, exercised in a sandboxed HOME.

The installer is a public seam: its contract is the files it writes, the
commands it runs, and what it prints. These tests put a temporary HOME and a
fake PATH in front of it, so no real package manager, sudo, systemd, or
release download is touched.
"""

from __future__ import annotations

import hashlib
import io
import os
import stat
import subprocess
import tarfile
from pathlib import Path
from typing import Any

import pytest

REPO_ROOT = Path(__file__).resolve().parents[2]
INSTALL_SH = REPO_ROOT / "install.sh"

GTK4_LIBS = (
    "libgtk-4.so.1 (libc6,x86-64) => /usr/lib64/libgtk-4.so.1\n"
    "libwebkitgtk-6.0.so.4 (libc6,x86-64) => /usr/lib64/libwebkitgtk-6.0.so.4\n"
)
GTK3_LIBS = (
    "libgtk-3.so.0 (libc6,x86-64) => /usr/lib64/libgtk-3.so.0\n"
    "libwebkit2gtk-4.1.so.0 (libc6,x86-64) => /usr/lib64/libwebkit2gtk-4.1.so.0\n"
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


def client_script(label: str) -> str:
    return f'#!/bin/sh\necho "client:{label} $*" >> "$CALL_LOG"\nexit 0\n'


def server_runtime_script(label: str = "server") -> str:
    return (
        "#!/bin/sh\n"
        f'echo "{label}: $*" >> "$CALL_LOG"\n'
        'echo "KITSUNE_ARTIFACT=${KITSUNE_BOOTSTRAP_ARTIFACT:-}" >> "$CALL_LOG"\n'
        "exit ${FAKE_SERVER_EXIT:-0}\n"
    )


def make_artifacts(tmp_path: Path, *, with_server: bool = False) -> Path:
    artifacts = tmp_path / "artifacts"
    artifacts.mkdir()
    template = "client:\n  hotkey: Ctrl+Shift+Space\n"
    write_tar(
        artifacts / "kitsune-client_linux_amd64.tar.gz",
        {
            "kitsune-client": (client_script("gtk4"), 0o755),
            "kitsune.example.yaml": (template, 0o644),
        },
    )
    write_tar(
        artifacts / "kitsune-client_linux_amd64-legacy.tar.gz",
        {
            "kitsune-client": (client_script("legacy"), 0o755),
            "kitsune.example.yaml": (template, 0o644),
        },
    )
    write_tar(
        artifacts / "kitsune-wayland_linux_amd64.tar.gz",
        {
            "bin/ydotool": (client_script("ydotool"), 0o755),
            "bin/ydotoold": (client_script("ydotoold"), 0o755),
            "bin/wtype": (client_script("wtype"), 0o755),
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
    directory = tmp_path / "fake-bin"
    directory.mkdir()
    scripts = {
        "ldconfig": '#!/bin/sh\ncat "$FAKE_LDCONFIG"\n',
        "sudo": (
            "#!/bin/sh\n"
            'echo "sudo $*" >> "$SUDO_LOG"\n'
            'for last in "$@"; do :; done\n'
            'if [ -f "$last" ]; then cp "$last" "$SUDO_SCRIPT" 2>/dev/null || true; fi\n'
            'exit "${FAKE_SUDO_EXIT:-0}"\n'
        ),
        "systemctl": '#!/bin/sh\necho "systemctl $*" >> "$SYSTEMCTL_LOG"\nexit 0\n',
        "loginctl": '#!/bin/sh\necho "loginctl $*" >> "$SYSTEMCTL_LOG"\nexit 0\n',
        "udevadm": '#!/bin/sh\necho "udevadm $*" >> "$SUDO_LOG"\nexit 0\n',
        "modprobe": '#!/bin/sh\necho "modprobe $*" >> "$SUDO_LOG"\nexit 0\n',
        "apt-get": '#!/bin/sh\necho "apt-get $*" >> "$SUDO_LOG"\nexit 0\n',
        "dnf": '#!/bin/sh\necho "dnf $*" >> "$SUDO_LOG"\nexit 0\n',
        "pacman": '#!/bin/sh\necho "pacman $*" >> "$SUDO_LOG"\nexit 0\n',
        "zypper": '#!/bin/sh\necho "zypper $*" >> "$SUDO_LOG"\nexit 0\n',
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
    libs = tmp_path / "libs"
    libs.write_text(GTK4_LIBS, encoding="utf-8")
    env = {
        "HOME": str(home),
        "XDG_CONFIG_HOME": str(tmp_path / "config"),
        "XDG_CACHE_HOME": str(tmp_path / "cache"),
        "XDG_STATE_HOME": str(tmp_path / "state"),
        "XDG_DATA_HOME": str(tmp_path / "data"),
        "XDG_RUNTIME_DIR": str(tmp_path / "runtime"),
        "PATH": f"{fake_bin}:{os.environ['PATH']}",
        "FAKE_LDCONFIG": str(libs),
        "CALL_LOG": str(tmp_path / "calls.log"),
        "SUDO_LOG": str(tmp_path / "sudo.log"),
        "SUDO_SCRIPT": str(tmp_path / "sudo-script.sh"),
        "SYSTEMCTL_LOG": str(tmp_path / "systemctl.log"),
        "KITSUNE_PACKAGE_MANAGER": "apt",
    }
    (tmp_path / "runtime").mkdir()
    return {"home": home, "env": env, "tmp": tmp_path, "libs": libs}


def run_installer(sandbox: dict[str, Any], *args: str, env: dict[str, str] | None = None):
    merged = dict(sandbox["env"])  # type: ignore[arg-type]
    if env:
        merged.update(env)
    return subprocess.run(
        ["bash", str(INSTALL_SH), *args],
        env=merged,
        capture_output=True,
        text=True,
        timeout=60,
    )


def read_log(sandbox: dict[str, Any], name: str) -> str:
    path = Path(str(sandbox["tmp"])) / name
    return path.read_text(encoding="utf-8") if path.exists() else ""


def install_client_only(sandbox: dict[str, Any], artifacts: Path, **kwargs: Any):
    return run_installer(
        sandbox,
        "--no-server",
        "--no-wayland",
        "--no-elevate",
        "--from",
        str(artifacts),
        **kwargs,
    )


def test_installer_offers_the_optional_cuda_runtime() -> None:
    script = INSTALL_SH.read_text(encoding="utf-8")
    assert "--cuda" in script
    assert "install_cuda_runtime" in script
    assert "nvidia-cublas-cu12" in script
    assert "nvidia-smi" in script
    assert '"$SERVER_ROOT/runtime/bin/python3" -m pip install' in script
    assert '--target "$SERVER_ROOT/lib"' in script
    # A GPU-less host must keep the CPU Server instead of failing the install.
    assert "keeping CPU" in script


def test_installer_is_valid_bash() -> None:
    completed = subprocess.run(["bash", "-n", str(INSTALL_SH)], capture_output=True, text=True)
    assert completed.returncode == 0, completed.stderr


def test_client_install_writes_the_binary_template_and_autostart(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)

    completed = install_client_only(sandbox, artifacts)

    assert completed.returncode == 0, completed.stderr
    home = Path(str(sandbox["home"]))
    binary = home / ".local" / "bin" / "kitsune-client"
    config = Path(str(sandbox["env"]["XDG_CONFIG_HOME"])) / "kitsune-whisper" / "kitsune.yaml"
    assert binary.is_file()
    assert binary.stat().st_mode & stat.S_IXUSR
    assert "client:gtk4" in read_log(sandbox, "calls.log")
    assert config.is_file()
    assert "install-autostart" in read_log(sandbox, "calls.log")
    assert "client:gtk4 migrate-config" in read_log(sandbox, "calls.log")


def test_reinstall_keeps_an_existing_configuration(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    config = Path(str(sandbox["env"]["XDG_CONFIG_HOME"])) / "kitsune-whisper" / "kitsune.yaml"
    config.parent.mkdir(parents=True)
    config.write_text("client:\n  hotkey: Alt+Space\n", encoding="utf-8")

    completed = install_client_only(sandbox, artifacts)

    assert completed.returncode == 0, completed.stderr
    assert "Alt+Space" in config.read_text(encoding="utf-8")


def test_checksum_mismatch_stops_before_installing(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    archive = artifacts / "kitsune-client_linux_amd64.tar.gz"
    archive.write_bytes(archive.read_bytes() + b"tampered")

    completed = install_client_only(sandbox, artifacts)

    assert completed.returncode != 0
    assert "checksum" in completed.stderr.lower()
    assert not (Path(str(sandbox["home"])) / ".local" / "bin" / "kitsune-client").exists()


def test_gtk4_libs_select_the_default_build(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)

    completed = install_client_only(sandbox, artifacts)

    assert completed.returncode == 0, completed.stderr
    binary = Path(str(sandbox["home"])) / ".local" / "bin" / "kitsune-client"
    assert "client:gtk4" in binary.read_text(encoding="utf-8")


def test_gtk3_only_older_lts_hosts_select_the_legacy_build(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    legacy_libs = tmp_path / "gtk3-libs"
    legacy_libs.write_text(GTK3_LIBS, encoding="utf-8")
    os_release = tmp_path / "os-release"
    os_release.write_text('ID=ubuntu\nVERSION_ID="22.04"\n', encoding="utf-8")

    completed = install_client_only(
        sandbox,
        artifacts,
        env={"FAKE_LDCONFIG": str(legacy_libs), "KITSUNE_OS_RELEASE": str(os_release)},
    )

    assert completed.returncode == 0, completed.stderr
    binary = Path(str(sandbox["home"])) / ".local" / "bin" / "kitsune-client"
    assert "client:legacy" in binary.read_text(encoding="utf-8")


def test_a_modern_host_with_only_gtk3_installs_the_default_gtk4_build(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    legacy_libs = tmp_path / "gtk3-libs"
    legacy_libs.write_text(GTK3_LIBS, encoding="utf-8")
    os_release = tmp_path / "os-release"
    os_release.write_text('ID=ubuntu\nVERSION_ID="24.04"\n', encoding="utf-8")

    completed = run_installer(
        sandbox,
        "--no-server",
        "--no-wayland",
        "--from",
        str(artifacts),
        env={
            "FAKE_LDCONFIG": str(legacy_libs),
            "KITSUNE_OS_RELEASE": str(os_release),
            "KITSUNE_PACKAGE_MANAGER": "apt",
        },
    )

    assert completed.returncode == 0, completed.stderr
    binary = Path(str(sandbox["home"])) / ".local" / "bin" / "kitsune-client"
    assert "client:gtk4" in binary.read_text(encoding="utf-8")


def test_a_derivative_on_a_modern_base_gets_the_gtk4_build(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    (tmp_path / "libs").write_text("", encoding="utf-8")
    os_release = tmp_path / "os-release"
    os_release.write_text(
        'ID=neon\nID_LIKE=ubuntu\nVERSION_ID="6.0"\nUBUNTU_CODENAME=noble\n',
        encoding="utf-8",
    )

    completed = install_client_only(
        sandbox,
        artifacts,
        env={"KITSUNE_OS_RELEASE": str(os_release), "KITSUNE_PACKAGE_MANAGER": "apt"},
    )

    assert completed.returncode == 0, completed.stderr
    binary = Path(str(sandbox["home"])) / ".local" / "bin" / "kitsune-client"
    assert "client:gtk4" in binary.read_text(encoding="utf-8")


def test_forced_legacy_installs_the_gtk3_stack_and_packages(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    (tmp_path / "libs").write_text("", encoding="utf-8")

    completed = run_installer(
        sandbox, "--no-server", "--no-wayland", "--gui", "gtk3", "--from", str(artifacts)
    )

    assert completed.returncode == 0, completed.stderr
    binary = Path(str(sandbox["home"])) / ".local" / "bin" / "kitsune-client"
    assert "client:legacy" in binary.read_text(encoding="utf-8")
    privileged = read_log(sandbox, "sudo-script.sh")
    assert "libgtk-3-0" in privileged
    assert "libwebkit2gtk-4.1-0" in privileged


def test_an_older_lts_without_gui_libraries_selects_the_legacy_build(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    (tmp_path / "libs").write_text("", encoding="utf-8")
    os_release = tmp_path / "os-release"
    os_release.write_text(
        'ID=ubuntu\nVERSION_ID="22.04"\n',
        encoding="utf-8",
    )

    completed = run_installer(
        sandbox,
        "--no-server",
        "--no-wayland",
        "--from",
        str(artifacts),
        env={"KITSUNE_OS_RELEASE": str(os_release), "KITSUNE_PACKAGE_MANAGER": "apt"},
    )

    assert completed.returncode == 0, completed.stderr
    binary = Path(str(sandbox["home"])) / ".local" / "bin" / "kitsune-client"
    assert "client:legacy" in binary.read_text(encoding="utf-8")
    privileged = read_log(sandbox, "sudo-script.sh")
    assert "libgtk-3-0" in privileged
    assert "libwebkit2gtk-4.1-0" in privileged


def test_a_modern_host_with_both_stacks_prefers_gtk4(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    (tmp_path / "libs").write_text(GTK4_LIBS + GTK3_LIBS, encoding="utf-8")
    os_release = tmp_path / "os-release"
    os_release.write_text('ID=ubuntu\nVERSION_ID="24.04"\n', encoding="utf-8")

    completed = install_client_only(sandbox, artifacts, env={"KITSUNE_OS_RELEASE": str(os_release)})

    assert completed.returncode == 0, completed.stderr
    binary = Path(str(sandbox["home"])) / ".local" / "bin" / "kitsune-client"
    assert "client:gtk4" in binary.read_text(encoding="utf-8")


def test_an_id_like_lts_derivative_selects_the_legacy_build(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    (tmp_path / "libs").write_text("", encoding="utf-8")
    os_release = tmp_path / "os-release"
    os_release.write_text(
        'ID=linuxmint\nID_LIKE=ubuntu\nVERSION_ID="21.3"\n',
        encoding="utf-8",
    )

    completed = install_client_only(
        sandbox,
        artifacts,
        env={"KITSUNE_OS_RELEASE": str(os_release), "KITSUNE_PACKAGE_MANAGER": "apt"},
    )

    assert completed.returncode == 0, completed.stderr
    binary = Path(str(sandbox["home"])) / ".local" / "bin" / "kitsune-client"
    assert "client:legacy" in binary.read_text(encoding="utf-8")


def test_one_elevated_step_installs_dependencies_and_the_uinput_rule(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    (tmp_path / "libs").write_text("", encoding="utf-8")

    completed = run_installer(sandbox, "--no-server", "--no-wayland", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    sudo_log = read_log(sandbox, "sudo.log")
    assert sudo_log.count("sudo ") == 1, sudo_log
    privileged = read_log(sandbox, "sudo-script.sh")
    assert "libgtk-4-1" in privileged
    assert "libwebkitgtk-6.0-4" in privileged
    assert "70-kitsune-whisper-uinput.rules" in privileged
    assert 'TAG+="uaccess"' in privileged
    assert "modules-load.d" in privileged
    assert "udevadm" in privileged


def test_declining_elevation_still_installs_and_explains_the_degradation(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    (tmp_path / "libs").write_text("", encoding="utf-8")

    completed = run_installer(
        sandbox,
        "--no-server",
        "--no-wayland",
        "--from",
        str(artifacts),
        env={"FAKE_SUDO_EXIT": "1"},
    )

    assert completed.returncode == 0, completed.stderr
    output = (completed.stdout + completed.stderr).lower()
    assert "clipboard" in output
    assert "settings page" in output or "browser" in output
    binary = Path(str(sandbox["home"])) / ".local" / "bin" / "kitsune-client"
    assert binary.is_file()


def test_wayland_helpers_are_bundled_and_supervised(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)

    completed = run_installer(
        sandbox, "--no-server", "--from", str(artifacts), env={"FAKE_SUDO_EXIT": "0"}
    )

    assert completed.returncode == 0, completed.stderr
    home = Path(str(sandbox["home"]))
    bin_dir = home / ".local" / "bin"
    wayland = home / ".local" / "lib" / "kitsune-whisper" / "wayland" / "bin"
    assert (wayland / "ydotool").is_file()
    assert (wayland / "ydotoold").is_file()
    assert (wayland / "wtype").is_file()
    assert (bin_dir / "ydotool").is_symlink()
    assert (bin_dir / "wtype").is_symlink()
    unit = Path(str(sandbox["env"]["XDG_CONFIG_HOME"])) / "systemd" / "user"
    assert (unit / "kitsune-ydotoold.service").is_file()
    assert "ydotoold" in (unit / "kitsune-ydotoold.service").read_text(encoding="utf-8")
    systemctl = read_log(sandbox, "systemctl.log")
    assert "enable --now kitsune-ydotoold.service" in systemctl
    dropin = unit / "kitsune-client.service.d"
    assert any("wayland" in path.read_text(encoding="utf-8") for path in dropin.iterdir())


def test_server_bundle_bootstraps_from_its_own_runtime(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, with_server=True)

    completed = run_installer(sandbox, "--no-wayland", "--no-elevate", "--from", str(artifacts))

    assert completed.returncode == 0, completed.stderr
    calls = read_log(sandbox, "calls.log")
    assert "server: -s" in calls
    assert "bootstrap.py install" in calls.replace(str(tmp_path), "")
    assert "--artifact" in calls
    assert "kitsune-server_linux_amd64.tar.gz" in calls
    assert "--sha256" in calls


def test_uninstall_keeps_user_data_and_purge_removes_it(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    assert install_client_only(sandbox, artifacts).returncode == 0
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
    assert "uninstall-autostart" in read_log(sandbox, "calls.log")

    assert install_client_only(sandbox, artifacts).returncode == 0
    purged = run_installer(sandbox, "--uninstall", "--purge")
    assert purged.returncode == 0, purged.stderr
    assert not config.exists()
    assert not cache.exists()
    assert not state.exists()
    assert not webview.exists()
    assert "uninstall-autostart" in read_log(sandbox, "calls.log")
    calls = read_log(sandbox, "calls.log")
    assert "secret-tool clear service kitsune-whisper username local" in calls
    assert "secret-tool clear service kitsune-whisper username work" in calls


def test_remove_models_and_server_data_are_separate_choices(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)
    assert install_client_only(sandbox, artifacts).returncode == 0
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

    assert install_client_only(sandbox, artifacts).returncode == 0
    (cache / "models").mkdir(parents=True, exist_ok=True)
    (cache / "models" / "small.bin").write_text("model", encoding="utf-8")
    (state / "devices.json").write_text("{}", encoding="utf-8")
    removed = run_installer(sandbox, "--uninstall", "--remove-server-data")
    assert removed.returncode == 0, removed.stderr
    assert not state.exists()
    assert (cache / "models" / "small.bin").is_file()
    assert (config / "kitsune.yaml").is_file()


def test_dry_run_prints_the_plan_and_changes_nothing(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path)

    completed = run_installer(
        sandbox,
        "--no-server",
        "--no-wayland",
        "--no-elevate",
        "--dry-run",
        "--from",
        str(artifacts),
    )

    assert completed.returncode == 0, completed.stderr
    assert "kitsune-client" in completed.stdout
    assert not (Path(str(sandbox["home"])) / ".local" / "bin" / "kitsune-client").exists()


def test_release_workflow_publishes_every_artifact() -> None:
    import yaml

    workflow_path = REPO_ROOT / ".github" / "workflows" / "release.yml"
    workflow = yaml.safe_load(workflow_path.read_text(encoding="utf-8"))
    assert set(workflow["jobs"]) == {
        "verify-version",
        "client-linux",
        "client-windows",
        "server-bundle",
        "server-bundle-windows",
        "windows-installer",
        "wayland-tools",
        "release",
    }
    text = workflow_path.read_text(encoding="utf-8")
    for asset in (
        "kitsune-client_linux_amd64.tar.gz",
        "kitsune-client_linux_amd64-legacy.tar.gz",
        "kitsune-client_windows_amd64.zip",
        "kitsune-server_linux_amd64.tar.gz",
        "kitsune-server_windows_amd64.tar.gz",
        "kitsune-whisper_windows_amd64_setup.exe",
        "kitsune-wayland_linux_amd64.tar.gz",
        "install.sh",
        "install.ps1",
        "checksums.txt",
    ):
        assert asset in text, asset
    assert "build-wayland-tools.sh" in text
    assert "packaging/windows/build-installer.ps1" in text
    assert "sha256sum" in text

    # Distribution packages are gone: no jobs, builders, flags, or published
    # globs may quietly return.
    for retired in (
        "packaging/deb",
        "packaging/rpm",
        "packaging/appimage",
        "--no-deb",
        "--no-rpm",
        "--no-appimage",
        ".deb",
        ".rpm",
        ".AppImage",
        "appimagetool",
    ):
        assert retired not in text, retired

    # The publish job copies install.sh and install.ps1 from the checkout, so
    # it must check the repository out before generating checksums.
    release_steps = workflow["jobs"]["release"]["steps"]
    assert any(step.get("uses") == "actions/checkout@v4" for step in release_steps)
    assert "cp install.sh dist/install.sh" in text
    assert "cp install.ps1 dist/install.ps1" in text

    # The tag must match the single version file before anything is published.
    assert "verify-version" in text
    assert '[[ "$tag" != "v$version" ]]' in text
    assert "server/pyproject.toml" in text
    assert "server/kitsune_server/__init__.py" in text
    assert "server/uv.lock" in text


def test_ci_builds_no_distribution_packages() -> None:
    import yaml

    workflow_path = REPO_ROOT / ".github" / "workflows" / "ci.yml"
    workflow = yaml.safe_load(workflow_path.read_text(encoding="utf-8"))
    assert "packaging" not in workflow["jobs"]
    text = workflow_path.read_text(encoding="utf-8")
    for retired in ("rpm", "appimagetool", ".deb", ".AppImage"):
        assert retired not in text, retired


def test_the_wayland_builder_exposes_all_three_helpers() -> None:
    script = REPO_ROOT / "scripts" / "build-wayland-tools.sh"
    completed = subprocess.run(["bash", "-n", str(script)], capture_output=True, text=True)
    assert completed.returncode == 0, completed.stderr
    source = script.read_text(encoding="utf-8")
    for tool in ("ydotool", "ydotoold", "wtype", "scdoc"):
        assert tool in source, tool
    assert "kitsune-wayland_linux_" in source


def test_no_client_finisher_writes_config_and_installs_the_server(sandbox, tmp_path):
    artifacts = make_artifacts(tmp_path, with_server=True)
    template = tmp_path / "kitsune.example.yaml"
    template.write_text("client:\n  hotkey: Ctrl+Shift+Space\n", encoding="utf-8")

    completed = run_installer(
        sandbox,
        "--no-client",
        "--no-wayland",
        "--no-elevate",
        "--from",
        str(artifacts),
        env={"KITSUNE_CONFIG_TEMPLATE": str(template)},
    )

    assert completed.returncode == 0, completed.stderr
    config = Path(str(sandbox["env"]["XDG_CONFIG_HOME"])) / "kitsune-whisper" / "kitsune.yaml"
    assert config.is_file()
    assert not (Path(str(sandbox["home"])) / ".local" / "bin" / "kitsune-client").exists()
    assert "bootstrap.py install" in read_log(sandbox, "calls.log")


def test_the_linux_artifact_builder_parses_and_has_no_package_flags():
    script = REPO_ROOT / "scripts" / "build-linux-artifacts.sh"
    completed = subprocess.run(["bash", "-n", str(script)], capture_output=True, text=True)
    assert completed.returncode == 0, completed.stderr

    help_output = subprocess.run(["bash", str(script), "--help"], capture_output=True, text=True)
    assert help_output.returncode == 0, help_output.stderr
    source = script.read_text(encoding="utf-8")
    for retired in (
        "--no-deb",
        "--no-rpm",
        "--no-appimage",
        "build-deb",
        "build-rpm",
        "build-appimage",
        "appimagetool",
        "rpmbuild",
    ):
        assert retired not in source, retired
        assert retired not in help_output.stdout, retired
