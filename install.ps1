#!/usr/bin/env pwsh
#requires -Version 7
<#
.SYNOPSIS
Installs, upgrades, or removes kitsune-whisper on Windows without elevation.

.DESCRIPTION
Downloads the per-user setup executable from the release, verifies its SHA-256
against checksums.txt, and runs it. The setup installs the Client with its Tray
and Settings window, the native Server bundle, WebView2 when missing, the Start
Menu entry, and both per-user logon tasks.

Re-run it to upgrade. Existing configuration, models, Devices, certificates, and
logs are kept. Use -Uninstall to remove the binaries and tasks; add -Purge to
remove user data too.

One-liner:

  irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1 | iex

.EXAMPLE
./install.ps1
./install.ps1 -Version v1.2.3
./install.ps1 -Uninstall
./install.ps1 -Uninstall -Purge
#>
[CmdletBinding()]
param(
    [string]$Version = $(if ($env:KITSUNE_VERSION) { $env:KITSUNE_VERSION } else { 'latest' }),
    [switch]$Uninstall,
    [switch]$Purge,
    [switch]$ForceConfig
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Repo = 'FoxRed-cmd/kitsune-whisper'
$Asset = 'kitsune-whisper_windows_amd64_setup.exe'
$InstallDir = Join-Path $env:LOCALAPPDATA 'kitsune-whisper'
$Uninstaller = Join-Path $InstallDir 'unins000.exe'

if (-not [Environment]::Is64BitOperatingSystem) {
    throw 'kitsune-whisper releases are built for 64-bit Windows only'
}

if ($Uninstall) {
    if (-not (Test-Path -LiteralPath $Uninstaller)) {
        throw "kitsune-whisper is not installed (no uninstaller at $Uninstaller)"
    }
    $arguments = @('/SILENT', '/SUPPRESSMSGBOXES', '/NORESTART')
    if ($Purge) {
        $arguments += '/PURGE=1'
    }
    $process = Start-Process -FilePath $Uninstaller -ArgumentList $arguments -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw "the uninstaller failed with exit code $($process.ExitCode)"
    }
    if ($Purge) {
        Write-Host 'kitsune-whisper uninstalled (configuration, models, Devices, certificates, logs, and Spool purged)'
    }
    else {
        Write-Host 'kitsune-whisper uninstalled (configuration, models, Devices, certificates, logs, and Spool kept)'
    }
    return
}

if ($Version -eq 'latest') {
    $Base = "https://github.com/$Repo/releases/latest/download"
}
else {
    $Base = "https://github.com/$Repo/releases/download/$Version"
}

$Temp = Join-Path ([System.IO.Path]::GetTempPath()) ("kitsune-install-" + [guid]::NewGuid())
New-Item -ItemType Directory -Force -Path $Temp | Out-Null
try {
    $Setup = Join-Path $Temp $Asset
    $Checksums = Join-Path $Temp 'checksums.txt'

    Write-Host "Downloading $Asset ($Version)..."
    Invoke-WebRequest -Uri "$Base/$Asset" -OutFile $Setup
    Invoke-WebRequest -Uri "$Base/checksums.txt" -OutFile $Checksums

    $expected = $null
    foreach ($line in Get-Content -LiteralPath $Checksums) {
        $parts = -split $line
        if ($parts.Length -ge 2 -and $parts[-1] -eq $Asset) { $expected = $parts[0]; break }
    }
    if (-not $expected) { throw "no checksum for $Asset in checksums.txt" }
    $actual = (Get-FileHash -LiteralPath $Setup -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected.ToLowerInvariant()) {
        throw "checksum mismatch for ${Asset}: expected $expected, got $actual"
    }

    Write-Host 'Running the per-user installer...'
    $arguments = @('/SILENT', '/NORESTART')
    if ($ForceConfig) {
        $arguments += '/FORCECONFIG=1'
    }
    $process = Start-Process -FilePath $Setup -ArgumentList $arguments -Wait -PassThru
    if ($process.ExitCode -ne 0) {
        throw ("the installer exited with code $($process.ExitCode). " +
            'If this machine is offline, install the WebView2 Evergreen Runtime from ' +
            'https://developer.microsoft.com/microsoft-edge/webview2/ and run this command again. ' +
            "Run $Setup interactively to see the exact error.")
    }
}
finally {
    Remove-Item -LiteralPath $Temp -Recurse -Force -ErrorAction SilentlyContinue
}

Write-Host 'Installed kitsune-whisper. Press Ctrl+Shift+Space to dictate.'
