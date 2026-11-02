#!/usr/bin/env pwsh
# Build the per-user kitsune-whisper Windows setup executable.
#
# Release CI supplies the Client executable and the Windows Server bundle,
# already built on native runners. This script verifies both inputs, fetches the
# Evergreen WebView2 bootstrapper (unless one is supplied), and compiles
# kitsune-whisper.iss with Inno Setup 6.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Client,
    [Parameter(Mandatory = $true)][string]$ServerArtifact,
    [string]$Version = 'dev',
    [string]$Output = 'dist',
    [string]$WebView2Bootstrapper = '',
    [string]$Iscc = '',
    [string]$ExampleConfig = '',
    [string]$SetupHelper = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 2.0

$setupName = 'kitsune-whisper_windows_amd64_setup.exe'

function Resolve-ExistingFile {
    param([string]$Path, [string]$Label)
    if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) {
        throw "$Label not found: $Path"
    }
    return (Resolve-Path -LiteralPath $Path).Path
}

function Find-Iscc {
    param([string]$Requested)
    if ($Requested) {
        return (Resolve-ExistingFile $Requested 'Inno Setup compiler')
    }
    $command = Get-Command ISCC.exe -ErrorAction SilentlyContinue
    if ($command) {
        return $command.Source
    }
    $candidates = @()
    foreach ($base in @($env:ProgramFiles, ${env:ProgramFiles(x86)}, $env:LOCALAPPDATA)) {
        if ($base) {
            $candidates += (Join-Path $base 'Inno Setup 6\ISCC.exe')
            $candidates += (Join-Path $base 'Programs\Inno Setup 6\ISCC.exe')
        }
    }
    foreach ($candidate in $candidates) {
        if (Test-Path -LiteralPath $candidate -PathType Leaf) {
            return $candidate
        }
    }
    throw 'Inno Setup 6 is required: install it or pass -Iscc PATH\ISCC.exe'
}

function Get-WebView2Bootstrapper {
    param([string]$Requested)
    if ($Requested) {
        return (Resolve-ExistingFile $Requested 'WebView2 bootstrapper')
    }
    $cache = Join-Path $PSScriptRoot '.cache'
    $target = Join-Path $cache 'MicrosoftEdgeWebView2Setup.exe'
    if (-not (Test-Path -LiteralPath $target -PathType Leaf)) {
        New-Item -ItemType Directory -Force -Path $cache | Out-Null
        Write-Host 'Downloading the Microsoft Edge WebView2 Evergreen bootstrapper...'
        Invoke-WebRequest -UseBasicParsing `
            -Uri 'https://go.microsoft.com/fwlink/p/?LinkId=2124703' `
            -OutFile $target
    }
    $signature = Get-AuthenticodeSignature -LiteralPath $target
    if ($signature.Status -ne 'Valid' -or $null -eq $signature.SignerCertificate -or
        $signature.SignerCertificate.Subject -notmatch 'Microsoft') {
        throw "the WebView2 bootstrapper is not signed by Microsoft: $($signature.Status)"
    }
    return $target
}

function ConvertTo-VersionInfo {
    param([string]$Value)
    $numbers = @($Value -replace '^v', '' -split '[.\-+]' | Where-Object { $_ -match '^\d+$' })
    while ($numbers.Count -lt 4) {
        $numbers += '0'
    }
    return ($numbers[0..3] -join '.')
}

$clientPath = Resolve-ExistingFile $Client 'Client executable'
$serverPath = Resolve-ExistingFile $ServerArtifact 'Server bundle'
if (-not $ExampleConfig) {
    $ExampleConfig = Join-Path (Split-Path -Parent (Split-Path -Parent $PSScriptRoot)) 'kitsune.example.yaml'
}
$ExampleConfig = Resolve-ExistingFile $ExampleConfig 'example configuration'
if (-not $SetupHelper) {
    $SetupHelper = Join-Path $PSScriptRoot 'setup.ps1'
}
$SetupHelper = Resolve-ExistingFile $SetupHelper 'setup helper'
$webview2 = Get-WebView2Bootstrapper $WebView2Bootstrapper
$isccPath = Find-Iscc $Iscc

$outputDir = $ExecutionContext.SessionState.Path.GetUnresolvedProviderPathFromPSPath($Output)
New-Item -ItemType Directory -Force -Path $outputDir | Out-Null

$serverSha256 = (Get-FileHash -LiteralPath $serverPath -Algorithm SHA256).Hash.ToLowerInvariant()
$versionInfo = ConvertTo-VersionInfo $Version
$scriptPath = Join-Path $PSScriptRoot 'kitsune-whisper.iss'

Write-Host "Compiling $setupName (version $Version)..."
& $isccPath `
    "/DAppVersion=$Version" `
    "/DVersionInfoVersion=$versionInfo" `
    "/DOutputDir=$outputDir" `
    "/DClientExe=$clientPath" `
    "/DServerArtifact=$serverPath" `
    "/DServerSha256=$serverSha256" `
    "/DWebView2Bootstrapper=$webview2" `
    "/DExampleConfig=$ExampleConfig" `
    "/DSetupHelper=$SetupHelper" `
    $scriptPath
if ($LASTEXITCODE -ne 0) {
    throw "Inno Setup failed with exit code $LASTEXITCODE"
}

$setup = Join-Path $outputDir $setupName
if (-not (Test-Path -LiteralPath $setup -PathType Leaf)) {
    throw "Inno Setup did not produce $setup"
}
$setupSha256 = (Get-FileHash -LiteralPath $setup -Algorithm SHA256).Hash.ToLowerInvariant()
Set-Content -LiteralPath "$setup.sha256" -Value "$setupSha256  $setupName" -Encoding ASCII
Write-Host "Wrote $setup"
Write-Host "SHA-256 $setupSha256"
