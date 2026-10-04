#!/usr/bin/env pwsh
#requires -Version 7
<#
.SYNOPSIS
Installs or removes the kitsune-whisper Client on Windows.

.DESCRIPTION
Downloads the release archive, verifies its SHA-256 checksum, installs
kitsune-client.exe under %LOCALAPPDATA%\kitsune-whisper\bin, writes a config
template to %APPDATA%\kitsune-whisper\kitsune.yaml (only when absent), adds the
binary directory to the user PATH, and registers a per-user Task Scheduler task
at logon. Re-run it to upgrade; the config is left untouched.

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
$InstallDir = Join-Path $env:LOCALAPPDATA 'kitsune-whisper\bin'
$Binary = Join-Path $InstallDir 'kitsune-client.exe'
$ConfigDir = Join-Path $env:APPDATA 'kitsune-whisper'
$Config = Join-Path $ConfigDir 'kitsune.yaml'
$AppDir = Join-Path $env:LOCALAPPDATA 'kitsune-whisper'
$Asset = 'kitsune-client_windows_amd64.zip'

if (-not [Environment]::Is64BitOperatingSystem) {
    throw 'kitsune-client releases are built for 64-bit Windows only'
}

if ($Uninstall) {
    if (Test-Path -LiteralPath $Binary) {
        try { & $Binary uninstall-autostart } catch { Write-Warning "could not remove autostart: $_" }
    }
    Remove-Item -LiteralPath $Binary -Force -ErrorAction SilentlyContinue
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($userPath) {
        $kept = @($userPath -split ';' | Where-Object { $_ -ne '' -and $_ -ne $InstallDir })
        [Environment]::SetEnvironmentVariable('Path', ($kept -join ';'), 'User')
    }
    if ($Purge) {
        Remove-Item -LiteralPath $ConfigDir -Recurse -Force -ErrorAction SilentlyContinue
        Remove-Item -LiteralPath $AppDir -Recurse -Force -ErrorAction SilentlyContinue
        Write-Host 'kitsune-client uninstalled (config, spool, and logs purged)'
    }
    else {
        Write-Host 'kitsune-client uninstalled (config, spool, and logs kept)'
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
    $Archive = Join-Path $Temp $Asset
    $Checksums = Join-Path $Temp 'checksums.txt'

    Write-Host "Downloading $Asset ($Version)..."
    Invoke-WebRequest -Uri "$Base/$Asset" -OutFile $Archive
    Invoke-WebRequest -Uri "$Base/checksums.txt" -OutFile $Checksums

    $expected = $null
    foreach ($line in Get-Content -LiteralPath $Checksums) {
        $parts = -split $line
        if ($parts.Length -ge 2 -and $parts[-1] -eq $Asset) { $expected = $parts[0]; break }
    }
    if (-not $expected) { throw "no checksum for $Asset in checksums.txt" }
    $actual = (Get-FileHash -LiteralPath $Archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($actual -ne $expected.ToLowerInvariant()) {
        throw "checksum mismatch for ${Asset}: expected $expected, got $actual"
    }

    Expand-Archive -LiteralPath $Archive -DestinationPath $Temp -Force
    New-Item -ItemType Directory -Force -Path $InstallDir | Out-Null
    Copy-Item -LiteralPath (Join-Path $Temp 'kitsune-client.exe') -Destination $Binary -Force

    New-Item -ItemType Directory -Force -Path $ConfigDir | Out-Null
    if ((-not (Test-Path -LiteralPath $Config)) -or $ForceConfig) {
        $template = Join-Path $Temp 'kitsune.example.yaml'
        if (Test-Path -LiteralPath $template) {
            Copy-Item -LiteralPath $template -Destination $Config -Force
            Write-Host "Wrote config template to $Config"
        }
    }

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (($userPath -split ';') -notcontains $InstallDir) {
        $newPath = if ([string]::IsNullOrEmpty($userPath)) { $InstallDir } else { "$userPath;$InstallDir" }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        Write-Host "Added $InstallDir to your user PATH"
    }
}
finally {
    Remove-Item -LiteralPath $Temp -Recurse -Force -ErrorAction SilentlyContinue
}

try {
    & $Binary install-autostart
    Write-Host 'Registered the kitsune-client Task Scheduler task'
}
catch {
    Write-Warning "installed the client but could not register autostart: $_"
    Write-Warning "re-run '$Binary install-autostart' inside your desktop session"
}

Write-Host "Installed $Binary"
