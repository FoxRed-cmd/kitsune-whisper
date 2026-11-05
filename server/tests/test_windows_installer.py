"""The native Windows installer and its release wiring.

The installer is a public seam: it is one PowerShell script that downloads the
release checksums and archives, verifies the detached signature and every
checksum, and writes a per-user installation. These tests read that script, the
uninstall shim it generates, and the CI workflows on any host. Compiling and
running it is release CI's job; when ``pwsh`` is available the parser check also
proves the scripts are syntactically valid.
"""

from __future__ import annotations

import re
import shutil
import subprocess
from pathlib import Path

import pytest
import yaml

REPO_ROOT = Path(__file__).resolve().parents[2]
WINDOWS = REPO_ROOT / "packaging" / "windows"
INSTALL_PS1 = REPO_ROOT / "install.ps1"
RELEASE_PUBLIC_KEY = REPO_ROOT / "packaging" / "keys" / "kitsune-release-public.pem"
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


def embedded_public_key(source: str) -> str:
    match = re.search(
        r"-----BEGIN PUBLIC KEY-----.*?-----END PUBLIC KEY-----",
        source,
        re.DOTALL,
    )
    assert match is not None
    return match.group(0).strip()


def uninstall_shim(source: str) -> str:
    """The standalone uninstaller the installer writes next to bin/."""
    return source.split("$body = @'", 1)[1].split("'@", 1)[0]


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


def test_the_pinned_key_is_the_committed_release_key(installer: str) -> None:
    assert embedded_public_key(installer) == read(RELEASE_PUBLIC_KEY).strip()


def test_the_release_key_is_never_downloaded(installer: str) -> None:
    key_function = installer.split("function Get-ReleasePublicKeyPem", 1)[1].split("function ", 1)[
        0
    ]
    assert "return $ReleasePublicKeyPem" in key_function
    assert "Invoke-WebRequest" not in key_function
    assert "curl" not in key_function
    # The environment override is explicitly a test seam, not a user option,
    # and accepts either a path or the PEM text.
    assert "installer test harness" in key_function
    assert "-----BEGIN PUBLIC KEY-----" in key_function


def test_the_detached_signature_is_checked_with_dotnet_ecdsa(installer: str) -> None:
    assert "checksums.txt.sig" in installer
    assert "[System.Security.Cryptography.ECDsa]::Create()" in installer
    assert ".ImportFromPem(" in installer
    # OpenSSL emits a DER ECDSA-Sig-Value; the .NET default is the raw P1363
    # pair, so the format must be named explicitly.
    assert "[System.Security.Cryptography.HashAlgorithmName]::SHA256" in installer
    assert "[System.Security.Cryptography.DSASignatureFormat]::Rfc3279DerSequence" in installer
    assert ".VerifyData(" in installer
    assert "[System.IO.File]::ReadAllBytes(" in installer
    assert "release signature verification failed" in installer


def test_the_signature_is_checked_before_an_archive_is_downloaded_or_extracted(
    installer: str,
) -> None:
    signature_check = installer.index("Assert-ReleaseSignature -ChecksumsPath")
    asset_loop = installer.index("foreach ($asset in $assets)")
    client_install = installer.index("Install-Client -Archive")
    server_install = installer.index("Install-Server -Archive")
    assert signature_check < asset_loop < client_install
    assert signature_check < server_install
    # The .sig is fetched next to checksums.txt, before the archive loop.
    assert installer.index("$signature = Join-Path $Temp 'checksums.txt.sig'") < asset_loop
    assert installer.index("$signature = Join-Path $Temp 'checksums.txt.sig'") < (
        installer.index("Get-ReleaseAsset -Asset")
    )


def test_a_missing_or_tampered_signature_aborts_before_writing(installer: str) -> None:
    assert "missing release signature" in installer
    assert "release signature verification failed" in installer
    # A local artifact directory is held to the same rule as a download.
    assert "missing release signature: $signatureSource" in installer
    signature = installer.split("function Assert-ReleaseSignature", 1)[1].split("function ", 1)[0]
    assert "throw" in signature
    assert "ImportFromPem" in signature
    # The archives are only requested after the checksum file is trusted.
    call = installer.index("Assert-ReleaseSignature -ChecksumsPath")
    assert call < installer.index("Install-Client -Archive")
    assert call < installer.index("Install-Server -Archive")


