#!/usr/bin/env pwsh
#requires -Version 7
<#
.SYNOPSIS
Installs, upgrades, or removes kitsune-whisper on Windows without elevation.

.DESCRIPTION
Downloads the release's Client archive and (unless -ClientOnly) the native
Server bundle, verifies both against checksums.txt, and installs them under the
current user's own programs directory:

  %LOCALAPPDATA%\Programs\kitsune-whisper\bin      Client and Server launcher
  %LOCALAPPDATA%\Programs\kitsune-whisper\Server   native Server bundle
  %LOCALAPPDATA%\Programs\kitsune-whisper\uninstall.ps1

The installer writes an Apps & Features entry, adds the bin directory to the
user PATH, and stops there. It never asks for administrator rights, never
starts the Client or the Server, and never enables a logon task: the Server's
Task Scheduler registration is created disabled, so `schtasks /Run` starts it on
demand but nothing starts at login.

Re-run the script to upgrade in place. Configuration in
%APPDATA%\kitsune-whisper and models are kept. An existing CUDA runtime
directory (Server\lib\nvidia) is preserved across an upgrade.

The Microsoft Edge WebView2 Runtime is not installed or downloaded. When it is
missing, the installer warns and points at the documented manual install.

One-liner:

  irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1 | iex

.EXAMPLE
./install.ps1
./install.ps1 -Version v1.2.3
./install.ps1 -ClientOnly
./install.ps1 -ServerOnly
./install.ps1 -ForceConfig
./install.ps1 -DryRun -From .\dist
./install.ps1 -Uninstall
#>
[CmdletBinding()]
param(
    [string]$Version = $(if ($env:KITSUNE_VERSION) { $env:KITSUNE_VERSION } else { 'latest' }),
    [switch]$Uninstall,
    [switch]$ClientOnly,
    [switch]$ServerOnly,
    [switch]$ForceConfig,
    [switch]$DryRun,
    [string]$From = $(if ($env:KITSUNE_ARTIFACTS_DIR) { $env:KITSUNE_ARTIFACTS_DIR } else { '' })
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$ProgressPreference = 'SilentlyContinue'

$Repo = 'FoxRed-cmd/kitsune-whisper'
$ClientAsset = 'kitsune-client_windows_amd64.zip'
$ServerAsset = 'kitsune-server_windows_amd64.tar.gz'

# The per-user programs convention, not a machine-wide directory.
$InstallRoot = Join-Path $env:LOCALAPPDATA 'Programs\kitsune-whisper'
$BinDir = Join-Path $InstallRoot 'bin'
$ServerRoot = Join-Path $InstallRoot 'Server'
$UninstallShim = Join-Path $InstallRoot 'uninstall.ps1'
$ConfigPath = Join-Path $env:APPDATA 'kitsune-whisper\kitsune.yaml'
$StartMenuShortcut = Join-Path $env:APPDATA `
    'Microsoft\Windows\Start Menu\Programs\kitsune-whisper.lnk'
$UninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\kitsune-whisper'
$WebView2Client = '{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}'
$WebView2Help = 'https://developer.microsoft.com/microsoft-edge/webview2/'
$InstallHelp = "https://github.com/$Repo/blob/main/docs/install.md#windows"

$Temp = ''

function Write-Step {
    param([string]$Message)
    Write-Host "kitsune-whisper: $Message"
}

function Get-BaseUrl {
    if ($Version -eq 'latest') {
        return "https://github.com/$Repo/releases/latest/download"
    }
    return "https://github.com/$Repo/releases/download/$Version"
}

function ConvertTo-Argument {
    param([string]$Value)
    if ($Value -ne '' -and $Value -notmatch '[\s"]') {
        return $Value
    }
    return '"' + $Value.Replace('"', '\"') + '"'
}

function Invoke-Native {
    # Start-Process waits for GUI-subsystem binaries too. The call operator
    # does not, and $LASTEXITCODE is then unset or stale.
    param(
        [Parameter(Mandatory = $true)][string]$FilePath,
        [string[]]$Arguments = @()
    )
    $argumentLine = (@($Arguments | ForEach-Object { ConvertTo-Argument $_ }) -join ' ')
    $process = Start-Process -FilePath $FilePath -ArgumentList $argumentLine `
        -Wait -PassThru -NoNewWindow
    return $process.ExitCode
}

