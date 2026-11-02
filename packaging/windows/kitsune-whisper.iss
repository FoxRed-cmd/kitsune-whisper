; kitsune-whisper per-user Windows installer.
;
; Build it with packaging/windows/build-installer.ps1, which supplies every
; #define below from the release artifacts. The installer never asks for
; elevation: the Client, Tray, Server bundle, Start Menu entry, and both logon
; tasks all belong to the installing user.
#ifndef AppVersion
  #define AppVersion "dev"
#endif
#ifndef VersionInfoVersion
  #define VersionInfoVersion "0.0.0.0"
#endif
#ifndef OutputDir
  #define OutputDir "dist"
#endif
#ifndef ClientExe
  #define ClientExe "..\..\dist\kitsune-client.exe"
#endif
#ifndef ServerArtifact
  #define ServerArtifact "..\..\dist\kitsune-server_windows_amd64.tar.gz"
#endif
#ifndef ServerSha256
  #define ServerSha256 ""
#endif
#ifndef WebView2Bootstrapper
  #define WebView2Bootstrapper "MicrosoftEdgeWebView2Setup.exe"
#endif
#ifndef ExampleConfig
  #define ExampleConfig "..\..\kitsune.example.yaml"
#endif
#ifndef SetupHelper
  #define SetupHelper "setup.ps1"
#endif