def test_insecure_skips_only_the_signature_check(installer: str) -> None:
    assert "[switch]$Insecure" in installer
    assert "skipping the release signature check" in installer
    # Insecure is an integrity opt-out, not a checksum opt-out.
    assert "Assert-Checksum" in installer
    assert "checksum mismatch" in installer
    download_block = installer.split("# The signature covers checksums.txt", 1)[1].split(
        "$assets = @()", 1
    )[0]
    insecure = download_block.split("if ($Insecure)", 1)[1].split("else", 1)[0]
    assert "skipping the release signature check" in insecure
    assert "Assert-ReleaseSignature" not in insecure
    # The archive loop is outside the insecure branch and always asserts the
    # checksum, so a tampered archive still aborts.
    archive_loop = installer.split("foreach ($asset in $assets)", 1)[1]
    assert "Get-ReleaseAsset" in archive_loop
    assert "Assert-Checksum" in installer.split("function Get-ReleaseAsset", 1)[1]


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
    shim = uninstall_shim(installer)
    assert "$PSScriptRoot" in shim
    assert "$installRoot = $PSScriptRoot" in shim or "InstallRoot" in shim
    assert "$uninstallKey" in shim
    assert "Remove-Item -LiteralPath $uninstallKey" in shim
    assert "uninstall-autostart" in shim
    assert "kitsune-server.cmd" in shim
    assert "schtasks.exe /Delete /TN 'kitsune-server'" in shim
    assert "schtasks.exe /Delete /TN 'kitsune-client'" in shim
    # The cached task files and the HKCU Run fallback are removed even when the
    # Client binary is already gone.
    assert "autostart-task.xml" in shim
    assert "Remove-ItemProperty" in shim
    assert "Remove-Item -LiteralPath $installRoot" in shim
    assert "configuration, models and pairing state were kept" in shim
    # The shim removes the whole install root, so a Client-only or Server-only
    # tree needs no separate path.
    assert "$installRoot" in shim
    # No interactive prompt.
    assert "Read-Host" not in shim
    assert "Read-Host" not in installer


def test_purge_and_partial_removals_match_the_linux_boundaries(installer: str) -> None:
    shim = uninstall_shim(installer)
    for switch in ("[switch]$Purge", "[switch]$RemoveModels", "[switch]$RemoveServerData"):
        assert switch in shim, switch
    # Uninstall removes the binaries, Server bundle, task registrations, menu
    # shortcut, PATH entry and registry key. It keeps the data directories.
    for removed in (
        "Remove-Item -LiteralPath $installRoot",
        "Remove-Item -LiteralPath $shortcut",
        "Remove-Item -LiteralPath $uninstallKey",
        "Remove-PathEntry",
    ):
        assert removed in shim, removed
    # The default mode must not delete configuration, models, state or logs.
    default_removal = shim.split("if ($Purge)", 1)[0]
    assert "Remove-Item -LiteralPath $configDir" not in default_removal
    assert "Remove-Item -LiteralPath $stateDir" not in default_removal
    assert "Remove-Item -LiteralPath $modelDir" not in default_removal
    # Purge removes every trace, including stored credentials.
    purge = shim.split("if ($Purge)", 1)[1]
    for removed in (
        "$configDir",
        "$stateDir",
        "$modelDir",
        "Remove-StoredCredentials",
    ):
        assert removed in purge, removed
    assert "cmdkey" in shim
    # --remove-server-data is narrower: certificates, Devices, logs and
    # soft-stop state, not the Client cache or spool.
    for server_data in (
        "tls",
        "devices.json",
        "pairing.json",
        "server.log",
        "lifecycle.json",
    ):
        assert server_data in shim, server_data
    assert "reset-local-pairing" in shim
    # Removal never installs or removes packages, and never elevates.
    lowered = shim.lower()
    for forbidden in (
        "sudo",
        "runas",
        "-verb ",
        "winget",
        "choco",
        "scoop",
        "msiexec",
        "apt-get",
        "dnf install",
        "pacman",
        "zypper",
    ):
        assert forbidden not in lowered, forbidden


def test_purge_and_data_flags_require_uninstall(installer: str) -> None:
    assert "only valid together with -Uninstall" in installer
    for flag in ("$Purge", "$RemoveModels", "$RemoveServerData"):
        assert flag in installer


def test_uninstall_does_not_require_a_previous_install(installer: str) -> None:
    uninstall = installer.split("function Uninstall-Kitsune", 1)[1].split("if ($ClientOnly", 1)[0]
    # A current shim carries the requested flags regardless of what an older
    # install left behind, and removal is safe when nothing was installed.
    assert "Write-UninstallShim -Path $shim" in uninstall
    assert "'-InstallRoot', $InstallRoot" in uninstall
    assert "Test-Path -LiteralPath $UninstallShim" not in uninstall


def test_uninstall_tolerates_a_client_only_or_server_only_tree(installer: str) -> None:
    shim = uninstall_shim(installer)
    # Both probes are guarded, so a missing Client or Server is not an error.
    assert "if (Test-Path -LiteralPath $client)" in shim
    assert "if (Test-Path -LiteralPath $launcher)" in shim
    assert "schtasks.exe /Delete /TN 'kitsune-server'" in shim
    assert "schtasks.exe /Delete /TN 'kitsune-client'" in shim