function Get-ChecksumFor {
    param(
        [Parameter(Mandatory = $true)][string]$ChecksumsPath,
        [Parameter(Mandatory = $true)][string]$Asset
    )
    $line = Get-Content -LiteralPath $ChecksumsPath |
        Where-Object { $_ -and (($_ -split '\s+')[-1] -replace '^\*', '') -eq $Asset } |
        Select-Object -First 1
    if (-not $line) {
        throw "no checksum for $Asset in checksums.txt"
    }
    return (($_ -split '\s+')[0]).ToLowerInvariant()
}

function Assert-Checksum {
    param(
        [Parameter(Mandatory = $true)][string]$Path,
        [Parameter(Mandatory = $true)][string]$Asset,
        [Parameter(Mandatory = $true)][string]$ChecksumsPath
    )
    $expected = Get-ChecksumFor -ChecksumsPath $ChecksumsPath -Asset $Asset
    $actual = (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected) {
        throw "checksum mismatch for ${Asset}: expected $expected, got $actual"
    }
}

function Get-ReleaseAsset {
    param(
        [Parameter(Mandatory = $true)][string]$Asset,
        [Parameter(Mandatory = $true)][string]$ChecksumsPath,
        [Parameter(Mandatory = $true)][string]$Destination
    )
    $target = Join-Path $Destination $Asset
    if ($From) {
        $source = Join-Path $From $Asset
        if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
            throw "missing release artifact: $source"
        }
        Copy-Item -LiteralPath $source -Destination $target -Force
    }
    else {
        Write-Step "downloading $Asset ($Version)"
        Invoke-WebRequest -Uri "$(Get-BaseUrl)/$Asset" -OutFile $target -UseBasicParsing
    }
    Assert-Checksum -Path $target -Asset $Asset -ChecksumsPath $ChecksumsPath
    return $target
}

# -- install steps ---------------------------------------------------------

function Install-Config {
    param([string]$Template)
    if (-not $Template -or -not (Test-Path -LiteralPath $Template)) {
        Write-Step 'no kitsune.example.yaml was shipped; leaving the default config absent'
        return
    }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $ConfigPath) | Out-Null
    if ((Test-Path -LiteralPath $ConfigPath) -and -not $ForceConfig) {
        Write-Step "keeping existing configuration at $ConfigPath"
        return
    }
    Copy-Item -LiteralPath $Template -Destination $ConfigPath -Force
    Write-Step "wrote $ConfigPath"
}

function Install-Client {
    param([string]$Archive)
    $stage = Join-Path $Temp 'client'
    Expand-Archive -LiteralPath $Archive -DestinationPath $stage -Force
    $executable = Join-Path $stage 'kitsune-client.exe'
    if (-not (Test-Path -LiteralPath $executable -PathType Leaf)) {
        throw "$([System.IO.Path]::GetFileName($Archive)) does not contain kitsune-client.exe"
    }
    New-Item -ItemType Directory -Force -Path $BinDir | Out-Null
    Copy-Item -LiteralPath $executable -Destination (Join-Path $BinDir 'kitsune-client.exe') -Force
    Write-Step "installed $(Join-Path $BinDir 'kitsune-client.exe')"
    Install-Config (Join-Path $stage 'kitsune.example.yaml')
}

function Invoke-ConfigMigration {
    $client = Join-Path $BinDir 'kitsune-client.exe'
    if (-not (Test-Path -LiteralPath $client) -or -not (Test-Path -LiteralPath $ConfigPath)) {
        return
    }
    $code = Invoke-Native -FilePath $client `
        -Arguments @('migrate-config', '--config', $ConfigPath)
    if ($code -ne 0) {
        Write-Warning "configuration migration failed (exit $code); the Client still applies defaults"
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
    param([string]$Archive, [string]$Sha256)

    $stage = Join-Path $Temp 'server'
    New-Item -ItemType Directory -Force -Path $stage | Out-Null
    $code = Invoke-Native -FilePath (Get-TarExecutable) `
        -Arguments @('-xzf', $Archive, '-C', $stage)
    if ($code -ne 0) {
        throw "could not extract the Server bundle (exit $code). The release artifact may be corrupt."
    }

    $python = Join-Path $stage 'runtime\python.exe'
    $pythonw = Join-Path $stage 'runtime\pythonw.exe'
    $bootstrap = Join-Path $stage 'bootstrap.py'
    foreach ($required in @($python, $pythonw, $bootstrap)) {
        if (-not (Test-Path -LiteralPath $required -PathType Leaf)) {
            throw "the Server bundle is incomplete: $required is missing. Rebuild the artifact with the Windows runtime."
        }
    }

    $arguments = @(
        '-s', $bootstrap, 'install',
        '--artifact', $Archive,
        '--sha256', $Sha256,
        '--no-start'
    )
    if (Test-Path -LiteralPath $ConfigPath) {
        $arguments += @('--config', $ConfigPath)
    }
    # --no-start installs the bundle and registers the supervisor with its
    # logon trigger disabled. Nothing is enabled, started, or scheduled to run
    # at login; `schtasks /Run /TN kitsune-server` starts it on demand.
    $code = Invoke-Native -FilePath $python -Arguments $arguments
    if ($code -ne 0) {
        throw ("the Server bundle could not be installed (exit $code). " +
            "Fix the error above and run the installer again, or install the Client alone with -ClientOnly.")
    }
    Write-Step "installed the Server bundle in $ServerRoot (registered, not started)"
}

