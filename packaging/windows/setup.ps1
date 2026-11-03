#!/usr/bin/env pwsh
# kitsune-whisper Windows installer helper.
#
# Inno Setup ships this file next to the Client and calls it after the files are
# in place, and again before uninstall. It installs or removes the native Server
# bundle, starts and stops the per-user autostart registrations, seeds a config
# template only when none exists, and (on purge) removes user data.
#
# Everything here runs as the installing user: no elevation, no system service.
[CmdletBinding()]
param(
    [ValidateSet('install', 'uninstall')]
    [string]$Action = 'install',
    [string]$AppDir = (Join-Path $env:LOCALAPPDATA 'kitsune-whisper'),
    [string]$ServerArtifact = '',
    [string]$ServerSha256 = '',
    [string]$ConfigPath = (Join-Path $env:APPDATA 'kitsune-whisper\kitsune.yaml'),
    [string]$TemplatePath = '',
    [string]$LogPath = '',
    [switch]$Cuda,
    [switch]$Purge,
    [switch]$RemoveModels,
    [switch]$RemoveServerData,
    [switch]$ForceConfig
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0

if (-not $LogPath) {
    $LogPath = Join-Path ([System.IO.Path]::GetTempPath()) 'kitsune-whisper-setup.log'
}

$script:LogLines = New-Object System.Collections.Generic.List[string]

function Write-Log {
    param([string]$Message)
    $line = '{0} {1}' -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Message
    $script:LogLines.Add($line)
    Write-Host $line
    try {
        Add-Content -LiteralPath $LogPath -Value $line -Encoding UTF8
    }
    catch {
        # A read-only temp directory must not defeat the install.
    }
}

function Get-LogTail {
    param([int]$Count = 20)
    $start = [Math]::Max(0, $script:LogLines.Count - $Count)
    return ($script:LogLines.GetRange($start, $script:LogLines.Count - $start) -join [Environment]::NewLine)
}

function Invoke-Native {
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$Arguments = @()
    )
    Write-Log ("run: {0} {1}" -f $FilePath, ($Arguments -join ' '))
    # Windows PowerShell turns a native command's stderr into a terminating
    # error when 2>&1 runs under ErrorActionPreference=Stop. The helper's own
    # progress lines (Client autostart, for one) go to stderr, so the install
    # would report failure even though the command succeeded.
    $previous = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try {
        $output = & $FilePath @Arguments 2>&1
        $code = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previous
    }
    foreach ($line in @($output)) {
        Write-Log ([string]$line)
    }
    return $code
}

function Initialize-Config {
    if ((Test-Path -LiteralPath $ConfigPath) -and -not $ForceConfig) {
        Write-Log "keeping existing configuration at $ConfigPath"
        return
    }
    if (-not $TemplatePath -or -not (Test-Path -LiteralPath $TemplatePath)) {
        Write-Log 'no configuration template was shipped; leaving the default config absent'
        return
    }
    $directory = Split-Path -Parent $ConfigPath
    New-Item -ItemType Directory -Force -Path $directory | Out-Null
    Copy-Item -LiteralPath $TemplatePath -Destination $ConfigPath -Force
    Write-Log "wrote configuration template to $ConfigPath"
}

function Invoke-ConfigMigration {
    $client = Join-Path $AppDir 'bin\kitsune-client.exe'
    if (-not (Test-Path -LiteralPath $client)) {
        Write-Log 'the Client is not present; skipping configuration migration'
        return
    }
    if (-not (Test-Path -LiteralPath $ConfigPath)) {
        Write-Log 'no existing configuration; skipping migration'
        return
    }
    $code = Invoke-Native $client @('migrate-config', '--config', $ConfigPath)
    if ($code -ne 0) {
        Write-Log "warning: configuration migration failed (exit $code); the Client still applies defaults"
    }
}

function Add-BinToUserPath {
    $bin = Join-Path $AppDir 'bin'
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @()
    if ($userPath) {
        $entries = @($userPath -split ';' | Where-Object { $_ })
    }
    if ($entries -notcontains $bin) {
        $entries += $bin
        [Environment]::SetEnvironmentVariable('Path', ($entries -join ';'), 'User')
        Write-Log "added $bin to the user PATH"
    }
}

function Remove-BinFromUserPath {
    $bin = Join-Path $AppDir 'bin'
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $userPath) {
        return
    }
    $kept = @($userPath -split ';' | Where-Object { $_ -and $_ -ne $bin })
    if ($kept.Count -ne @($userPath -split ';' | Where-Object { $_ }).Count) {
        [Environment]::SetEnvironmentVariable('Path', ($kept -join ';'), 'User')
        Write-Log "removed $bin from the user PATH"
    }
}

