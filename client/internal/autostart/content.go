package autostart

import (
	"fmt"
	"strings"
)

// unitFile renders the systemd user unit. It binds to graphical-session.target
// so it starts with the desktop session and inherits DISPLAY/WAYLAND_DISPLAY,
// XDG_RUNTIME_DIR, the session bus, and the audio socket. There is deliberately
// no XDG autostart and no linger: linger would start the client at boot with no
// display.
func unitFile(opts Options) string {
	return fmt.Sprintf(`[Unit]
Description=kitsune-whisper dictation client
Documentation=https://github.com/FoxRed-cmd/kitsune-whisper
After=graphical-session.target
PartOf=graphical-session.target

[Service]
Type=simple
ExecStart=%s
Restart=on-failure
RestartSec=2

[Install]
WantedBy=graphical-session.target
`, systemdCommand(opts))
}

func systemdCommand(opts Options) string {
	return commandArgs(opts, systemdQuote)
}

// systemdQuote wraps a field in double quotes when it contains whitespace or a
// quote/backslash, per systemd.syntax(7).
func systemdQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\") {
		return s
	}
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
}

// desktopEntry renders the .desktop file the GlobalShortcuts portal requires.
// The file's basename must equal AppID exactly, so it is named that way.
func desktopEntry(opts Options) string {
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=kitsune-whisper
Comment=Private, LAN-local speech-to-text dictation
Exec=%s
Terminal=false
NoDisplay=true
`, desktopCommand(opts))
}

func desktopCommand(opts Options) string {
	return commandArgs(opts, desktopQuote)
}

// commandArgs builds "binary [--config PATH] run" with the given field quoter.
func commandArgs(opts Options, quote func(string) string) string {
	parts := []string{quote(opts.Binary)}
	if opts.Config != "" {
		parts = append(parts, "--config", quote(opts.Config))
	}
	return strings.Join(append(parts, "run"), " ")
}

// desktopQuote applies the Exec key's quoting rules: an unquoted argument must
// stay clear of reserved characters, otherwise it is double-quoted with
// backslash escapes.
func desktopQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'\\><~|&;$*?#()`") {
		return s
	}
	replacer := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", `\$`)
	return `"` + replacer.Replace(s) + `"`
}

// taskXML renders the Task Scheduler definition registered with schtasks /XML.
// It is a per-user, interactive-token task at logon with restart-on-failure —
// never a system service (docs/adr/0001). user scopes both the logon trigger and
// the principal to the installing account; when empty, Task Scheduler defaults
// to the registering user.
func taskXML(opts Options, user string) string {
	args := "run"
	if opts.Config != "" {
		args = `--config "` + xmlEscape(opts.Config) + `" run`
	}
	userID := ""
	triggerUser := ""
	if user != "" {
		escaped := xmlEscape(user)
		userID = fmt.Sprintf("\n      <UserId>%s</UserId>", escaped)
		triggerUser = fmt.Sprintf("\n      <UserId>%s</UserId>", escaped)
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>kitsune-whisper dictation client</Description>
    <Author>kitsune-whisper</Author>
  </RegistrationInfo>
  <Triggers>
    <LogonTrigger>
      <Enabled>true</Enabled>%s
    </LogonTrigger>
  </Triggers>
  <Principals>
    <Principal id="Author">
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>%s
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <AllowHardTerminate>true</AllowHardTerminate>
    <StartWhenAvailable>true</StartWhenAvailable>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <Enabled>true</Enabled>
  </Settings>
  <Actions Context="Author">
    <Exec>
      <Command>%s</Command>
      <Arguments>%s</Arguments>
    </Exec>
  </Actions>
</Task>
`, triggerUser, userID, xmlEscape(opts.Binary), args)
}

// runCommand renders the HKCU Run value, the fallback when task registration is
// denied.
func runCommand(opts Options) string {
	return commandArgs(opts, windowsQuote)
}

func windowsQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"") {
		return s
	}
	return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
}

func xmlEscape(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
	).Replace(s)
}
