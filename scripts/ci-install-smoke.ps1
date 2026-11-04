#!/usr/bin/env pwsh
#requires -Version 7
<#
.SYNOPSIS
Prove that the Windows release artifacts install with the checked-out installer.

.DESCRIPTION
Runs install.ps1 against a local artifact directory signed with a throwaway
ECDSA key, then asserts the per-user install tree, the Apps & Features entry,
the user PATH entry, the disabled Server logon trigger, and the installed
binaries' --version output. It then exercises -Uninstall and -Uninstall -Purge
and checks the documented data boundaries and the PATH/registry cleanup.

The installer never asks for administrator rights. LOCALAPPDATA, APPDATA,
USERPROFILE and TEMP are redirected into a temporary tree; the user PATH and
the HKCU uninstall key are the only real per-user state touched, and both are
restored or removed before the script exits.

Example (release CI signs dist/ with an ephemeral key first):

  pwsh -NoProfile -File scripts/ci-install-smoke.ps1 -Artifacts dist -Version 1.0.0

The CI job is install-smoke-windows in .github/workflows/ci.yml.
#>
[CmdletBinding()]
param(
    [string]$Artifacts = (Join-Path (Get-Location) 'dist'),
    [string]$Version = ((Get-Content -LiteralPath (Join-Path $PSScriptRoot '..\VERSION') -Raw).Trim()),
    [string]$Installer = (Join-Path $PSScriptRoot '..\install.ps1')
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$ProgressPreference = 'SilentlyContinue'

function Fail {
    param([string]$Message)
    throw "install smoke: $Message"
}

function Assert-True {
    param([bool]$Condition, [string]$Message)
    if (-not $Condition) { Fail $Message }
}

function Assert-File {
    param([string]$Path)
    Assert-True (Test-Path -LiteralPath $Path -PathType Leaf) "missing file: $Path"
}

function Assert-Absent {
    param([string]$Path)
    Assert-True (-not (Test-Path -LiteralPath $Path)) "unexpected path: $Path"
}

function Assert-Contains {
    param([string]$Path, [string]$Needle)
    Assert-File $Path
    $text = Get-Content -LiteralPath $Path -Raw
    Assert-True ($text.Contains($Needle)) "$Path does not contain: $Needle"
}

function Assert-NoRunEntry {
    $runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
    if (-not (Test-Path -LiteralPath $runKey)) { return }
    $run = Get-ItemProperty -LiteralPath $runKey -ErrorAction SilentlyContinue
    if ($null -eq $run) { return }
    Assert-True ($null -eq $run.PSObject.Properties['kitsune-whisper']) `
        'the installer registered a Run-at-logon entry'
}

function Get-NativeOutput {
    param([string]$FilePath, [string[]]$Arguments = @())
    $stdout = Join-Path $script:Root 'stdout.txt'
    $stderr = Join-Path $script:Root 'stderr.txt'
    $process = Start-Process -FilePath $FilePath -ArgumentList $Arguments `
        -Wait -PassThru -NoNewWindow -RedirectStandardOutput $stdout -RedirectStandardError $stderr
    if ($process.ExitCode -ne 0) {
        $err = if (Test-Path -LiteralPath $stderr) {
            Get-Content -LiteralPath $stderr -Raw
        }
        else { '' }
        Fail "$FilePath exited $($process.ExitCode): $err"
    }
    return (Get-Content -LiteralPath $stdout -Raw).Trim()
}

if (-not $env:LOCALAPPDATA) {
    Fail 'this smoke test runs on Windows only (LOCALAPPDATA is unset)'
}

$Artifacts = (Resolve-Path -LiteralPath $Artifacts).Path
$Installer = (Resolve-Path -LiteralPath $Installer).Path
$Version = $Version.Trim()
foreach ($asset in @(
        'checksums.txt', 'checksums.txt.sig',
        'kitsune-client_windows_amd64.zip',
        'kitsune-server_windows_amd64.tar.gz',
        'ci-public-key.pem')) {
    Assert-File (Join-Path $Artifacts $asset)
}

$script:Root = Join-Path ([System.IO.Path]::GetTempPath()) `
    ('kitsune-install-smoke-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Force -Path $script:Root | Out-Null

$localAppData = Join-Path $script:Root 'localappdata'
$appData = Join-Path $script:Root 'appdata'
$userProfile = Join-Path $script:Root 'profile'
$tempDir = Join-Path $script:Root 'temp'
foreach ($directory in @($localAppData, $appData, $userProfile, $tempDir)) {
    New-Item -ItemType Directory -Force -Path $directory | Out-Null
}

$installRoot = Join-Path $localAppData 'Programs\kitsune-whisper'
$bin = Join-Path $installRoot 'bin'
$serverRoot = Join-Path $installRoot 'Server'
$client = Join-Path $bin 'kitsune-client.exe'
$launcher = Join-Path $bin 'kitsune-server.cmd'
$shortcut = Join-Path $appData 'Microsoft\Windows\Start Menu\Programs\kitsune-whisper.lnk'
$configDir = Join-Path $appData 'kitsune-whisper'
$config = Join-Path $configDir 'kitsune.yaml'
$stateDir = Join-Path $localAppData 'kitsune-whisper'
$models = Join-Path $userProfile '.cache\kitsune-whisper\models'
$uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\kitsune-whisper'

$saved = @{
    LOCALAPPDATA = $env:LOCALAPPDATA
    APPDATA = $env:APPDATA
    USERPROFILE = $env:USERPROFILE
    HOME = $env:HOME
    TEMP = $env:TEMP
    TMP = $env:TMP
    KITSUNE_RELEASE_PUBLIC_KEY = $env:KITSUNE_RELEASE_PUBLIC_KEY
}
$savedUserPath = [Environment]::GetEnvironmentVariable('Path', 'User')
$savedMachinePath = [Environment]::GetEnvironmentVariable('Path', 'Machine')

function Remove-KitsuneTask {
    & schtasks.exe /Delete /TN 'kitsune-server' /F 2>$null | Out-Null
    & schtasks.exe /Delete /TN 'kitsune-client' /F 2>$null | Out-Null
}

function Restore-Environment {
    foreach ($name in @('LOCALAPPDATA', 'APPDATA', 'USERPROFILE', 'HOME', 'TEMP', 'TMP')) {
        $value = $saved[$name]
        if ($null -eq $value) {
            Remove-Item "Env:$name" -ErrorAction SilentlyContinue
        }
        else {
            Set-Item "Env:$name" $value
        }
    }
    $key = $saved['KITSUNE_RELEASE_PUBLIC_KEY']
    if ($null -eq $key) {
        Remove-Item Env:KITSUNE_RELEASE_PUBLIC_KEY -ErrorAction SilentlyContinue
    }
    else {
        $env:KITSUNE_RELEASE_PUBLIC_KEY = $key
    }
    if ($null -ne $savedUserPath) {
        [Environment]::SetEnvironmentVariable('Path', $savedUserPath, 'User')
    }
    else {
        [Environment]::SetEnvironmentVariable('Path', $null, 'User')
    }
}

try {
    # A leftover task or registry key from an earlier run would mask a bug.
    Remove-KitsuneTask
    if (Test-Path -LiteralPath $uninstallKey) {
        Remove-Item -LiteralPath $uninstallKey -Recurse -Force
    }
    [Environment]::SetEnvironmentVariable('Path', '', 'User')

    $env:LOCALAPPDATA = $localAppData
    $env:APPDATA = $appData
    $env:USERPROFILE = $userProfile
    $env:HOME = $userProfile
    $env:TEMP = $tempDir
    $env:TMP = $tempDir
    $env:KITSUNE_RELEASE_PUBLIC_KEY = Join-Path $Artifacts 'ci-public-key.pem'

    function Invoke-Installer {
        param([string[]]$Arguments)
        & $Installer @Arguments
    }

    function Assert-Tree {
        Assert-File $client
        Assert-File $launcher
        Assert-File (Join-Path $serverRoot 'runtime\python.exe')
        Assert-File (Join-Path $serverRoot 'runtime\pythonw.exe')
        Assert-File (Join-Path $serverRoot 'bootstrap.py')
        Assert-File (Join-Path $serverRoot 'launcher.py')
        Assert-File (Join-Path $serverRoot 'bundle.json')
        Assert-File (Join-Path $installRoot 'uninstall.ps1')
        Assert-File $shortcut
        Assert-File $config
        Assert-Contains (Join-Path $serverRoot 'bundle.json') ('"version": "' + $Version + '"')

        $entry = Get-ItemProperty -LiteralPath $uninstallKey -ErrorAction SilentlyContinue
        Assert-True ($null -ne $entry) 'the Apps & Features entry is missing'
        Assert-True ($entry.DisplayName -eq 'kitsune-whisper') 'Apps & Features DisplayName is wrong'
        Assert-True ($entry.DisplayVersion -eq $Version) "Apps & Features DisplayVersion is '$($entry.DisplayVersion)'"
        Assert-True ($entry.InstallLocation -eq $installRoot) 'Apps & Features InstallLocation is wrong'
        Assert-True ($entry.UninstallString -like '*uninstall.ps1*') 'the Apps & Features uninstall command is wrong'

        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        Assert-True ($userPath -and ($userPath -split ';' | Where-Object { $_ -ieq $bin })) `
            'the install bin directory is not on the user PATH'

        # The Server registration exists but its logon trigger is disabled, so
        # it never starts at login while schtasks /Run still works on demand.
        $taskXmlPath = Join-Path $configDir 'kitsune-server-task.xml'
        Assert-File $taskXmlPath
        $taskXml = Get-Content -LiteralPath $taskXmlPath -Raw
        Assert-True ($taskXml -match '(?s)<LogonTrigger>\s*<Enabled>false</Enabled>') `
            'the Server logon trigger is enabled'
        Assert-True ($taskXml -match '(?s)<Settings>.*?<Enabled>true</Enabled>.*?</Settings>') `
            'the Server task itself is disabled, so schtasks /Run would not work'
        $null = & schtasks.exe /Query /TN 'kitsune-server' /FO LIST
        Assert-True ($LASTEXITCODE -eq 0) 'the Server task is not registered'
        $null = & schtasks.exe /Query /TN 'kitsune-client' /FO LIST
        Assert-True ($LASTEXITCODE -ne 0) 'an autostart Client task was registered'
        Assert-NoRunEntry

        $clientVersion = Get-NativeOutput -FilePath $client -Arguments @('--version')
        Assert-True ($clientVersion -eq "kitsune-client $Version") `
            "Client version is '$clientVersion', want 'kitsune-client $Version'"
        $serverVersion = (& $launcher --version 2>&1 | Out-String).Trim()
        Assert-True ($serverVersion -eq "kitsune-server $Version") `
            "Server version is '$serverVersion', want 'kitsune-server $Version'"
        Write-Host "install smoke: installed binaries report $clientVersion and $serverVersion"
    }

    function Assert-Cleanup {
        Assert-Absent $installRoot
        Assert-Absent $shortcut
        Assert-Absent $uninstallKey
        $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
        Assert-True (-not $userPath -or -not ($userPath -split ';' | Where-Object { $_ -ieq $bin })) `
            'the install bin directory is still on the user PATH'
        $null = & schtasks.exe /Query /TN 'kitsune-server' /FO LIST
        Assert-True ($LASTEXITCODE -ne 0) 'the Server task survived removal'
        $null = & schtasks.exe /Query /TN 'kitsune-client' /FO LIST
        Assert-True ($LASTEXITCODE -ne 0) 'a Client task survived removal'
        Assert-NoRunEntry
        $machinePath = [Environment]::GetEnvironmentVariable('Path', 'Machine')
        Assert-True ($machinePath -eq $savedMachinePath) 'the machine PATH changed'
    }

    Write-Host "install smoke: installing $Version from $Artifacts"
    Invoke-Installer -Arguments @('-From', $Artifacts, '-Version', $Version)
    Assert-Tree

    # Uninstall removes our files, registrations and PATH entry but keeps the
    # user's configuration, models and Server data.
    New-Item -ItemType Directory -Force -Path $models | Out-Null
    Set-Content -LiteralPath (Join-Path $models 'smoke-model.bin') -Value 'model'
    New-Item -ItemType Directory -Force -Path (Join-Path $stateDir 'tls') | Out-Null
    Set-Content -LiteralPath (Join-Path $stateDir 'tls\server.crt') -Value 'certificate'
    Set-Content -LiteralPath (Join-Path $stateDir 'devices.json') -Value '{"device":"smoke"}'
    Set-Content -LiteralPath (Join-Path $stateDir 'pairing.json') -Value '{"device":"smoke"}'
    Add-Content -LiteralPath $config -Value '# smoke-kept'

    Invoke-Installer -Arguments @('-Uninstall')
    Assert-Cleanup
    Assert-File $config
    Assert-Contains $config '# smoke-kept'
    Assert-File (Join-Path $models 'smoke-model.bin')
    Assert-File (Join-Path $stateDir 'tls\server.crt')
    Assert-File (Join-Path $stateDir 'devices.json')
    Assert-File (Join-Path $stateDir 'pairing.json')

    # Purge removes everything, including what uninstall kept.
    Invoke-Installer -Arguments @('-From', $Artifacts, '-Version', $Version)
    New-Item -ItemType Directory -Force -Path $models | Out-Null
    Set-Content -LiteralPath (Join-Path $models 'smoke-model.bin') -Value 'model'
    New-Item -ItemType Directory -Force -Path (Join-Path $stateDir 'tls') | Out-Null
    Set-Content -LiteralPath (Join-Path $stateDir 'tls\server.crt') -Value 'certificate'

    Invoke-Installer -Arguments @('-Uninstall', '-Purge')
    Assert-Cleanup
    Assert-Absent $configDir
    Assert-Absent $stateDir
    Assert-Absent (Join-Path $userProfile '.cache\kitsune-whisper')

    Write-Host 'install smoke: PASS: install, versions, uninstall and purge behaved as documented'
}
finally {
    Remove-KitsuneTask
    if (Test-Path -LiteralPath $uninstallKey) {
        Remove-Item -LiteralPath $uninstallKey -Recurse -Force -ErrorAction SilentlyContinue
    }
    Restore-Environment
    if (-not $env:KITSUNE_SMOKE_KEEP) {
        Remove-Item -LiteralPath $script:Root -Recurse -Force -ErrorAction SilentlyContinue
    }
    else {
        Write-Host "install smoke: kept $script:Root"
    }
}
