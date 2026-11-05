// Package startup controls whether the Client and the Local Server start with
// the user's desktop session. The user chooses one Autostart mode; this package
// writes and enables exactly the registrations that mode needs and removes
// them again when autostart is switched off.
//
// The Client registration is the login unit or task that runs the Client. The
// Server side uses the supervisor registration the Server bundle already owns:
// on Linux it is enabled or disabled with systemctl, and on Windows only the
// task's logon trigger is toggled, so schtasks /Run still starts the Server on
// demand. Turning autostart off never deletes the Server registration.
package startup

import (
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unicode/utf16"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/autostart"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/process"
)

const (
	// ClientUnit is the systemd user unit that runs the Client.
	ClientUnit = autostart.UnitName
	// ServerUnit is the systemd user unit that runs the Local Server.
	ServerUnit = components.ServerUnit
	// ClientTask is the Task Scheduler task that runs the Client on Windows.
	ClientTask = autostart.TaskName
	// ServerTask is the Task Scheduler task that runs the Local Server on
	// Windows.
	ServerTask = components.ServerTask
	// configDirName is the per-user directory under the OS config home that
	// holds the cached Server task definition.
	configDirName = "kitsune-whisper"
)

// Runner executes one supervisor command and returns its combined output.
type Runner func(name string, args ...string) (string, error)

// RunValue is the HKCU Run fallback the Windows installer writes when Task
// Scheduler registration is denied. It registers the Client at login.
type RunValue interface {
	// Present reports whether the fallback value exists.
	Present() (bool, error)
	// Remove deletes the fallback value.
	Remove() error
}

// Controller reads and changes the Autostart mode.
type Controller struct {
	// GOOS selects the platform ("linux", "windows", ...).
	GOOS string
	// ConfigHome is the XDG config root; the cached Windows Server task
	// definition lives beneath it.
	ConfigHome string
	// Run executes systemctl or schtasks and returns its combined output. It
	// is a field so tests avoid a real user manager and Task Scheduler.
	Run Runner
	// RunKey is the HKCU Run fallback on Windows. It is nil elsewhere.
	RunKey RunValue
	// Register writes and enables the Client's Autostart registration.
	Register func() error
	// Remove deletes the Client's Autostart registration.
	Remove func() error
	// Installed is the set of Components present on this machine. Only modes
	// whose Components are installed are accepted.
	Installed components.Set
}

// Default returns a controller for this process. configPath is the explicit
// --config path, which the Client registration pins; empty lets the Client
// discover its config at login.
func Default(configPath string) *Controller {
	env := components.Default()
	controller := New(runtime.GOOS, runCommand, components.Detect(env))
	controller.ConfigHome = env.ConfigHome
	if configPath != "" {
		if abs, err := filepath.Abs(configPath); err == nil {
			configPath = abs
		}
	}
	autostartEnv := autostart.Default(nil)
	controller.Remove = func() error {
		return autostart.Remove(autostartEnv)
	}
	exe, err := os.Executable()
	if err != nil {
		return controller
	}
	controller.Register = func() error {
		return autostart.Install(autostartEnv, autostart.Options{Binary: exe, Config: configPath})
	}
	return controller
}

// New builds a controller with an explicit platform, command runner, and
// installed Component set.
func New(goos string, run Runner, installed components.Set) *Controller {
	if run == nil {
		run = runCommand
	}
	controller := &Controller{
		GOOS:       goos,
		ConfigHome: components.ConfigHome(goos),
		Run:        run,
		Installed:  installed,
	}
	if goos == "windows" {
		controller.RunKey = registryRunKey{}
	}
	return controller
}

// Status reports which Components are installed, whether autostart is on, and
// the active mode. While autostart is off, Mode is the default offered mode, so
// the selector always shows what turning the switch on would start.
func (c *Controller) Status() components.State {
	state := components.State{Installed: c.Installed}
	if c.GOOS != "linux" && c.GOOS != "windows" {
		state.Mode = components.ModeOff
		return state
	}
	state.Supported = true
	if !c.Installed.Any() {
		state.Mode = components.ModeOff
		return state
	}
	var clientOn, serverOn bool
	switch c.GOOS {
	case "linux":
		clientOn = c.Installed.Client && unitEnabled(c.unitState(ClientUnit))
		serverOn = c.Installed.Server && unitEnabled(c.unitState(ServerUnit))
	case "windows":
		clientOn = c.Installed.Client && c.windowsClientOn()
		serverOn = c.Installed.Server && c.windowsServerOn()
	}
	state.Enabled = clientOn || serverOn
	switch {
	case clientOn && serverOn:
		state.Mode = components.ModeClientAndServer
	case clientOn:
		state.Mode = components.ModeClientOnly
	case serverOn:
		state.Mode = components.ModeServerOnly
	default:
		state.Mode = c.Installed.DefaultMode()
	}
	return state
}