def test_user_path_is_extended_once_and_removed_on_uninstall(installer: str) -> None:
    assert "GetEnvironmentVariable('Path', 'User')" in installer
    assert "SetEnvironmentVariable('Path'" in installer
    # Compare case-insensitively so the entry is never duplicated.
    assert "-ieq $BinDir" in installer
    shim = uninstall_shim(installer)
    assert "-ine $bin" in shim
    assert "SetEnvironmentVariable('Path'" in shim
    # Removing every duplicate, not just the first, is what makes the change
    # reversible even when an older installer appended the entry twice.
    assert "Where-Object { $_ -and ($_ -ine $bin) }" in shim


def test_component_selection_installs_only_the_selected_tree(installer: str) -> None:
    assert "[switch]$ClientOnly" in installer
    assert "[switch]$ServerOnly" in installer
    assert "'-ClientOnly and -ServerOnly are mutually exclusive'" in installer
    # Neither flag means both components.
    assert "if (-not $ServerOnly) {" in installer
    assert "if (-not $ClientOnly) {" in installer
    assert re.search(r"if \(-not \$ServerOnly\) \{\s*\$assets \+= \$ClientAsset", installer)
    assert re.search(r"if \(-not \$ClientOnly\) \{\s*\$assets \+= \$ServerAsset", installer)
    assert re.search(r"if \(-not \$ServerOnly\) \{\s*Install-Client", installer)
    assert re.search(r"if \(-not \$ClientOnly\) \{\s*Install-Server", installer)
    # A Server-only install must not create a Client configuration.
    install_client = installer.split("function Install-Client", 1)[1].split("function ", 1)[0]
    assert "Install-Config" in install_client
    assert installer.count("Install-Config (") == 1
    # The install plan names both components and what is skipped.
    assert "client:    $clientPlan" in installer
    assert "server:    $serverPlan" in installer
    assert "'disabled'" in installer


def test_missing_webview2_only_warns_for_a_client_install(installer: str) -> None:
    assert "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" in installer
    assert "Test-WebView2Installed" in installer
    assert "Write-Warning" in installer
    assert "WebView2" in installer
    assert "developer.microsoft.com/microsoft-edge/webview2" in installer
    # Warnings point at the README Dependencies section, the only place with
    # package commands.
    assert "README.md#dependencies" in installer
    assert re.search(r"if \(-not \$ServerOnly\) \{\s*Write-WebView2Warning", installer)
    # No bootstrapper, no silent install, and no Microsoft download at all.
    assert "WebView2Setup" not in installer
    assert "MicrosoftEdgeWebView2" not in installer
    assert "/silent /install" not in installer
    for forbidden in ("winget", "choco", "scoop", "msiexec", "Install-Package"):
        assert forbidden not in installer.lower(), forbidden


def test_other_windows_prerequisites_are_warned_about_after_install(
    installer: str,
) -> None:
    assert "Write-DependencyWarning" in installer
    assert "windows-64-bit" in installer
    assert "windows-version" in installer
    # Dependency reporting runs once the selected binaries are in place; a
    # missing one never changes the exit status.
    report = installer.rindex("Write-WindowsDependencyWarnings")
    assert report > installer.index("Register-UninstallEntry")
    assert report > installer.index("New-StartMenuShortcut")
    function = installer.split("function Write-WindowsDependencyWarnings", 1)[1].split(
        "function ", 1
    )[0]
    assert "exit 1" not in function


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
        "-Purge",
        "-RemoveModels",
        "-RemoveServerData",
        "-ClientOnly",
        "-ServerOnly",
        "-ForceConfig",
        "-Force",
        "-Check",
        "-DryRun",
        "-From",
        "-Insecure",
    ):
        assert flag in installer, flag
    assert "KITSUNE_VERSION" in installer
    # Every flag is described in the embedded help.
    help_text = installer.split("<#", 1)[1].split("#>", 1)[0]
    for flag in (
        "-Version",
        "-Uninstall",
        "-Purge",
        "-RemoveModels",
        "-RemoveServerData",
        "-ClientOnly",
        "-ServerOnly",
        "-ForceConfig",
        "-Force",
        "-Check",
        "-DryRun",
        "-From",
        "-Insecure",
    ):
        assert flag in help_text, flag
    # Nothing to start after install, so nothing is launched in the Tray.
    assert "gui --hidden" not in installer


