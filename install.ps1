#!/usr/bin/env pwsh
#requires -Version 7
<#
.SYNOPSIS
Installs, upgrades, or removes kitsune-whisper on Windows without elevation.

.DESCRIPTION
Downloads the release's Client archive and/or native Server bundle, verifies
checksums.txt against the release's detached ECDSA P-256 signature, verifies
every archive against checksums.txt, and only then installs it under the
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

The Microsoft Edge WebView2 Runtime and every other dependency are not
installed or downloaded. A missing dependency produces a warning that points at
the README Dependencies section; the README is the only place with package
commands.

.PARAMETER Version
Install VERSION instead of the latest release (also KITSUNE_VERSION).

.PARAMETER ClientOnly
Install only the Client. The Server bundle and its task are not installed.

.PARAMETER ServerOnly
Install only the Server bundle and launcher. No Client, config or shortcut.

.PARAMETER From
Install from release artifacts in this directory (also
KITSUNE_ARTIFACTS_DIR). The directory must contain checksums.txt and
checksums.txt.sig (unless -Insecure).

.PARAMETER ForceConfig
Overwrite an existing %APPDATA%\kitsune-whisper\kitsune.yaml.

.PARAMETER DryRun
Print the plan and change nothing.

.PARAMETER Insecure
Skip the release signature check. For local development only; the SHA-256
checksums are still verified. Never use it for a download you do not control.

.PARAMETER Uninstall
Remove the binaries, Server bundle, supervisor tasks, Start Menu shortcut, PATH
entry and Apps & Features entry. Configuration, models, certificates, pairing
state and logs are kept.

.PARAMETER Purge
With -Uninstall: also remove configuration, models, cache/state, certificates,
pairing state, logs, spool, stored tokens and the install directory, leaving
nothing of ours.

.PARAMETER RemoveModels
With -Uninstall: delete downloaded models only.

.PARAMETER RemoveServerData
With -Uninstall: delete Server certificates, Devices, logs and soft-stop state.

One-liner:

  irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1 | iex

