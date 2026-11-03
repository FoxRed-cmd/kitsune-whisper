"""Behaviour of the per-user Windows installer and its release wiring.

The installer is a public seam: its contract is the directives it compiles into,
the helper it ships, and the artifacts CI publishes. These tests read those
artifacts on any host; compiling the Inno script is release CI's job.
"""

from __future__ import annotations

from pathlib import Path

import yaml

REPO_ROOT = Path(__file__).resolve().parents[2]
WINDOWS = REPO_ROOT / "packaging" / "windows"
INNO_SCRIPT = WINDOWS / "kitsune-whisper.iss"
BUILD_SCRIPT = WINDOWS / "build-installer.ps1"
SETUP_HELPER = WINDOWS / "setup.ps1"
INSTALL_PS1 = REPO_ROOT / "install.ps1"
RELEASE_WORKFLOW = REPO_ROOT / ".github" / "workflows" / "release.yml"
SETUP_ASSET = "kitsune-whisper_windows_amd64_setup.exe"
SERVER_ASSET = "kitsune-server_windows_amd64.tar.gz"


def read(path: Path) -> str:
    return path.read_text(encoding="utf-8")


def test_installer_never_requests_administration_rights() -> None:
    script = read(INNO_SCRIPT).lower()
    assert "privilegesrequired=lowest" in script
    assert "defaultdirname={localappdata}\\kitsune-whisper" in script
    assert "admin" not in script.replace("administrator", "")


def test_installer_carries_the_client_tray_and_server_payload() -> None:
    script = read(INNO_SCRIPT)
    for needle in (
        "{#ClientExe}",
        "kitsune-client.exe",
        "kitsune-setup.ps1",
        "{#ServerArtifact}",
        "kitsune-server_windows_amd64.tar.gz",
        "{#ExampleConfig}",
    ):
        assert needle in script, needle


def test_start_menu_entry_and_optional_desktop_shortcut_are_created() -> None:
    script = read(INNO_SCRIPT)
    assert "{autoprograms}\\kitsune-whisper" in script
    assert "{autodesktop}\\kitsune-whisper" in script
    assert 'Name: "{app}\\bin\\kitsune-client.exe"' not in script
    assert 'Parameters: "gui"' in script
    assert "Tasks: desktopicon" in script
    assert "Flags: unchecked" in script


def test_webview2_is_bootstrapped_per_user_with_an_actionable_failure() -> None:
    script = read(INNO_SCRIPT)
    # The Evergreen runtime is detected in both per-user and per-machine hives,
    # then installed by the current user, never by an elevated helper.
    assert "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}" in script
    assert "HKCU" in script
    assert "HKLM" in script
    assert "MicrosoftEdgeWebView2Setup.exe" in script
    assert "/silent /install" in script
    lowered = script.lower()
    assert "offline" in lowered
    assert "webview2" in lowered
    assert "developer.microsoft.com" in lowered


def test_client_and_server_autostart_are_registered_with_the_hidden_shell() -> None:
    script = read(INNO_SCRIPT)
    helper = read(SETUP_HELPER)
    assert "install-autostart" in helper
    assert "bootstrap.py" in helper
    assert "install" in helper
    assert "RunHelper('install'" in script
    assert "'gui', '--hidden'" in helper
    assert "Start-Client" in helper


def test_cuda_runtime_is_an_optional_windows_download() -> None:
    script = read(INNO_SCRIPT)
    helper = read(SETUP_HELPER)
    assert 'Name: "cuda"' in script
    assert "WizardIsTaskSelected('cuda')" in script
    assert "Params + ' -Cuda'" in script
    assert "nvidia-cublas-cu12" in helper
    assert "Test-NvidiaGpu" in helper
    assert "Server\\runtime\\python.exe" in helper
    assert "'--target'" in helper
    # Restart through the supervisor; `start` is the foreground Server and
    # would block the setup until the user kills it.
    assert "@('restart')" in helper
    # A failed download is a warning, not an install failure.
    assert "warning: CUDA runtime" in helper


def test_cuda_task_reports_an_already_downloaded_runtime() -> None:
    script = read(INNO_SCRIPT)
    # The task starts as a download offer, then a [Code] hook inspects the
    # previous install and turns it into a disabled "already installed" line.
    assert 'Description: "{cm:CudaTask}"' in script
    assert "CudaTaskInstalled" in script
    assert "function CudaRuntimeInstalled" in script
    assert "Server\\lib\\nvidia\\cublas\\bin\\cublas64_12.dll" in script
    assert "WizardForm.TasksList.ItemCaption[Index]" in script
    assert "WizardForm.TasksList.Checked[Index] := False" in script
    assert "WizardForm.TasksList.ItemEnabled[Index] := False" in script
    assert "CurPageID = wpSelectTasks" in script


def test_upgrade_preserves_a_downloaded_cuda_runtime() -> None:
    helper = read(SETUP_HELPER)
    # The bundle install replaces lib/ wholesale, so the helper carries the
    # nvidia wheel directory over before it runs bootstrap install.
    assert "preserving the CUDA runtime" in helper
    assert "restored the CUDA runtime" in helper
    assert "Server\\lib\\nvidia" in helper
    assert "'kitsune-nvidia-'" in helper


def test_in_place_upgrade_stops_the_tray_and_the_server_first() -> None:
    # Inno's Restart Manager cannot close a tray process or a Task Scheduler
    # process, so an upgrade must stop both before copying files instead of
    # showing the "unable to close all applications" dialog.
    script = read(INNO_SCRIPT)
    assert "CloseApplications=no" in script
    assert "procedure StopRunningProcesses" in script
    assert "/End /TN kitsune-client" in script
    assert "/End /TN kitsune-server" in script
    assert "/F /IM kitsune-client.exe" in script
    prepare = script.split("function PrepareToInstall", 1)[1]
    assert "StopRunningProcesses;" in prepare.split("end;", 1)[0]