function Install-Server {
    param([string]$Archive, [string]$Sha256)

    # The bundle install replaces lib/ wholesale, while the optional CUDA
    # runtime lives inside lib/. Keep an existing (user-provided or downloaded)
    # runtime so an in-place upgrade of a host with server.device: cuda does not
    # leave a Server that cannot start.
    $cublas = Join-Path $ServerRoot 'lib\nvidia\cublas\bin\cublas64_12.dll'
    $preserve = Join-Path $Temp 'nvidia'
    $preserved = $false
    try {
        if (Test-Path -LiteralPath $cublas) {
            New-Item -ItemType Directory -Force -Path $preserve | Out-Null
            Copy-Item -LiteralPath (Join-Path $ServerRoot 'lib\nvidia') `
                -Destination (Join-Path $preserve 'nvidia') -Recurse -Force
            $preserved = $true
            Write-Step 'preserving the CUDA runtime from the previous installation'
        }

        Install-ServerBundle -Archive $Archive -Sha256 $Sha256

        if ($preserved -and -not (Test-Path -LiteralPath $cublas)) {
            $lib = Join-Path $ServerRoot 'lib'
            New-Item -ItemType Directory -Force -Path $lib | Out-Null
            Copy-Item -LiteralPath (Join-Path $preserve 'nvidia') `
                -Destination $lib -Recurse -Force
            Write-Step 'restored the CUDA runtime into the upgraded Server bundle'
        }
    }
    finally {
        Remove-Item -LiteralPath $preserve -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Add-BinToUserPath {
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    $entries = @()
    if ($userPath) {
        $entries = @($userPath -split ';' | Where-Object { $_ })
    }
    if ($entries | Where-Object { $_ -ieq $BinDir }) {
        return
    }
    $entries += $BinDir
    [Environment]::SetEnvironmentVariable('Path', ($entries -join ';'), 'User')
    Write-Step "added $BinDir to the user PATH"
}

function New-StartMenuShortcut {
    $client = Join-Path $BinDir 'kitsune-client.exe'
    if (-not (Test-Path -LiteralPath $client)) {
        return
    }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $StartMenuShortcut) | Out-Null
    $shell = New-Object -ComObject WScript.Shell
    $shortcut = $shell.CreateShortcut($StartMenuShortcut)
    $shortcut.TargetPath = $client
    $shortcut.Arguments = 'gui'
    $shortcut.WorkingDirectory = $BinDir
    $shortcut.IconLocation = "$client,0"
    $shortcut.Description = 'Private, LAN-local speech-to-text dictation'
    $shortcut.Save()
}

function Write-UninstallShim {
    # The shim lives next to bin/ and Server/, so apps & features never depends
    # on a download or an interactive prompt. #97 adds the purge boundaries.
    $body = @'
#requires -Version 7
# Generated by the kitsune-whisper installer: removes the files and
# registrations this installation created and keeps configuration, models and
# pairing state. It is safe to run non-interactively.
[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$installRoot = $PSScriptRoot
$bin = Join-Path $installRoot 'bin'
$uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\kitsune-whisper'
$shortcut = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\kitsune-whisper.lnk'

Get-Process -Name 'kitsune-client' -ErrorAction SilentlyContinue |
    Stop-Process -Force -ErrorAction SilentlyContinue

$client = Join-Path $bin 'kitsune-client.exe'
if (Test-Path -LiteralPath $client) {
    $null = Start-Process -FilePath $client -ArgumentList @('uninstall-autostart') `
        -Wait -PassThru -NoNewWindow
}

$launcher = Join-Path $bin 'kitsune-server.cmd'
if (Test-Path -LiteralPath $launcher) {
    & $launcher uninstall 2>$null | Out-Null
}
# The launcher is gone when a previous install failed early or its uninstall
# failed; never leave a registration that points at deleted files.
& schtasks.exe /End /TN 'kitsune-server' 2>$null | Out-Null
& schtasks.exe /Delete /TN 'kitsune-server' /F 2>$null | Out-Null

$userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
if ($userPath) {
    $entries = @($userPath -split ';' | Where-Object { $_ })
    $kept = @($entries | Where-Object { $_ -ine $bin })
    if ($kept.Count -ne $entries.Count) {
        [Environment]::SetEnvironmentVariable('Path', ($kept -join ';'), 'User')
    }
}

if (Test-Path -LiteralPath $uninstallKey) {
    Remove-Item -LiteralPath $uninstallKey -Recurse -Force
}
Remove-Item -LiteralPath $shortcut -Force -ErrorAction SilentlyContinue

if (Test-Path -LiteralPath $installRoot) {
    Get-ChildItem -LiteralPath $installRoot -Force -ErrorAction SilentlyContinue |
        Remove-Item -Recurse -Force -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $installRoot -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host 'kitsune-whisper was removed; configuration, models and pairing state were kept.'
'@
    New-Item -ItemType Directory -Force -Path $InstallRoot | Out-Null
    Set-Content -LiteralPath $UninstallShim -Value $body -Encoding utf8NoBOM
}

function Register-UninstallEntry {
    $command = '"{0}" -NoProfile -NonInteractive -ExecutionPolicy Bypass -File "{1}"' -f `
        (Join-Path $PSHOME 'pwsh.exe'), $UninstallShim
    $icon = Join-Path $BinDir 'kitsune-client.exe'
    if (-not (Test-Path -LiteralPath $icon)) {
        $icon = Join-Path $ServerRoot 'runtime\pythonw.exe'
    }

    New-Item -Path $UninstallKey -Force | Out-Null
    Set-ItemProperty -Path $UninstallKey -Name DisplayName -Value 'kitsune-whisper'
    Set-ItemProperty -Path $UninstallKey -Name DisplayVersion -Value (Get-DisplayVersion)
    Set-ItemProperty -Path $UninstallKey -Name Publisher -Value 'FoxRed-cmd'
    Set-ItemProperty -Path $UninstallKey -Name InstallLocation -Value $InstallRoot
    Set-ItemProperty -Path $UninstallKey -Name DisplayIcon -Value $icon
    Set-ItemProperty -Path $UninstallKey -Name UninstallString -Value $command
    Set-ItemProperty -Path $UninstallKey -Name QuietUninstallString -Value $command
    Set-ItemProperty -Path $UninstallKey -Name URLInfoAbout `
        -Value "https://github.com/$Repo"
    Set-ItemProperty -Path $UninstallKey -Name NoModify -Value 1 -Type DWord
    Set-ItemProperty -Path $UninstallKey -Name NoRepair -Value 1 -Type DWord
}

function Get-DisplayVersion {
    if ($Version -ne 'latest') {
        return ($Version -replace '^[vV]', '')
    }
    $manifest = Join-Path $ServerRoot 'bundle.json'
    if (Test-Path -LiteralPath $manifest) {
        try {
            return (Get-Content -LiteralPath $manifest -Raw | ConvertFrom-Json).version
        }
        catch {
            Write-Verbose "could not read $manifest"
        }
    }
    $client = Join-Path $BinDir 'kitsune-client.exe'
    if (Test-Path -LiteralPath $client) {
        $info = (Get-Item -LiteralPath $client).VersionInfo
        if ($info.ProductVersion) {
            return $info.ProductVersion
        }
    }
    return 'unknown'
}

function Test-WebView2Installed {
    $keys = @(
        "HKCU:\Software\Microsoft\EdgeUpdate\Clients\$WebView2Client",
        "HKLM:\SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\$WebView2Client",
        "HKLM:\SOFTWARE\Microsoft\EdgeUpdate\Clients\$WebView2Client"
    )
    foreach ($key in $keys) {
        if (-not (Test-Path -LiteralPath $key)) {
            continue
        }
        $item = Get-ItemProperty -LiteralPath $key -ErrorAction SilentlyContinue
        if ($item -and $item.PSObject.Properties['pv']) {
            $version = $item.pv
            if ($version -and $version -ne '0.0.0.0') {
                return $true
            }
        }
    }
    return $false
}

function Write-WebView2Warning {
    if (Test-WebView2Installed) {
        return
    }
    # Never download the runtime: it is a system component with its own
    # installer and terms, and the fix is documented for the user.
    Write-Warning ("kitsune-whisper needs the Microsoft Edge WebView2 Runtime for " +
        "its Settings window and Tray. Install it once from $WebView2Help " +
        "(see $InstallHelp). The installer does not download or install it.")
}

function Write-InstallPlan {
    $clientPlan = if ($ClientOnly) { 'disabled' } else { Join-Path $BinDir 'kitsune-client.exe' }
    $serverPlan = if ($ServerOnly) { 'disabled' } else { $ServerRoot }
    Write-Host 'kitsune-whisper install plan'
    Write-Host "  version: $Version"
    Write-Host "  client:  $clientPlan"
    Write-Host "  server:  $serverPlan"
    Write-Host "  path:    $BinDir"
    Write-Host "  install: $InstallRoot"
    Write-Host '  starts:  nothing (the Server supervisor registration is disabled)'
    Write-Host 'Nothing has been changed.'
}

function Install-Kitsune {
    if ($From) {
        if (-not (Test-Path -LiteralPath $From -PathType Container)) {
            throw "artifact directory not found: $From"
        }
        $script:From = (Resolve-Path -LiteralPath $From).Path
    }

    if ($DryRun) {
        Write-InstallPlan
        return
    }

    $script:Temp = Join-Path ([System.IO.Path]::GetTempPath()) `
        ('kitsune-install-' + [guid]::NewGuid().ToString('N'))
    New-Item -ItemType Directory -Force -Path $Temp | Out-Null
    try {
        $checksums = Join-Path $Temp 'checksums.txt'
        if ($From) {
            $source = Join-Path $From 'checksums.txt'
            if (-not (Test-Path -LiteralPath $source -PathType Leaf)) {
                throw "missing release artifact: $source"
            }
            Copy-Item -LiteralPath $source -Destination $checksums -Force
        }
        else {
            Write-Step "downloading checksums.txt ($Version)"
            Invoke-WebRequest -Uri "$(Get-BaseUrl)/checksums.txt" -OutFile $checksums -UseBasicParsing
        }

        # Trust seam: #97 verifies checksums.txt.sig with .NET ECDsa here,
        # against the same checksums consumed below. Until then HTTPS plus these
        # SHA-256 checks are the only integrity checks.
        $assets = @()
        if (-not $ServerOnly) {
            $assets += $ClientAsset
        }
        if (-not $ClientOnly) {
            $assets += $ServerAsset
        }
        $paths = @{}
        foreach ($asset in $assets) {
            $paths[$asset] = Get-ReleaseAsset -Asset $asset -ChecksumsPath $checksums `
                -Destination $Temp
        }

        if (-not $ServerOnly) {
            Install-Client -Archive $paths[$ClientAsset]
        }
        if (-not $ClientOnly) {
            $serverSha = Get-ChecksumFor -ChecksumsPath $checksums -Asset $ServerAsset
            Install-Server -Archive $paths[$ServerAsset] -Sha256 $serverSha
        }

        Invoke-ConfigMigration
        Add-BinToUserPath
        Write-UninstallShim
        Register-UninstallEntry
        New-StartMenuShortcut
        Write-WebView2Warning

        Write-Step "installed to $InstallRoot"
        Write-Step 'nothing was started; the Server task is registered disabled'
        Write-Step 'run install.ps1 -Uninstall or use Apps & features to remove it'
    }
    finally {
        Remove-Item -LiteralPath $Temp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Uninstall-Kitsune {
    if (-not (Test-Path -LiteralPath $UninstallShim -PathType Leaf)) {
        throw "kitsune-whisper is not installed (no uninstall shim at $UninstallShim)"
    }
    $pwsh = Join-Path $PSHOME 'pwsh.exe'
    & $pwsh -NoProfile -NonInteractive -ExecutionPolicy Bypass -File $UninstallShim
    if ($LASTEXITCODE -ne 0) {
        throw "the uninstaller failed with exit code $LASTEXITCODE"
    }
}

if (-not $env:LOCALAPPDATA) {
    throw 'kitsune-whisper releases are built for 64-bit Windows; run this script on Windows.'
}
if ($ClientOnly -and $ServerOnly) {
    throw '-ClientOnly and -ServerOnly are mutually exclusive'
}

if ($Uninstall) {
    Uninstall-Kitsune
}
else {
    Install-Kitsune
}