.EXAMPLE
./install.ps1
./install.ps1 -Version v1.2.3
./install.ps1 -ClientOnly
./install.ps1 -ServerOnly
./install.ps1 -ForceConfig
./install.ps1 -DryRun -From .\dist
./install.ps1 -Insecure -From .\dist
./install.ps1 -Uninstall
./install.ps1 -Uninstall -Purge
./install.ps1 -Uninstall -RemoveModels
./install.ps1 -Uninstall -RemoveServerData
#>
[CmdletBinding()]
param(
    [string]$Version = $(if ($env:KITSUNE_VERSION) { $env:KITSUNE_VERSION } else { 'latest' }),
    [switch]$Uninstall,
    [switch]$Purge,
    [switch]$RemoveModels,
    [switch]$RemoveServerData,
    [switch]$ClientOnly,
    [switch]$ServerOnly,
    [switch]$ForceConfig,
    [switch]$DryRun,
    [switch]$Insecure,
    [string]$From = $(if ($env:KITSUNE_ARTIFACTS_DIR) { $env:KITSUNE_ARTIFACTS_DIR } else { '' })
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest
$ProgressPreference = 'SilentlyContinue'

if (-not $env:LOCALAPPDATA) {
    throw 'kitsune-whisper releases are built for 64-bit Windows; run this script on Windows.'
}

$Repo = 'FoxRed-cmd/kitsune-whisper'
$ClientAsset = 'kitsune-client_windows_amd64.zip'
$ServerAsset = 'kitsune-server_windows_amd64.tar.gz'

# The release key that signs checksums.txt. It is the same PEM committed at
# packaging/keys/kitsune-release-public.pem; pinning it here means a compromised
# download host cannot substitute both the checksums and their signature.
$ReleasePublicKeyPem = @'
-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEGh5umJxHjCLuQS4EVIkydw0zrLKU
f+HckG02s6mF/HzNPpGCLIZ6RBotAeC5KTfg6B0mmMnLopxnc1WKln/kYQ==
-----END PUBLIC KEY-----
'@

# The per-user programs convention, not a machine-wide directory.
$InstallRoot = Join-Path $env:LOCALAPPDATA 'Programs\kitsune-whisper'
$BinDir = Join-Path $InstallRoot 'bin'
$ServerRoot = Join-Path $InstallRoot 'Server'
$UninstallShim = Join-Path $InstallRoot 'uninstall.ps1'
$ConfigDir = Join-Path $env:APPDATA 'kitsune-whisper'
$ConfigPath = Join-Path $ConfigDir 'kitsune.yaml'
$StateDir = Join-Path $env:LOCALAPPDATA 'kitsune-whisper'
$HomeDir = if ($env:USERPROFILE) { $env:USERPROFILE } elseif ($env:HOME) { $env:HOME } else { '' }
$ModelDir = if ($HomeDir) { Join-Path $HomeDir '.cache\kitsune-whisper\models' } else { '' }
$StartMenuShortcut = Join-Path $env:APPDATA `
    'Microsoft\Windows\Start Menu\Programs\kitsune-whisper.lnk'
$UninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\kitsune-whisper'
$WebView2Client = '{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}'
$WebView2Help = 'https://developer.microsoft.com/microsoft-edge/webview2/'
$ReadmeDependencies = "https://github.com/$Repo/blob/main/README.md#dependencies"

# Stable dependency identifiers. The README Dependencies section and the tests
# may rely on them; warnings never print a package-manager command.
$DepWebView2 = 'webview2'
$DepWindowsVersion = 'windows-version'
$DepWindows64 = 'windows-64-bit'
$MinimumWindowsBuild = 17763  # Windows 10 1809, the oldest serviced release.

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

function Get-ReleasePublicKeyPem {
    # KITSUNE_RELEASE_PUBLIC_KEY exists only for the installer test harness, so
    # local artifacts can be signed with a throwaway key. Production uses the
    # pinned PEM above. The value is a path or the PEM text itself.
    $override = $env:KITSUNE_RELEASE_PUBLIC_KEY
    if ($override) {
        if ($override -match '-----BEGIN PUBLIC KEY-----') {
            return $override
        }
        try {
            if (Test-Path -LiteralPath $override -PathType Leaf) {
                return Get-Content -LiteralPath $override -Raw
            }
        }
        catch {
            # Not a path and not PEM: let ImportFromPem report the bad key.
        }
        return $override
    }
    return $ReleasePublicKeyPem
}

function Assert-ReleaseSignature {
    param(
        [Parameter(Mandatory = $true)][string]$ChecksumsPath,
        [Parameter(Mandatory = $true)][string]$SignaturePath
    )
    if (-not (Test-Path -LiteralPath $SignaturePath -PathType Leaf)) {
        throw "missing release signature: $SignaturePath"
    }
    $key = [System.Security.Cryptography.ECDsa]::Create()
    try {
        $key.ImportFromPem((Get-ReleasePublicKeyPem))
        $valid = $key.VerifyData(
            [System.IO.File]::ReadAllBytes($ChecksumsPath),
            [System.IO.File]::ReadAllBytes($SignaturePath),
            [System.Security.Cryptography.HashAlgorithmName]::SHA256,
            [System.Security.Cryptography.DSASignatureFormat]::Rfc3279DerSequence)
        if (-not $valid) {
            throw 'release signature verification failed: checksums.txt.sig does not match the pinned release key'
        }
    }
    catch [System.Security.Cryptography.CryptographicException] {
        throw ("release signature verification failed: checksums.txt.sig is not a valid " +
            "ECDSA P-256 signature ($($_.Exception.Message))")
    }
    finally {
        $key.Dispose()
    }
    Write-Step 'verified the release signature for checksums.txt'
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
    # The shim lives next to bin/ and Server/, so Apps & Features never depends
    # on a download or an interactive prompt. It is the single source of the
    # uninstall and purge boundaries, and it is safe to run non-interactively.
    param([string]$Path = $UninstallShim)

    $body = @'
#requires -Version 7
# Generated by the kitsune-whisper installer: removes the files and
# registrations this installation created. Without -Purge it keeps
# configuration, models, certificates, pairing state and logs; -Purge removes
# every trace we created. No elevation and no package changes.
[CmdletBinding()]
param(
    [switch]$Purge,
    [switch]$RemoveModels,
    [switch]$RemoveServerData,
    [string]$InstallRoot = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

# Apps & Features runs the shim in place, so $PSScriptRoot is the install root.
# The installer's temp-shim fallback passes -InstallRoot explicitly.
$installRoot = if ($InstallRoot) { $InstallRoot } else { $PSScriptRoot }
$bin = Join-Path $installRoot 'bin'
$uninstallKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\kitsune-whisper'
$shortcut = Join-Path $env:APPDATA 'Microsoft\Windows\Start Menu\Programs\kitsune-whisper.lnk'
$configDir = if ($env:APPDATA) { Join-Path $env:APPDATA 'kitsune-whisper' } else { '' }
$stateDir = if ($env:LOCALAPPDATA) { Join-Path $env:LOCALAPPDATA 'kitsune-whisper' } else { '' }
$homeDir = if ($env:USERPROFILE) { $env:USERPROFILE } elseif ($env:HOME) { $env:HOME } else { '' }
$modelDir = if ($homeDir) { Join-Path $homeDir '.cache\kitsune-whisper\models' } else { '' }

function Remove-OurPath {
    param([string]$Path)
    if ($Path -and (Test-Path -LiteralPath $Path)) {
        Remove-Item -LiteralPath $Path -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Remove-PathEntry {
    # Remove every duplicate, comparing case-insensitively: Windows paths are
    # case-insensitive, and an older install may have appended the entry twice.
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $userPath) {
        return
    }
    $entries = @($userPath -split ';')
    $kept = @($entries | Where-Object { $_ -and ($_ -ine $bin) })
    if ($kept.Count -ne $entries.Count) {
        [Environment]::SetEnvironmentVariable('Path', ($kept -join ';'), 'User')
    }
}

function Remove-StoredCredentials {
    # go-keyring files Windows tokens in Credential Manager as
    # "kitsune-whisper:<profile>". Read profile ids from the config while it
    # still exists, then sweep any target cmdkey lists for the same prefix.
    if (-not $env:SystemRoot) {
        return
    }
    $cmdkey = Join-Path $env:SystemRoot 'System32\cmdkey.exe'
    if (-not (Test-Path -LiteralPath $cmdkey)) {
        return
    }
    try {
        $ids = @('local')
        $configFile = Join-Path $configDir 'kitsune.yaml'
        if ($configDir -and (Test-Path -LiteralPath $configFile)) {
            $yaml = Get-Content -LiteralPath $configFile -Raw
            foreach ($match in [regex]::Matches($yaml, '(?m)^\s*-\s*id:\s*["'']?([A-Za-z0-9._-]+)')) {
                $ids += $match.Groups[1].Value
            }
        }
        foreach ($id in @($ids | Select-Object -Unique)) {
            & $cmdkey ("/delete:kitsune-whisper:$id") 2>$null | Out-Null
        }
        $listing = & $cmdkey '/list' 2>$null
        foreach ($line in @($listing)) {
            if ($line -match 'kitsune-whisper:[^\s]+') {
                & $cmdkey ("/delete:" + $Matches[0]) 2>$null | Out-Null
            }
        }
    }
    catch {
        Write-Verbose "could not clear stored credentials: $($_.Exception.Message)"
    }
}

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
& schtasks.exe /End /TN 'kitsune-client' 2>$null | Out-Null
& schtasks.exe /Delete /TN 'kitsune-client' /F 2>$null | Out-Null
if ($configDir) {
    Remove-OurPath (Join-Path $configDir 'kitsune-server-task.xml')
    Remove-OurPath (Join-Path $configDir 'autostart-task.xml')
}
$runKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
if (Test-Path -LiteralPath $runKey) {
    Remove-ItemProperty -Path $runKey -Name 'kitsune-whisper' -ErrorAction SilentlyContinue
}

Remove-PathEntry

if (Test-Path -LiteralPath $uninstallKey) {
    Remove-Item -LiteralPath $uninstallKey -Recurse -Force
}
Remove-Item -LiteralPath $shortcut -Force -ErrorAction SilentlyContinue

if ($RemoveServerData -and -not $Purge) {
    # Creating new Server data would invalidate the kept Client pin, so forget
    # the Local Server pairing while the Client can still do it.
    if (Test-Path -LiteralPath $client) {
        $null = Start-Process -FilePath $client -ArgumentList @('reset-local-pairing') `
            -Wait -PassThru -NoNewWindow
    }
    if ($stateDir) {
        foreach ($entry in @('tls', 'devices.json', 'pairing.json', 'server.log', 'lifecycle.json')) {
            Remove-OurPath (Join-Path $stateDir $entry)
        }
    }
}