def test_the_server_runs_hidden_and_the_port_preflight_is_actionable() -> None:
    helper = read(SETUP_HELPER).lower()
    assert "server.port" in helper
    assert "already in use" in helper
    assert "pythonw.exe" in helper
    assert "change server.port" in helper


def test_helper_does_not_treat_native_stderr_as_a_failure() -> None:
    # Windows PowerShell 5.1 raises NativeCommandError for a native command's
    # stderr under ErrorActionPreference=Stop, which turned the Client's
    # successful autostart log line into a reported install failure.
    helper = read(SETUP_HELPER)
    assert "$ErrorActionPreference = 'Continue'" in helper
    assert "finally" in helper


def test_helper_waits_for_a_gui_client_before_reading_its_exit_code() -> None:
    # Windows PowerShell does not wait for a GUI-subsystem executable, so the
    # call operator returned immediately and left $LASTEXITCODE unset, which is
    # a StrictMode error in Invoke-ConfigMigration. The helper starts the
    # process explicitly and drains both pipes before asking for the exit code.
    helper = read(SETUP_HELPER)
    invoke = helper.split("function Invoke-Native", 1)[1].split("function Initialize-Config", 1)[0]
    assert "System.Diagnostics.ProcessStartInfo" in invoke
    assert "ReadToEndAsync" in invoke
    assert "WaitForExit" in invoke
    # A .cmd launcher is still run through the call operator, which sets
    # $LASTEXITCODE itself.
    assert "$LASTEXITCODE" in invoke


def test_uninstall_removes_tasks_and_binaries_but_keeps_user_data_by_default() -> None:
    script = read(INNO_SCRIPT)
    helper = read(SETUP_HELPER).lower()
    assert "uninstall-autostart" in helper
    assert "uninstall" in helper
    # The helper only broadens deletion when the purge option is present.
    assert "$purge" in helper
    assert "remove-userdata" in helper
    assert "appdata" in helper
    assert "localappdata" in helper
    assert "CurUninstallStepChanged" in script
    assert "PURGE" in script
    assert "autostart-task.xml" in script
    assert "kitsune-server-task.xml" in script
    assert "user data" in helper or "user data" in script.lower()
    # Models and Server data are separate choices, not one combined prompt.
    assert "REMOVEMODELS" in script
    assert "REMOVESERVERDATA" in script
    assert "delete downloaded Whisper models" in script
    assert "delete Server data" in script or "Server data:" in script
    assert "remove-models" in helper
    assert "remove-serverdata" in helper


def test_build_script_verifies_and_defines_every_installer_input() -> None:
    script = read(BUILD_SCRIPT)
    for needle in (
        "Get-FileHash",
        "SHA256",
        "/DServerSha256=",
        "/DClientExe=",
        "/DServerArtifact=",
        "/DWebView2Bootstrapper=",
        "ISCC.exe",
        SETUP_ASSET,
        "VersionInfoVersion",
        "Get-AuthenticodeSignature",
    ):
        assert needle in script, needle


def test_release_ci_builds_the_windows_bundle_and_installer() -> None:
    workflow = yaml.safe_load(read(RELEASE_WORKFLOW))
    jobs = workflow["jobs"]
    assert "server-bundle-windows" in jobs
    assert "windows-installer" in jobs
    assert jobs["windows-installer"]["runs-on"] == "windows-latest"
    assert set(jobs["release"]["needs"]) >= {
        "client-windows",
        "server-bundle-windows",
        "windows-installer",
    }

    client_windows = jobs["client-windows"]
    build = next(step for step in client_windows["steps"] if step.get("name") == "Build")
    # The workflow delegates the desktop tag to the build script, which is the
    # one place that also sets the GUI subsystem and version resources.
    assert "packaging/windows/build-client.ps1" in build["run"]
    assert "-tags 'desktop'" in read(WINDOWS / "build-client.ps1")

    upload = next(
        step
        for step in client_windows["steps"]
        if str(step.get("uses", "")).startswith("actions/upload-artifact")
    )
    download = next(
        step
        for step in jobs["windows-installer"]["steps"]
        if str(step.get("uses", "")).startswith("actions/download-artifact")
    )
    assert download["with"]["name"] == upload["with"]["name"]

    text = read(RELEASE_WORKFLOW)
    assert SERVER_ASSET in text
    assert SETUP_ASSET in text
    assert "packaging/windows/build-installer.ps1" in text
    assert "sha256sum" in text


def test_install_upgrades_migrate_the_existing_configuration() -> None:
    helper = read(SETUP_HELPER)
    installer = read(INSTALL_PS1)
    assert "Invoke-ConfigMigration" in helper
    assert "@('migrate-config', '--config', $ConfigPath)" in helper
    assert "-RemoveModels" in installer
    assert "-RemoveServerData" in installer
    assert "/REMOVEMODELS=1" in installer
    assert "/REMOVESERVERDATA=1" in installer


def test_the_one_line_installer_runs_the_full_setup() -> None:
    script = read(INSTALL_PS1)
    helper = read(SETUP_HELPER)
    assert SETUP_ASSET in script
    assert "checksums.txt" in script
    assert "Get-FileHash" in script
    assert "/SILENT" in script
    assert "unins" in script
    assert "-Purge" in script or "Purge" in script
    assert "/FORCECONFIG=1" in script
    assert "SetEnvironmentVariable" in helper
    assert "user PATH" in helper
