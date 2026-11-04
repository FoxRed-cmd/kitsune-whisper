"""The native Windows installer and its release wiring.

The installer is a public seam: it is one PowerShell script that downloads the
release archives, verifies their checksums, and writes a per-user installation.
These tests read that script, the uninstall shim it generates, and the CI
workflows on any host. Compiling and running it is release CI's job; when
``pwsh`` is available the parser check also proves the scripts are syntactically
valid.
"""

from __future__ import annotations

import shutil
import subprocess
from pathlib import Path

import pytest
import yaml

REPO_ROOT = Path(__file__).resolve().parents[2]
WINDOWS = REPO_ROOT / "packaging" / "windows"
INSTALL_PS1 = REPO_ROOT / "install.ps1"
BUILD_CLIENT = WINDOWS / "build-client.ps1"
INNO_SCRIPT = WINDOWS / ("kitsune-whisper" + ".iss")
BUILD_INSTALLER = WINDOWS / ("build-" + "installer.ps1")
RETIRED_INNO = "kitsune-whisper" + ".iss"
RETIRED_BUILDER = "build-" + "installer"
RETIRED_INNO_SETUP = "inno" + "setup"
SETUP_HELPER = WINDOWS / "setup.ps1"
RELEASE_WORKFLOW = REPO_ROOT / ".github" / "workflows" / "release.yml"
CI_WORKFLOW = REPO_ROOT / ".github" / "workflows" / "ci.yml"
CLIENT_ASSET = "kitsune-client_windows_amd64.zip"
SERVER_ASSET = "kitsune-server_windows_amd64.tar.gz"
SETUP_ASSET = "kitsune-whisper_windows_amd64_setup.exe"
INSTALL_ROOT = "'Programs\\kitsune-whisper'"


def read(path: Path) -> str:
    return path.read_text(encoding="utf-8")


@pytest.fixture(scope="module")
def installer() -> str:
    return read(INSTALL_PS1)


def test_the_setup_executable_pipeline_is_deleted() -> None:
    assert not INNO_SCRIPT.exists()
    assert not BUILD_INSTALLER.exists()
    assert not SETUP_HELPER.exists()
    # The Client build stays: it produces the zip the installer downloads.
    assert BUILD_CLIENT.exists()


def test_installer_never_requires_administration(installer: str) -> None:
    lowered = installer.lower()
    # Nothing may re-launch this script elevated or ask Windows for elevation.
    assert "runas" not in lowered
    assert "-verb " not in lowered
    assert "requireadministrator" not in lowered
    assert "sudo" not in lowered


def test_installer_writes_the_per_user_programs_layout(installer: str) -> None:
    assert "LOCALAPPDATA" in installer
    assert INSTALL_ROOT in installer
    assert "$BinDir = Join-Path $InstallRoot 'bin'" in installer
    assert "$ServerRoot = Join-Path $InstallRoot 'Server'" in installer
    assert "$UninstallShim = Join-Path $InstallRoot 'uninstall.ps1'" in installer
    # The layout is the per-user programs convention, not the old app-data dir.
    assert "'Programs\\kitsune-whisper'" in installer
    assert "kitsune-whisper\\bin'" not in installer


def test_installer_downloads_and_verifies_both_archives(installer: str) -> None:
    assert CLIENT_ASSET in installer
    assert SERVER_ASSET in installer
    assert "checksums.txt" in installer
    assert "Get-FileHash" in installer
    assert "SHA256" in installer
    assert "checksum mismatch" in installer
    assert "Get-ChecksumFor" in installer
    assert "Assert-Checksum" in installer
    # Local artifacts keep the installer testable and support an offline host.
    assert "KITSUNE_ARTIFACTS_DIR" in installer
    assert "-From" in installer
    # Signature verification is deliberately #97's completion, not a stub here.
    assert "checksums.txt.sig" in installer
    assert "ECDsa" in installer
    assert "System.Security.Cryptography.ECDsa" not in installer


def test_apps_and_features_registers_a_working_uninstall_command(installer: str) -> None:
    assert "HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Uninstall" in installer
    for value in (
        "DisplayName",
        "DisplayVersion",
        "Publisher",
        "InstallLocation",
        "DisplayIcon",
        "UninstallString",
        "QuietUninstallString",
        "NoModify",
        "NoRepair",
    ):
        assert value in installer, value
    # The uninstall command runs the installed shim, not a downloaded
    # executable and not an interactive prompt.
    assert "Join-Path $PSHOME 'pwsh.exe'" in installer
    assert "-NoProfile -NonInteractive -ExecutionPolicy Bypass -File" in installer
    assert "$UninstallShim" in installer


def test_uninstall_shim_removes_our_files_and_keeps_user_data(installer: str) -> None:
    shim = installer.split("$body = @'", 1)[1].split("'@", 1)[0]
    assert "$PSScriptRoot" in shim
    assert "$installRoot = $PSScriptRoot" in shim
    assert "$uninstallKey" in shim
    assert "Remove-Item -LiteralPath $uninstallKey" in shim
    assert "uninstall-autostart" in shim
    assert "kitsune-server.cmd" in shim
    assert "schtasks.exe /Delete /TN 'kitsune-server'" in shim
    assert "Remove-Item -LiteralPath $installRoot" in shim
    assert "configuration, models and pairing state were kept" in shim
    # No interactive prompt, and purge boundaries stay with #97.
    assert "Read-Host" not in shim
    assert "Purge" not in shim
    assert "Read-Host" not in installer