// SetMode enables one Autostart mode or switches autostart off. Enabling a mode
// writes and enables exactly the registrations it needs; switching off removes
// the Client registration and disables the Server's login registration without
// deleting it.
func (c *Controller) SetMode(enabled bool, mode components.Mode) error {
	if c.GOOS != "linux" && c.GOOS != "windows" {
		return unsupported(c.GOOS)
	}
	if !enabled {
		if err := c.setClient(false); err != nil {
			return err
		}
		return c.setServer(false)
	}
	if !c.Installed.Allows(mode) {
		return fmt.Errorf("autostart mode %q is not available: its Components are not installed", mode)
	}
	if err := c.setClient(mode.Set().Client); err != nil {
		return err
	}
	return c.setServer(mode.Set().Server)
}

// setClient writes or removes the Client's own Autostart registration.
func (c *Controller) setClient(enabled bool) error {
	if !c.Installed.Client {
		if enabled {
			return errors.New("the Client is not installed")
		}
		return nil
	}
	if enabled {
		if c.Register == nil {
			return errors.New("this build cannot write the Client autostart registration")
		}
		return c.Register()
	}
	if c.Remove == nil {
		return errors.New("this build cannot remove the Client autostart registration")
	}
	return c.Remove()
}

// setServer toggles the existing Server supervisor registration's login state.
func (c *Controller) setServer(enabled bool) error {
	if !c.Installed.Server {
		if enabled {
			return errors.New("the local Server is not installed; install the Server bundle first")
		}
		return nil
	}
	if c.GOOS == "windows" {
		return c.setServerWindows(enabled)
	}
	return c.setUnit(ServerUnit, enabled)
}

func (c *Controller) setUnit(unit string, enabled bool) error {
	action := "disable"
	if enabled {
		action = "enable"
	}
	if _, err := c.Run("systemctl", "--user", action, unit); err != nil {
		return fmt.Errorf("systemctl --user %s %s: %w", action, unit, err)
	}
	return nil
}

// unitState returns the UnitFileState of one systemd user unit, or "" when the
// unit does not exist in this session.
func (c *Controller) unitState(unit string) string {
	out, err := c.Run("systemctl", "--user", "show", "-p", "UnitFileState", "--value", unit)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func unitEnabled(state string) bool {
	return state == "enabled" || state == "enabled-runtime"
}

// windowsClientOn reports whether either Client registration starts at login.
func (c *Controller) windowsClientOn() bool {
	if out, err := c.Run("schtasks", "/Query", "/TN", ClientTask, "/XML"); err == nil {
		enabled, trigger := logonTriggerState(out)
		if !trigger {
			// A registration without a readable logon trigger falls back to
			// the task-level flag, which is what older installs wrote.
			enabled = taskEnabled(out)
		}
		if enabled {
			return true
		}
	}
	if c.RunKey == nil {
		return false
	}
	present, err := c.RunKey.Present()
	return err == nil && present
}

// windowsServerOn reads the Server task's logon trigger. The task-level
// <Settings><Enabled> flag stays true so /Run keeps working; only the trigger
// says whether it starts at login.
func (c *Controller) windowsServerOn() bool {
	if out, err := c.Run("schtasks", "/Query", "/TN", ServerTask, "/XML"); err == nil {
		if enabled, ok := logonTriggerState(out); ok {
			return enabled
		}
	}
	raw, err := os.ReadFile(c.serverTaskPath())
	if err != nil {
		return false
	}
	enabled, ok := logonTriggerState(string(raw))
	return ok && enabled
}

// setServerWindows toggles the logon trigger on the cached task definition and
// re-registers the task. It deliberately does not disable the task itself.
func (c *Controller) setServerWindows(enabled bool) error {
	path := c.serverTaskPath()
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read the Server task definition: %w", err)
	}
	updated, err := withLogonTrigger(decodeTaskOutput(string(raw)), enabled)
	if err != nil {
		return err
	}
	if err := writeUTF16File(path, updated); err != nil {
		return err
	}
	if _, err := c.Run("schtasks", "/Create", "/TN", ServerTask, "/XML", path, "/F"); err != nil {
		return fmt.Errorf("schtasks /Create /TN %s: %w", ServerTask, err)
	}
	return nil
}