def test_install_manifest_matches_the_linux_schema(installer: str) -> None:
    assert "$ManifestPath = Join-Path $StateDir 'install.json'" in installer
    assert "function Write-InstallManifest" in installer
    manifest = installer.split("function Write-InstallManifest", 1)[1].split("function ", 1)[0]
    for field in ('"version"', '"components"', '"installed_at"'):
        assert field in manifest, field
    # ISO-8601 UTC, byte-for-byte the format install.sh writes.
    assert "UtcNow.ToString('yyyy-MM-ddTHH:mm:ss') + 'Z'" in manifest
    # Atomic replace beside the manifest, not a partial in-place write.
    assert "Set-Content -LiteralPath $temporary" in manifest
    assert "Move-Item -LiteralPath $temporary -Destination $ManifestPath -Force" in manifest
    assert 'Write-Step "wrote the install manifest' in manifest

    # Apps & Features reads the manifest rather than guessing from binaries.
    display = installer.split("function Get-DisplayVersion", 1)[1].split("function ", 1)[0]
    assert "Get-ManifestVersion" in display

    # Uninstall removes the bookkeeping even when it keeps user data.
    shim = uninstall_shim(installer)
    assert "$manifest = if ($stateDir) { Join-Path $stateDir 'install.json' } else { '' }" in shim
    assert "Remove-OurPath $manifest" in shim


def test_check_mode_reports_versions_and_components_without_changing_anything(
    installer: str,
) -> None:
    assert "[switch]$Check" in installer
    assert "function Show-CheckStatus" in installer
    check = installer.split("function Show-CheckStatus", 1)[1].split("function ", 1)[0]
    for label in ("Manifest:", "Installed:", "Components:", "Available:"):
        assert label in check, label
    assert "exit 0" in check
    # Check mode neither installs nor prompts.
    assert "Install-Kitsune" not in check
    assert "Read-Host" not in check
    assert "'-Check and -Uninstall are mutually exclusive'" in installer
    # Dispatch runs the check before the installer can write anything.
    dispatch = installer.rsplit("if ($Check)", 1)[1]
    assert "Show-CheckStatus" in dispatch
    assert dispatch.index("Show-CheckStatus") < dispatch.index("Install-Kitsune")


def test_available_version_and_downgrade_guard_match_linux(installer: str) -> None:
    assert "[switch]$Force" in installer
    assert "function Resolve-AvailableVersion" in installer
    assert "function Get-LocalArtifactVersion" in installer
    assert "function Get-LatestReleaseVersion" in installer
    assert "Join-Path $From 'VERSION'" in installer
    assert "'-xOf' $archive 'bundle.json'" in installer
    assert "tag_name" in installer

    resolve = installer.split("function Resolve-AvailableVersion", 1)[1].split("function ", 1)[0]
    assert "if ($Version -ne 'latest')" in resolve
    assert "Get-LocalArtifactVersion" in resolve
    assert "Get-LatestReleaseVersion" in resolve

    guard = installer.split("function Assert-NoDowngrade", 1)[1].split("function ", 1)[0]
    assert "Get-ManifestVersion" in guard
    assert "Test-VersionIsLower $Available $installed" in guard
    assert "refusing to downgrade" in guard
    assert "pass -Force" in guard
    # Equal and higher versions pass; only a lower numeric component refuses.
    assert "if ($an -lt $bn) { return $true }" in installer
    assert "if ($an -gt $bn) { return $false }" in installer

    # The guard runs after the signature check but before archives are fetched.
    guard_call = installer.index("Assert-NoDowngrade -Available")
    assert guard_call < installer.index("foreach ($asset in $assets)")
    assert installer.index("Assert-ReleaseSignature -ChecksumsPath") < guard_call


def test_install_stops_a_running_client_and_server_before_replacing(installer: str) -> None:
    assert "function Stop-RunningComponents" in installer
    stop = installer.split("function Stop-RunningComponents", 1)[1].split("function ", 1)[0]
    assert "Get-Process -Name 'kitsune-client'" in stop
    assert "Stop-Process -Force" in stop
    assert "& schtasks.exe /End /TN 'kitsune-server'" in stop
    # A Server started outside Task Scheduler is matched by bundle path, never
    # by stopping an unrelated Python.
    assert "Get-Process -Name 'python', 'pythonw'" in stop
    assert "StartsWith($prefix" in stop
    # Every stop is best-effort: no failure may abort the install.
    assert stop.count("catch") >= 3
    assert "throw" not in stop
    assert "exit 1" not in stop

    install_client = installer.split("function Install-Client", 1)[1].split("function ", 1)[0]
    assert "Stop-RunningComponents" in install_client
    assert install_client.index("Stop-RunningComponents") < install_client.index(
        "Copy-Item -LiteralPath $executable"
    )

    install_server = installer.split("function Install-Server {", 1)[1].split("function ", 1)[0]
    assert "Stop-RunningComponents" in install_server
    assert install_server.index("Stop-RunningComponents") < install_server.index(
        "Install-ServerBundle"
    )


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
    # The checksums the Windows installer pins are themselves signed.
    assert "checksums.txt.sig" in text
    assert "scripts/sign-release-checksums.sh" in text


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