function Get-TarExecutable {
    $candidate = Join-Path $env:SystemRoot 'System32\tar.exe'
    if (Test-Path -LiteralPath $candidate) {
        return $candidate
    }
    return 'tar.exe'
}

function Install-ServerBundle {
    if (-not $ServerArtifact) {
        Write-Log 'no Server artifact was supplied; leaving the Client ready for a Remote Server'
        return
    }
    if (-not (Test-Path -LiteralPath $ServerArtifact)) {
        throw "the Server bundle is missing from the installer payload: $ServerArtifact"
    }

    $stage = Join-Path ([System.IO.Path]::GetTempPath()) ('kitsune-server-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $stage | Out-Null
    # The Server bundle replaces lib/ wholesale, while the optional CUDA runtime
    # lives inside lib/. Keep a previously downloaded runtime so an in-place
    # upgrade of a host with server.device: cuda does not leave a Server that
    # cannot start. The Inno CUDA task cannot be relied on here: checkedonce
    # keeps a user's earlier uncheck, and a failed download is only a warning.
    $cublas = Join-Path $AppDir 'Server\lib\nvidia\cublas\bin\cublas64_12.dll'
    $preserve = Join-Path ([System.IO.Path]::GetTempPath()) ('kitsune-nvidia-' + [guid]::NewGuid().ToString('N'))
    $preserved = $false
    try {
        if (Test-Path -LiteralPath $cublas) {
            try {
                New-Item -ItemType Directory -Force -Path $preserve | Out-Null
                Copy-Item -LiteralPath (Join-Path $AppDir 'Server\lib\nvidia') `
                    -Destination (Join-Path $preserve 'nvidia') -Recurse -Force
                $preserved = $true
                Write-Log 'preserving the CUDA runtime from the previous installation'
            }
            catch {
                Write-Log "warning: the CUDA runtime could not be preserved: $_"
            }
        }

        $tar = Get-TarExecutable
        $code = Invoke-Native $tar @('-xzf', $ServerArtifact, '-C', $stage)
        if ($code -ne 0) {
            throw "could not extract the Server bundle (exit $code). The release artifact may be corrupt."
        }

        $python = Join-Path $stage 'runtime\python.exe'
        $pythonw = Join-Path $stage 'runtime\pythonw.exe'
        $bootstrap = Join-Path $stage 'bootstrap.py'
        foreach ($required in @($python, $pythonw, $bootstrap)) {
            if (-not (Test-Path -LiteralPath $required)) {
                throw "the Server bundle is incomplete: $required is missing. Rebuild the artifact with the Windows runtime."
            }
        }
        # Registration runs the bundle's pythonw.exe, so a runtime without it
        # would open a console window at every logon instead of staying hidden.
        Write-Log 'the Server supervisor will run the bundle pythonw.exe (hidden, no console window)'

        $arguments = @('-s', $bootstrap, 'install', '--artifact', $ServerArtifact, '--sha256', $ServerSha256)
        if (Test-Path -LiteralPath $ConfigPath) {
            $arguments += @('--config', $ConfigPath)
        }
        $code = Invoke-Native $python $arguments
        if ($code -ne 0) {
            $tail = Get-LogTail
            if ($tail -match 'server\.port\s+(\d+)\s+is already in use') {
                $port = $Matches[1]
                throw ("the Local Server cannot start because server.port $port is already in use. " +
                    "Stop the process using that port, or change server.port in $ConfigPath, then run the installer again. " +
                    "The Server never picks a random port.")
            }
            throw ("the Local Server bundle could not be installed (exit $code). " +
                "Fix the error above and run the installer again, or use a Remote Server. " +
                $tail)
        }

        if ($preserved -and -not (Test-Path -LiteralPath $cublas)) {
            $lib = Join-Path $AppDir 'Server\lib'
            New-Item -ItemType Directory -Force -Path $lib | Out-Null
            Copy-Item -LiteralPath (Join-Path $preserve 'nvidia') -Destination $lib -Recurse -Force
            Write-Log 'restored the CUDA runtime into the upgraded Server bundle'
        }
    }
    finally {
        Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath $preserve -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Test-NvidiaGpu {
    try {
        $adapters = Get-CimInstance Win32_VideoController -ErrorAction Stop |
            Where-Object { $_.Name -like '*NVIDIA*' }
        return [bool]$adapters
    }
    catch {
        Write-Log "could not query the video adapters: $_"
        return $false
    }
}

function Install-CudaRuntime {
    if (-not $Cuda) {
        return
    }
    $python = Join-Path $AppDir 'Server\runtime\python.exe'
    if (-not (Test-Path -LiteralPath $python)) {
        throw 'CUDA was requested, but the Server bundle is not installed'
    }
    if (-not (Test-NvidiaGpu)) {
        Write-Log 'CUDA was requested, but no NVIDIA GPU was detected; keeping CPU'
        return
    }
    $cublas = Join-Path $AppDir 'Server\lib\nvidia\cublas\bin\cublas64_12.dll'
    if (Test-Path -LiteralPath $cublas) {
        Write-Log 'the CUDA runtime is already installed'
        return
    }

    # The wheel is ~550 MB and CTranslate2 loads it lazily on the first
    # transcription. Stop the Server so the new DLLs are picked up on restart;
    # a failed download must leave a working CPU Server, so the caller treats
    # this as a warning.
    $launcher = Join-Path $AppDir 'bin\kitsune-server.cmd'
    $stopped = $false
    try {
        if (Test-Path -LiteralPath $launcher) {
            $null = Invoke-Native $launcher @('stop')
            $stopped = $true
        }
        $target = Join-Path $AppDir 'Server\lib'
        $code = Invoke-Native $python @(
            '-m', 'pip', 'install', '--disable-pip-version-check',
            '--no-warn-script-location', '--no-input', '--upgrade',
            '--target', $target, 'nvidia-cublas-cu12'
        )
        if ($code -ne 0) {
            throw "the CUDA runtime could not be downloaded (exit $code). The Server still works on CPU."
        }
        if (-not (Test-Path -LiteralPath $cublas)) {
            throw 'the CUDA runtime installed without cublas64_12.dll. The Server will use CPU.'
        }
        Write-Log 'installed the CUDA runtime (nvidia-cublas-cu12)'
    }
    finally {
        if ($stopped -and (Test-Path -LiteralPath $launcher)) {
            # `start` runs the Server in the foreground; the supervisor's
            # restart returns after Task Scheduler accepts the start.
            $null = Invoke-Native $launcher @('restart')
        }
    }
}

function Register-ClientAutostart {
    $client = Join-Path $AppDir 'bin\kitsune-client.exe'
    if (-not (Test-Path -LiteralPath $client)) {
        throw "the Client executable is missing: $client"
    }
    $code = Invoke-Native $client @('install-autostart')
    if ($code -ne 0) {
        throw "the Client installed but its logon task could not be registered (exit $code)."
    }
    Write-Log 'registered the kitsune-client logon task'
}

function Start-Client {
    $client = Join-Path $AppDir 'bin\kitsune-client.exe'
    if (-not (Test-Path -LiteralPath $client)) {
        return
    }
    try {
        # Starting from the helper (not the Inno postinstall page) means a
        # silent one-line install also leaves the Tray running and dictation
        # available immediately.
        Start-Process -FilePath $client `
            -ArgumentList @('gui', '--hidden') `
            -WorkingDirectory (Split-Path -Parent $client) `
            -WindowStyle Hidden | Out-Null
        Write-Log 'started the Client in the Tray'
    }
    catch {
        Write-Log "warning: the Client could not be started now: $_"
    }
}

function Uninstall-Autostart {
    # A running Client holds its executable open, so stop the logon task and any
    # manual `gui` instance before Inno Setup removes the files.
    & schtasks.exe /End /TN 'kitsune-client' 2>&1 | Out-Null
    Get-Process -Name 'kitsune-client' -ErrorAction SilentlyContinue |
        Stop-Process -Force -ErrorAction SilentlyContinue

    $client = Join-Path $AppDir 'bin\kitsune-client.exe'
    if (Test-Path -LiteralPath $client) {
        $code = Invoke-Native $client @('uninstall-autostart')
        if ($code -ne 0) {
            Write-Log "warning: the Client autostart task could not be removed (exit $code)"
        }
    }

    $launcher = Join-Path $AppDir 'bin\kitsune-server.cmd'
    if (Test-Path -LiteralPath $launcher) {
        $code = Invoke-Native $launcher @('uninstall')
        if ($code -eq 0) {
            return
        }
        Write-Log "warning: the Server bundle could not be removed cleanly (exit $code)"
    }

    # The launcher is gone when a previous install failed early, or its
    # uninstall failed. Remove the task directly rather than leaving it to fire.
    foreach ($task in @('kitsune-client', 'kitsune-server')) {
        & schtasks.exe /End /TN $task 2>&1 | Out-Null
        & schtasks.exe /Delete /TN $task /F 2>&1 | Out-Null
    }
}

function Remove-Models {
    $models = Join-Path $env:USERPROFILE '.cache\kitsune-whisper\models'
    if (Test-Path -LiteralPath $models) {
        Remove-Item -LiteralPath $models -Recurse -Force -ErrorAction SilentlyContinue
        Write-Log "removed $models"
    }
}

function Reset-LocalPairing {
    $client = Join-Path $AppDir 'bin\kitsune-client.exe'
    if (-not (Test-Path -LiteralPath $client) -or -not (Test-Path -LiteralPath $ConfigPath)) {
        return
    }
    $code = Invoke-Native $client @('reset-local-pairing', '--config', $ConfigPath)
    if ($code -ne 0) {
        Write-Log "warning: the Local Server pairing could not be reset; re-pair it after reinstall"
    }
}

function Remove-ServerData {
    $state = Join-Path $env:LOCALAPPDATA 'kitsune-whisper'
    foreach ($name in @('tls', 'devices.json', 'server.log', 'lifecycle.json')) {
        $target = Join-Path $state $name
        if (Test-Path -LiteralPath $target) {
            Remove-Item -LiteralPath $target -Recurse -Force -ErrorAction SilentlyContinue
            Write-Log "removed $target"
        }
    }
}

function Remove-UserData {
    Write-Log 'purging configuration, models, Devices, certificates, logs, and Spool'
    $targets = @(
        (Join-Path $env:APPDATA 'kitsune-whisper'),
        (Join-Path $env:USERPROFILE '.cache\kitsune-whisper')
    )
    foreach ($target in $targets) {
        if (Test-Path -LiteralPath $target) {
            Remove-Item -LiteralPath $target -Recurse -Force -ErrorAction SilentlyContinue
            Write-Log "removed $target"
        }
    }

    # The uninstaller runs from AppDir, so delete its contents rather than the
    # directory itself. The uninstaller files (unins*.exe/.dat) stay until Inno
    # Setup removes them after this helper returns.
    if (Test-Path -LiteralPath $AppDir) {
        Get-ChildItem -LiteralPath $AppDir -Force |
            Where-Object { $_.Name -notlike 'unins*' } |
            ForEach-Object {
                Remove-Item -LiteralPath $_.FullName -Recurse -Force -ErrorAction SilentlyContinue
                Write-Log "removed $($_.FullName)"
            }
    }
}

function Install-Kitsune {
    $failures = New-Object System.Collections.Generic.List[string]
    New-Item -ItemType Directory -Force -Path (Join-Path $AppDir 'bin') | Out-Null

    try {
        Initialize-Config
        Invoke-ConfigMigration
    }
    catch {
        $failures.Add("configuration: $_")
        Write-Log "error: configuration: $_"
    }

    try {
        Install-ServerBundle
    }
    catch {
        $failures.Add("local Server: $_")
        Write-Log "error: local Server: $_"
    }

    try {
        Install-CudaRuntime
    }
    catch {
        # GPU is an optional acceleration: a failed download falls back to the
        # CPU Server instead of failing the whole install.
        Write-Log "warning: CUDA runtime: $_"
    }

    try {
        Register-ClientAutostart
    }
    catch {
        $failures.Add("client autostart: $_")
        Write-Log "error: client autostart: $_"
    }

    try {
        Add-BinToUserPath
    }
    catch {
        $failures.Add("user PATH: $_")
        Write-Log "error: user PATH: $_"
    }

    Start-Client

    if ($failures.Count -gt 0) {
        return 1
    }
    Write-Log 'install completed: Client, Server bundle, and both logon tasks are ready'
    return 0
}

function Uninstall-Kitsune {
    Uninstall-Autostart
    Remove-BinFromUserPath
    if ($Purge) {
        Remove-UserData
    }
    else {
        if ($RemoveModels) {
            Remove-Models
        }
        if ($RemoveServerData) {
            Reset-LocalPairing
            Remove-ServerData
        }
        Write-Log 'keeping configuration, Spool, wizard progress, and Device tokens unless /PURGE=1 was passed'
        if (-not $RemoveModels) {
            Write-Log 'downloaded models were kept (use /REMOVEMODELS=1 to remove them)'
        }
        if (-not $RemoveServerData) {
            Write-Log 'Server certificates, Devices, and logs were kept (use /REMOVESERVERDATA=1 to remove them)'
        }
    }
    Write-Log 'uninstall completed'
    return 0
}

try {
    if ($Action -eq 'install') {
        exit (Install-Kitsune)
    }
    exit (Uninstall-Kitsune)
}
catch {
    Write-Log "error: $_"
    exit 1
}