if ($RemoveModels) {
    Remove-OurPath $modelDir
    if ($stateDir) {
        Remove-OurPath (Join-Path $stateDir 'models')
    }
}

if ($Purge) {
    Remove-StoredCredentials
    Remove-OurPath $configDir
    Remove-OurPath $stateDir
    Remove-OurPath $modelDir
    if ($homeDir) {
        Remove-OurPath (Join-Path $homeDir '.cache\kitsune-whisper')
    }
    Remove-Item -LiteralPath $installRoot -Recurse -Force -ErrorAction SilentlyContinue
    Write-Host 'kitsune-whisper was purged (configuration, models, certificates, Devices, spool, logs, tokens).'
    return
}

Remove-Item -LiteralPath $installRoot -Recurse -Force -ErrorAction SilentlyContinue
Write-Host 'kitsune-whisper was removed; configuration, models and pairing state were kept.'
'@
    New-Item -ItemType Directory -Force -Path (Split-Path -Parent $Path) | Out-Null
    Set-Content -LiteralPath $Path -Value $body -Encoding utf8NoBOM
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

function Write-DependencyWarning {
    param(
        [Parameter(Mandatory = $true)][string]$Identifier,
        [Parameter(Mandatory = $true)][string]$Detail
    )
    # The README is the only place with package commands. Vary the wording of
    # the fix, never the installation itself.
    Write-Warning ("dependency '$Identifier' is missing: $Detail " +
        "See the README Dependencies section: $ReadmeDependencies")
}

