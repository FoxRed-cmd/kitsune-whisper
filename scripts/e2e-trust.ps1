#!/usr/bin/env pwsh
#requires -Version 7
<#
.SYNOPSIS
Authenticated end-to-end slice for kitsune-whisper (issue #83).

.DESCRIPTION
Builds the real Client, then runs scripts/e2e_trust.py, which starts a
TLS-enabled Server and exercises local pairing, remote pairing and revocation,
manual Server switch, soft-stop/start, supervised restart, an unreachable
Server, and Spool recovery. Use -Report to also write the results as Markdown.

.EXAMPLE
./scripts/e2e-trust.ps1 -Model small -Report e2e-trust.md
./scripts/e2e-trust.ps1 -Speech .\sample.wav
#>
[CmdletBinding()]
param(
    [string]$Speech = '',
    [string]$Report = '',
    [string]$Client = '',
    [string]$Model = 'small',
    [switch]$NoBuild,
    [switch]$Keep,
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$Passthrough
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

if (-not $Client) {
    $Client = Join-Path ([System.IO.Path]::GetTempPath()) 'kitsune-e2e/kitsune-client.exe'
}

if (-not $NoBuild) {
    # The harness reads the Client's stdout, so this is the console build, not
    # the GUI-subsystem executable that packaging/windows/build-client.ps1
    # produces. The shipped Client is still built by that script.
    $env:CGO_ENABLED = '1'
    if (-not (Get-Command gcc -ErrorAction SilentlyContinue) -and (Get-Command zig -ErrorAction SilentlyContinue)) {
        $env:CC = 'zig cc'
    }
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Client) | Out-Null
    Push-Location (Join-Path $root 'client')
    try {
        & go build -o $Client ./cmd/kitsune-client
        if ($LASTEXITCODE -ne 0) { throw 'client build failed' }
    }
    finally {
        Pop-Location
    }
}

$python = Join-Path $root 'server/.venv/Scripts/python.exe'
if (-not (Test-Path $python)) {
    $python = (Get-Command python -ErrorAction Stop).Source
}

$arguments = @(
    (Join-Path $PSScriptRoot 'e2e_trust.py'),
    '--client', $Client,
    '--model', $Model
)
if ($Speech) { $arguments += @('--speech', $Speech) }
if ($Report) { $arguments += @('--report', $Report) }
if ($Keep) { $arguments += '--keep' }
if ($Passthrough) { $arguments += $Passthrough }

& $python @arguments
exit $LASTEXITCODE
