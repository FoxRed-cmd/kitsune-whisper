#!/usr/bin/env pwsh
#requires -Version 7
<#
.SYNOPSIS
Live end-to-end slice for kitsune-whisper (issue #22).

.DESCRIPTION
Builds the real Client, then runs scripts/e2e_live.py, which brings up a real
Server and drives the speech, silence, and error paths through the frozen HTTP
contract. Use -Report to also write the results as Markdown.

.EXAMPLE
./scripts/e2e-live.ps1 -Profile local-cpu -Report e2e-cpu.md
./scripts/e2e-live.ps1 -Profile local-gpu -Speech .\sample.wav
#>
[CmdletBinding()]
param(
    [ValidateSet('external', 'local-cpu', 'local-gpu', 'docker-cpu', 'docker-gpu')]
    [string]$Profile = 'local-cpu',
    [string]$ServerUrl = '',
    [string]$Speech = '',
    [string]$Report = '',
    [string]$Client = '',
    [int]$Repeats = 3,
    [switch]$NoBuild,
    [switch]$Keep,
    [switch]$Capture,
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$Passthrough
)

$ErrorActionPreference = 'Stop'
$root = Split-Path -Parent $PSScriptRoot

if (-not $Client) {
    $Client = Join-Path ([System.IO.Path]::GetTempPath()) 'kitsune-e2e/kitsune-client.exe'
}

if (-not $NoBuild) {
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
    (Join-Path $PSScriptRoot 'e2e_live.py'),
    '--profile', $Profile,
    '--client', $Client,
    '--repeats', $Repeats
)
if ($ServerUrl) { $arguments += @('--server-url', $ServerUrl) }
if ($Speech) { $arguments += @('--speech', $Speech) }
if ($Report) { $arguments += @('--report', $Report) }
if ($Keep) { $arguments += '--keep' }
if ($Capture) { $arguments += '--capture' }
if ($Passthrough) { $arguments += $Passthrough }

& $python @arguments
exit $LASTEXITCODE