function Write-WebView2Warning {
    if (Test-WebView2Installed) {
        return
    }
    # Never download the runtime: it is a system component with its own
    # installer and terms, and the fix is documented for the user.
    Write-DependencyWarning $DepWebView2 ("the Microsoft Edge WebView2 Runtime was not found, " +
        "so the Settings window and Tray cannot open. Install it once from " +
        "$WebView2Help.")
}

function Write-WindowsDependencyWarnings {
    # These checks run after the selected binaries are in place. They only
    # report: a missing dependency never changes the install's exit status.
    try {
        if (-not $ServerOnly) {
            Write-WebView2Warning
        }
        $build = [Environment]::OSVersion.Version.Build
        if ($build -gt 0 -and $build -lt $MinimumWindowsBuild) {
            Write-DependencyWarning $DepWindowsVersion ("this is Windows build $build, but the " +
                "releases target Windows 10 build $MinimumWindowsBuild or newer.")
        }
        if (-not [Environment]::Is64BitOperatingSystem) {
            Write-DependencyWarning $DepWindows64 ("this is a 32-bit Windows installation, but the " +
                "release artifacts are built for windows/amd64.")
        }
    }
    catch {
        # Detection itself must never fail an install that placed its files.
        Write-Verbose "dependency detection was skipped: $($_.Exception.Message)"
    }
}