def test_user_path_is_extended_once_and_removed_on_uninstall(installer: str) -> None:
    assert "GetEnvironmentVariable('Path', 'User')" in installer
    assert "SetEnvironmentVariable('Path'" in installer
    # Compare case-insensitively so the entry is never duplicated.
    assert "-ieq $BinDir" in installer
    shim = installer.split("$body = @'", 1)[1].split("'@", 1)[0]
    assert "-ine $bin" in shim
    assert "SetEnvironmentVariable('Path'" in shim


def test_missing_webview2_only_warns_and_never_downloads(installer: str) -> None:
    assert "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" in installer
    assert "Test-WebView2Installed" in installer
    assert "Write-Warning" in installer
    assert "WebView2" in installer
    assert "developer.microsoft.com/microsoft-edge/webview2" in installer
    assert "docs/install.md#windows" in installer
    # No bootstrapper, no silent install, and no Microsoft download at all.
    assert "WebView2Setup" not in installer
    assert "MicrosoftEdgeWebView2" not in installer
    assert "/silent /install" not in installer


def test_nothing_starts_at_logon_and_the_server_is_installed_no_start(
    installer: str,
) -> None:
    # The Server bundle registers its supervisor disabled through --no-start.
    assert "'--no-start'" in installer
    assert "registered, not started" in installer
    # The Client autostart is the settings switch's job, not the installer's.
    assert "'install-autostart'" not in installer
    # Only the Server supervisor may create a task, and it does so disabled.
    assert "/Create" not in installer
    assert "New-ScheduledTask" not in installer
    assert "/SC " not in installer
    assert "schtasks.exe /Run" not in installer


def test_upgrade_preserves_an_existing_cuda_directory(installer: str) -> None:
    assert "Join-Path $ServerRoot 'lib\\nvidia" in installer
    assert "cublas64_12.dll" in installer
    assert "preserving the CUDA runtime" in installer
    assert "restored the CUDA runtime" in installer
    assert "nvidia-cublas-cu12" not in installer


def test_the_installer_exposes_the_maintained_flags(installer: str) -> None:
    for flag in (
        "-Version",
        "-Uninstall",
        "-ClientOnly",
        "-ServerOnly",
        "-ForceConfig",
        "-DryRun",
        "-From",
    ):
        assert flag in installer, flag
    assert "KITSUNE_VERSION" in installer
    # Nothing to start after install, so nothing is launched in the Tray.
    assert "gui --hidden" not in installer


def test_release_workflow_publishes_scripts_and_archives_only() -> None:
    workflow = yaml.safe_load(read(RELEASE_WORKFLOW))
    jobs = workflow["jobs"]
    assert "windows-installer" not in jobs
    assert {"client-windows", "server-bundle-windows"} <= set(jobs)
    assert "windows-installer" not in jobs["release"]["needs"]

    text = read(RELEASE_WORKFLOW)
    assert SETUP_ASSET not in text
    assert RETIRED_BUILDER not in text
    assert RETIRED_INNO_SETUP not in text.lower()
    assert "Inno Setup" not in text
    # The release still publishes the Windows zip, the Server bundle, and both
    # installer scripts, and the publish step copies the scripts from the tree.
    assert CLIENT_ASSET in text
    assert SERVER_ASSET in text
    assert "cp install.ps1 dist/install.ps1" in text


def test_ci_windows_job_parses_only_the_remaining_scripts() -> None:
    workflow = yaml.safe_load(read(CI_WORKFLOW))
    job = workflow["jobs"]["packaging-windows"]
    parse = next(step for step in job["steps"] if step.get("name", "").startswith("Parse"))
    run = parse["run"]
    assert "install.ps1" in run
    assert "build-client.ps1" in run
    assert "setup.ps1" not in run
    assert RETIRED_BUILDER not in run

    text = read(CI_WORKFLOW).lower()
    assert RETIRED_INNO_SETUP not in text
    assert RETIRED_BUILDER not in text
    assert RETIRED_INNO not in text


PWSH = shutil.which("pwsh")


@pytest.mark.skipif(PWSH is None, reason="pwsh is not installed")
@pytest.mark.parametrize(
    "path", [INSTALL_PS1, BUILD_CLIENT], ids=["install.ps1", "build-client.ps1"]
)
def test_powershell_entry_points_parse(path: Path) -> None:
    assert PWSH is not None
    pwsh = PWSH
    escaped = str(path).replace("'", "''")
    command = (
        "$errors = $null; "
        f"$null = [System.Management.Automation.Language.Parser]::ParseFile('{escaped}', "
        "[ref]$null, [ref]$errors); "
        "if ($errors.Count -gt 0) { "
        "$errors | ForEach-Object { Write-Error $_.Message }; exit 1 }"
    )
    completed = subprocess.run(
        [pwsh, "-NoProfile", "-NonInteractive", "-Command", command],
        capture_output=True,
        text=True,
        timeout=60,
    )
    assert completed.returncode == 0, completed.stderr