[Setup]
AppId={{6D62A0E2-3BE8-4C1B-9A18-2C0A6C2B0C11}
AppName=kitsune-whisper
AppVerName=kitsune-whisper {#AppVersion}
AppVersion={#AppVersion}
AppPublisher=FoxRed-cmd
AppPublisherURL=https://github.com/FoxRed-cmd/kitsune-whisper
AppSupportURL=https://github.com/FoxRed-cmd/kitsune-whisper/issues
AppUpdatesURL=https://github.com/FoxRed-cmd/kitsune-whisper/releases
DefaultDirName={localappdata}\kitsune-whisper
DefaultGroupName=kitsune-whisper
DisableDirPage=yes
DisableProgramGroupPage=yes
DisableWelcomePage=no
PrivilegesRequired=lowest
ArchitecturesAllowed=x64compatible
OutputDir={#OutputDir}
OutputBaseFilename=kitsune-whisper_windows_amd64_setup
Compression=lzma2/max
SolidCompression=yes
WizardStyle=modern
; The Client and the scheduled Server are stopped by StopRunningProcesses
; below. Inno's Restart Manager cannot close a tray process or a Task Scheduler
; process, so leaving this on would block every in-place upgrade.
CloseApplications=no
RestartApplications=no
UninstallDisplayIcon={app}\bin\kitsune-client.exe
VersionInfoVersion={#VersionInfoVersion}
VersionInfoDescription=kitsune-whisper setup
VersionInfoProductName=kitsune-whisper
VersionInfoProductVersion={#VersionInfoVersion}

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked
; CTranslate2 needs cuBLAS 12 on a CUDA host; the Server bundle deliberately
; does not carry the ~550 MB wheel. The helper detects an NVIDIA GPU and skips
; the download on CPU-only machines, so checking this by default is safe.
Name: "cuda"; Description: "Download the NVIDIA CUDA runtime for GPU transcription (~550 MB, several minutes)"; GroupDescription: "Optional components:"; Flags: checkedonce

[Files]
Source: "{#ClientExe}"; DestDir: "{app}\bin"; DestName: "kitsune-client.exe"; Flags: ignoreversion
Source: "{#SetupHelper}"; DestDir: "{app}\bin"; DestName: "kitsune-setup.ps1"; Flags: ignoreversion
Source: "{#ExampleConfig}"; DestDir: "{tmp}"; DestName: "kitsune.example.yaml"; Flags: dontcopy
Source: "{#ServerArtifact}"; DestDir: "{tmp}"; DestName: "kitsune-server_windows_amd64.tar.gz"; Flags: dontcopy
Source: "{#WebView2Bootstrapper}"; DestDir: "{tmp}"; DestName: "MicrosoftEdgeWebView2Setup.exe"; Flags: dontcopy

[Icons]
; The visible launcher opens Settings. When the Tray instance is already
; running hidden, the Client's single-instance handoff forwards this launch to
; that instance instead of starting a second process.
Name: "{autoprograms}\kitsune-whisper"; Filename: "{app}\bin\kitsune-client.exe"; Parameters: "gui"; WorkingDir: "{app}\bin"; Comment: "Private, LAN-local speech-to-text dictation"
Name: "{autodesktop}\kitsune-whisper"; Filename: "{app}\bin\kitsune-client.exe"; Parameters: "gui"; WorkingDir: "{app}\bin"; Comment: "Private, LAN-local speech-to-text dictation"; Tasks: desktopicon

[UninstallDelete]
Type: filesandordirs; Name: "{app}\bin"
Type: filesandordirs; Name: "{app}\Server"
Type: files; Name: "{userappdata}\kitsune-whisper\autostart-task.xml"
Type: files; Name: "{userappdata}\kitsune-whisper\kitsune-server-task.xml"
Type: dirifempty; Name: "{app}"

[Code]
const
  WebView2Client = '{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}';
  WebView2Help = 'https://developer.microsoft.com/microsoft-edge/webview2/';
  WebView2OfflineMessage =
    'kitsune-whisper needs the Microsoft Edge WebView2 Runtime for its Settings window and Tray, ' +
    'but it could not be installed. Connect this computer to the internet and run this installer ' +
    'again, or install the WebView2 Evergreen Runtime manually from ' + WebView2Help + '.';

var
  SetupErrors: Boolean;

function Quote(const Value: String): String;
begin
  Result := '"' + Value + '"';
end;

function LogPath: String;
begin
  Result := ExpandConstant('{tmp}\kitsune-whisper-setup.log');
end;

function LogTail: String;
var
  Content: AnsiString;
begin
  if not LoadStringFromFile(LogPath, Content) then
  begin
    Result := '(no setup helper log was written)';
    Exit;
  end;
  if Length(Content) > 4000 then
    Content := Copy(Content, Length(Content) - 3999, 4000);
  Result := Content;
end;

function WebView2Version(RootKey: Integer; const SubKey: String): String;
begin
  if not RegQueryStringValue(RootKey, SubKey, 'pv', Result) then
    Result := '';
end;

function WebView2Installed: Boolean;
var
  Version: String;
  ClientKey: String;
begin
  ClientKey := 'Software\Microsoft\EdgeUpdate\Clients\' + WebView2Client;
  Version := WebView2Version(HKCU, ClientKey);
  if Version = '' then
    Version := WebView2Version(HKLM, 'SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\' + WebView2Client);
  if Version = '' then
    Version := WebView2Version(HKLM, ClientKey);
  Result := (Version <> '') and (Version <> '0.0.0.0');
end;

function InstallWebView2: Boolean;
var
  ResultCode: Integer;
begin
  Result := WebView2Installed;
  if Result then
  begin
    Log('WebView2 Runtime is already installed');
    Exit;
  end;

  ExtractTemporaryFile('MicrosoftEdgeWebView2Setup.exe');
  if not Exec(
    ExpandConstant('{tmp}\MicrosoftEdgeWebView2Setup.exe'),
    '/silent /install',
    '',
    SW_HIDE,
    ewWaitUntilTerminated,
    ResultCode) then
  begin
    Log('WebView2 bootstrapper could not start');
    Result := False;
    Exit;
  end;
  Log(Format('WebView2 bootstrapper exited with code %d', [ResultCode]));
  Result := (ResultCode = 0) and WebView2Installed;
end;

procedure StopRunningProcesses;
var
  ResultCode: Integer;
begin
  // An in-place upgrade replaces both kitsune-client.exe and the Server bundle.
  // Stop the logon task and any manual tray instance before Setup copies files;
  // the helper re-registers both tasks and starts the Client again afterwards.
  Exec(ExpandConstant('{sys}\schtasks.exe'), '/End /TN kitsune-client',
    '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM kitsune-client.exe',
    '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  Exec(ExpandConstant('{sys}\schtasks.exe'), '/End /TN kitsune-server',
    '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  // Task Scheduler returns before Windows releases every mapped file handle.
  Sleep(1000);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  if InstallWebView2 then
  begin
    StopRunningProcesses;
    Result := '';
  end
  else
    Result := WebView2OfflineMessage;
end;

function RunHelper(const Mode: String; PurgeData: Boolean): Integer;
var
  Params: String;
  ResultCode: Integer;
begin
  Params :=
    '-NoProfile -NonInteractive -ExecutionPolicy Bypass -File ' +
    Quote(ExpandConstant('{app}\bin\kitsune-setup.ps1')) +
    ' -Action ' + Mode +
    ' -AppDir ' + Quote(ExpandConstant('{app}')) +
    ' -ConfigPath ' + Quote(ExpandConstant('{userappdata}\kitsune-whisper\kitsune.yaml')) +
    ' -LogPath ' + Quote(LogPath);
  if Mode = 'install' then
  begin
    Params := Params +
      ' -ServerArtifact ' + Quote(ExpandConstant('{tmp}\kitsune-server_windows_amd64.tar.gz')) +
      ' -ServerSha256 ' + Quote('{#ServerSha256}') +
      ' -TemplatePath ' + Quote(ExpandConstant('{tmp}\kitsune.example.yaml'));
    if ExpandConstant('{param:FORCECONFIG|}') <> '' then
      Params := Params + ' -ForceConfig';
    if WizardIsTaskSelected('cuda') then
      Params := Params + ' -Cuda';
  end;
  if PurgeData then
    Params := Params + ' -Purge';

  if not Exec(
    ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe'),
    Params,
    '',
    SW_HIDE,
    ewWaitUntilTerminated,
    ResultCode) then
    Result := -1
  else
    Result := ResultCode;
end;

procedure CurStepChanged(CurStep: TSetupStep);
var
  Code: Integer;
begin
  if CurStep <> ssPostInstall then
    Exit;

  ExtractTemporaryFile('kitsune.example.yaml');
  ExtractTemporaryFile('kitsune-server_windows_amd64.tar.gz');
  Code := RunHelper('install', False);
  if Code <> 0 then
  begin
    SetupErrors := True;
    SuppressibleMsgBox(
      'The Client was installed, but the Local Server did not start.' + #13#10#13#10 +
      'Fix the problem below, then run this installer again. The Client can still use a Remote Server.' + #13#10#13#10 +
      LogTail(),
      mbError, MB_OK, IDOK);
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  PurgeData: Boolean;
  Code: Integer;
begin
  if CurUninstallStep <> usUninstall then
    Exit;

  PurgeData := ExpandConstant('{param:PURGE|}') <> '';
  if not UninstallSilent then
    PurgeData := SuppressibleMsgBox(
      'Also delete kitsune-whisper configuration, models, Devices, certificates, logs, and Spool?' + #13#10#13#10 +
      'Choose No to keep them for a later reinstall.',
      mbConfirmation, MB_YESNO, IDNO) = IDYES;

  Code := RunHelper('uninstall', PurgeData);
  if Code <> 0 then
    SuppressibleMsgBox(
      'kitsune-whisper was removed, but a task or the Server bundle may remain.' + #13#10#13#10 +
      LogTail(),
      mbError, MB_OK, IDOK);
end;

function GetCustomSetupExitCode: Integer;
begin
  if SetupErrors then
    Result := 1
  else
    Result := 0;
end;