function Write-InstallPlan {
    $clientPlan = if ($ClientOnly) { 'disabled' } else { Join-Path $BinDir 'kitsune-client.exe' }
    $serverPlan = if ($ServerOnly) { 'disabled' } else { $ServerRoot }
    $signaturePlan = if ($Insecure) {
        'skipped (-Insecure; checksums still verified)'
    }
    else {
        'verified against the pinned release key'
    }
    Write-Host 'kitsune-whisper install plan'
    Write-Host "  version:   $Version"
    Write-Host "  client:    $clientPlan"
    Write-Host "  server:    $serverPlan"
    Write-Host "  path:      $BinDir"
    Write-Host "  install:   $InstallRoot"
    Write-Host "  signature: $signaturePlan"
    Write-Host '  starts:    nothing (the Server supervisor registration is disabled)'
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

        # The signature covers checksums.txt, so verify it before trusting any
        # digest and before fetching or extracting an archive.
        $signature = Join-Path $Temp 'checksums.txt.sig'
        if ($Insecure) {
            Write-Warning '-Insecure: skipping the release signature check'
        }
        else {
            if ($From) {
                $signatureSource = Join-Path $From 'checksums.txt.sig'
                if (-not (Test-Path -LiteralPath $signatureSource -PathType Leaf)) {
                    throw "missing release signature: $signatureSource"
                }
                Copy-Item -LiteralPath $signatureSource -Destination $signature -Force
            }
            else {
                Write-Step "downloading checksums.txt.sig ($Version)"
                Invoke-WebRequest -Uri "$(Get-BaseUrl)/checksums.txt.sig" `
                    -OutFile $signature -UseBasicParsing
            }
            Assert-ReleaseSignature -ChecksumsPath $checksums -SignaturePath $signature
        }

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
            Install-Server -Archive $paths[$ServerAsset] `
                -Sha256 (Get-ChecksumFor -ChecksumsPath $checksums -Asset $ServerAsset)
        }

        Invoke-ConfigMigration
        Add-BinToUserPath
        Write-UninstallShim
        Register-UninstallEntry
        New-StartMenuShortcut
        Write-WindowsDependencyWarnings

        Write-Step "installed to $InstallRoot"
        Write-Step 'nothing was started; the Server task is registered disabled'
        Write-Step 'run install.ps1 -Uninstall or use Apps & features to remove it'
    }
    finally {
        Remove-Item -LiteralPath $Temp -Recurse -Force -ErrorAction SilentlyContinue
    }
}

function Uninstall-Kitsune {
    $pwsh = Join-Path $PSHOME 'pwsh.exe'
    $shim = $UninstallShim
    $tempDir = ''
    if (-not (Test-Path -LiteralPath $shim -PathType Leaf)) {
        # Removal is idempotent: a partial install or a second -Uninstall still
        # cleans the registrations and data. The temp shim receives the install
        # root explicitly because its $PSScriptRoot is the temp directory.
        $tempDir = Join-Path ([System.IO.Path]::GetTempPath()) `
            ('kitsune-uninstall-' + [guid]::NewGuid().ToString('N'))
        New-Item -ItemType Directory -Force -Path $tempDir | Out-Null
        $shim = Join-Path $tempDir 'uninstall.ps1'
        Write-UninstallShim -Path $shim
    }
    $arguments = @(
        '-NoProfile', '-NonInteractive', '-ExecutionPolicy', 'Bypass', '-File', $shim
    )
    if ($tempDir) {
        $arguments += @('-InstallRoot', $InstallRoot)
    }
    if ($Purge) {
        $arguments += '-Purge'
    }
    if ($RemoveModels) {
        $arguments += '-RemoveModels'
    }
    if ($RemoveServerData) {
        $arguments += '-RemoveServerData'
    }
    try {
        & $pwsh @arguments
        if ($LASTEXITCODE -ne 0) {
            throw "the uninstaller failed with exit code $LASTEXITCODE"
        }
    }
    finally {
        if ($tempDir) {
            Remove-Item -LiteralPath $tempDir -Recurse -Force -ErrorAction SilentlyContinue
        }
    }
}

if ($ClientOnly -and $ServerOnly) {
    throw '-ClientOnly and -ServerOnly are mutually exclusive'
}

if (-not $Uninstall -and ($Purge -or $RemoveModels -or $RemoveServerData)) {
    throw '-Purge, -RemoveModels and -RemoveServerData are only valid together with -Uninstall'
}

if ($Uninstall) {
    Uninstall-Kitsune
}
else {
    Install-Kitsune
}
