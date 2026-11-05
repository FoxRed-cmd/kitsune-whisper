#!/usr/bin/env pwsh
#requires -Version 7
<#
.SYNOPSIS
Builds the Windows Client executable with a GUI subsystem, its icon, and its
version resources.

.DESCRIPTION
The Client links cgo (malgo), so Go selects the external linker on Windows and
`-H=windowsgui` is ignored there; the subsystem has to come from the C linker
instead. Without it the executable keeps the console subsystem, and Explorer
opens a console window that mirrors the Client's log.

The icon and the version block are generated from the SVG brand asset into a
temporary .syso file, so the repository keeps no binary artwork. Both temporary
files are removed afterwards.

Set CC when no gcc is on PATH (for example, `$env:CC = 'zig cc'`).

.EXAMPLE
./packaging/windows/build-client.ps1 -Version v1.0.0
#>
[CmdletBinding()]
param(
    [string]$Version = 'dev',
    [string]$Output = (Join-Path (Get-Location) 'dist\kitsune-client.exe'),
    [string]$RepoRoot = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path,
    [switch]$SkipIcon,
    # Build a console-subsystem executable so CI can capture --version output.
    # Release builds keep the default GUI subsystem.
    [switch]$Console
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$ClientDir = Join-Path $RepoRoot 'client'
$PackageDir = Join-Path $ClientDir 'cmd\kitsune-client'
$Icon = Join-Path $PSScriptRoot 'app.ico'
$Resource = Join-Path $PackageDir 'rsrc_windows_amd64.syso'

# Resolve the output before changing directory, so a relative -Output keeps
# meaning "relative to the caller" (the release workflow passes dist\...).
if (-not [System.IO.Path]::IsPathRooted($Output)) {
    $Output = Join-Path (Get-Location) $Output
}

# Version resources take up to four numeric fields. A release tag like v0.2.1
# becomes 0.2.1.0; anything without a usable number becomes 0.0.0.0.
$ResourceVersion = '0.0.0.0'
$Fields = @($Version.TrimStart('v') -split '[^0-9]+' | Where-Object { $_ -ne '' })
if ($Fields.Count -ge 2) {
    while ($Fields.Count -lt 4) { $Fields += '0' }
    $ResourceVersion = ($Fields[0..3] -join '.')
}

Push-Location $ClientDir
try {
    # A previous interrupted build must not leak its generated resources into
    # this one.
    Remove-Item -LiteralPath $Resource -ErrorAction SilentlyContinue
    if (-not $SkipIcon) {
        go run ./cmd/winicon -out $Icon
        go run github.com/tc-hib/go-winres@v0.3.3 simply `
            --arch amd64 `
            --out (Join-Path $PackageDir 'rsrc') `
            --icon $Icon `
            --manifest gui `
            --product-name 'kitsune-whisper' `
            --file-description 'kitsune-whisper Client' `
            --original-filename 'kitsune-client.exe' `
            --product-version $ResourceVersion `
            --file-version $ResourceVersion
    }

    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Output) | Out-Null
    $env:CGO_ENABLED = '1'
    # -H=windowsgui covers an internal-link build; the extldflags cover the
    # external-link build cgo forces, where Go ignores -H.
    $ldflags = "-X github.com/FoxRed-cmd/kitsune-whisper/client/internal/cli.Version=$Version"
    if (-not $Console) {
        # -H=windowsgui covers an internal-link build; the extldflags cover the
        # external-link build cgo forces, where Go ignores -H.
        $ldflags += ' -H=windowsgui -extldflags=-Wl,--subsystem,windows'
    }
    go build -trimpath -tags 'desktop' -ldflags $ldflags -o $Output ./cmd/kitsune-client
}
finally {
    Pop-Location
    Remove-Item -LiteralPath $Resource -ErrorAction SilentlyContinue
    Remove-Item -LiteralPath $Icon -ErrorAction SilentlyContinue
}