func (c *Controller) serverTaskPath() string {
	return filepath.Join(c.ConfigHome, configDirName, ServerTask+"-task.xml")
}

// taskEnabled reads the task-level <Settings><Enabled> flag. schtasks omits the
// element when it is true, so an absent flag means enabled.
func taskEnabled(output string) bool {
	var definition struct {
		Settings struct {
			Enabled *bool `xml:"Enabled"`
		} `xml:"Settings"`
	}
	if err := decodeTask(output, &definition); err != nil {
		return true
	}
	return definition.Settings.Enabled == nil || *definition.Settings.Enabled
}

// logonTriggerState reads the Server task's logon trigger. The second result is
// false when the definition has no logon trigger or cannot be parsed.
func logonTriggerState(output string) (enabled, ok bool) {
	var definition struct {
		Triggers struct {
			Logon *struct {
				Enabled *bool `xml:"Enabled"`
			} `xml:"LogonTrigger"`
		} `xml:"Triggers"`
	}
	if err := decodeTask(output, &definition); err != nil || definition.Triggers.Logon == nil {
		return false, false
	}
	if definition.Triggers.Logon.Enabled == nil {
		return true, true
	}
	return *definition.Triggers.Logon.Enabled, true
}

// withLogonTrigger rewrites only the logon trigger's Enabled flag, preserving
// the rest of the task definition byte for byte.
func withLogonTrigger(definition string, enabled bool) (string, error) {
	const (
		triggerOpen  = "<LogonTrigger>"
		triggerClose = "</LogonTrigger>"
	)
	start := strings.Index(definition, triggerOpen)
	if start < 0 {
		return "", errors.New("the Server task definition has no logon trigger")
	}
	bodyStart := start + len(triggerOpen)
	end := strings.Index(definition[bodyStart:], triggerClose)
	if end < 0 {
		return "", errors.New("the Server task definition has an unterminated logon trigger")
	}
	body := definition[bodyStart : bodyStart+end]
	value := "false"
	if enabled {
		value = "true"
	}
	if flagStart := strings.Index(body, "<Enabled>"); flagStart >= 0 {
		flagEnd := strings.Index(body[flagStart:], "</Enabled>")
		if flagEnd < 0 {
			return "", errors.New("the Server task logon trigger has an unterminated Enabled flag")
		}
		body = body[:flagStart] + "<Enabled>" + value + "</Enabled>" + body[flagStart+flagEnd+len("</Enabled>"):]
	} else {
		body = "\n      <Enabled>" + value + "</Enabled>" + body
	}
	return definition[:bodyStart] + body + definition[bodyStart+end:], nil
}

func decodeTask(output string, target any) error {
	decoder := xml.NewDecoder(strings.NewReader(decodeTaskOutput(output)))
	// schtasks declares UTF-16 but writes a single-byte, ASCII-compatible
	// stream to a pipe. The declaration is stale once the bytes are decoded.
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	return decoder.Decode(target)
}

// decodeTaskOutput normalizes schtasks output to valid UTF-8. It handles the
// UTF-16LE stream schtasks writes on some hosts and replaces bytes from the
// console code page in element text, which only carries the account name.
func decodeTaskOutput(output string) string {
	raw := []byte(output)
	switch {
	case len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE:
		raw = decodeUTF16(raw[2:])
	case len(raw) >= 4 && raw[0] != 0 && raw[1] == 0 && raw[3] == 0:
		// UTF-16LE without a BOM: ASCII element names interleave NULs.
		raw = decodeUTF16(raw)
	}
	return strings.ToValidUTF8(string(raw), "?")
}

func decodeUTF16(raw []byte) []byte {
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i+1 < len(raw); i += 2 {
		units = append(units, binary.LittleEndian.Uint16(raw[i:]))
	}
	return []byte(string(utf16.Decode(units)))
}

// writeUTF16File writes content as UTF-16LE with a BOM, the encoding Task
// Scheduler expects for an /XML import.
func writeUTF16File(path, content string) error {
	units := utf16.Encode([]rune(content))
	buf := make([]byte, 0, 2+len(units)*2)
	buf = append(buf, 0xFF, 0xFE)
	for _, unit := range units {
		buf = binary.LittleEndian.AppendUint16(buf, unit)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		return fmt.Errorf("write the Server task definition: %w", err)
	}
	return nil
}

func unsupported(goos string) error {
	return fmt.Errorf("startup: no login supervisor on %s", goos)
}

func runCommand(name string, args ...string) (string, error) {
	out, err := process.Command(name, args...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			return "", err
		}
		return "", fmt.Errorf("%s: %w", message, err)
	}
	return string(out), nil
}
