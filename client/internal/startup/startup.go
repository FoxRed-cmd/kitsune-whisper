// Package startup controls whether the Client and the local Server start with
// the user's desktop session. On Linux it enables or disables the systemd user
// units the installers registered; on Windows it enables or disables the
// per-user Task Scheduler tasks (and the HKCU Run fallback) they registered.
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
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/process"
)

const (
	// ClientUnit is the systemd user unit that runs the Client.
	ClientUnit = autostart.UnitName
	// ServerUnit is the systemd user unit that runs the local Server.
	ServerUnit = "kitsune-server.service"
	// ClientTask is the Task Scheduler task that runs the Client on Windows.
	ClientTask = autostart.TaskName
	// ServerTask is the Task Scheduler task that runs the local Server on
	// Windows.
	ServerTask = "kitsune-server"
)

// Runner executes one supervisor command and returns its combined output.
type Runner func(name string, args ...string) (string, error)

// RunValue is the HKCU Run fallback the Windows installer writes when Task
// Scheduler registration is denied. Disabling it deletes the value; turning
// autostart back on recreates a full registration.
type RunValue interface {
	// Present reports whether the fallback value exists.
	Present() (bool, error)
	// Remove deletes the fallback value.
	Remove() error
}

// Controller reads and changes the login autostart state.
type Controller struct {
	// GOOS selects the platform ("linux", "windows", ...).
	GOOS string
	// Run executes systemctl or schtasks and returns its combined output. It
	// is a field so tests avoid a real user manager and Task Scheduler.
	Run Runner
	// RunKey is the HKCU Run fallback on Windows. It is nil elsewhere.
	RunKey RunValue
	// Register recreates the Client's login registration when the Startup
	// switch is turned on and the installers' registration is missing. It is
	// nil on non-Windows platforms, where the unit must already exist.
	Register func() error
}

// Default returns a controller for this process. configPath is the explicit
// --config path, which a recreated registration pins; empty lets the Client
// discover its config at login.
func Default(configPath string) *Controller {
	controller := New(runtime.GOOS, runCommand)
	if runtime.GOOS != "windows" {
		return controller
	}
	if configPath != "" {
		if abs, err := filepath.Abs(configPath); err == nil {
			configPath = abs
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return controller
	}
	controller.Register = func() error {
		env := autostart.Default(nil)
		return autostart.Install(env, autostart.Options{Binary: exe, Config: configPath})
	}
	return controller
}

// New builds a controller with an explicit platform and command runner.
func New(goos string, run Runner) *Controller {
	if run == nil {
		run = runCommand
	}
	controller := &Controller{GOOS: goos, Run: run}
	if goos == "windows" {
		controller.RunKey = registryRunKey{}
	}
	return controller
}

// Status reports the Client and Server autostart state. serverAvailable is
// false when this session has no local Server registration (a remote-only
// setup).
func (c *Controller) Status() (clientEnabled, serverEnabled, serverAvailable bool) {
	switch c.GOOS {
	case "linux":
		clientState := c.unitState(ClientUnit)
		serverState := c.unitState(ServerUnit)
		return unitEnabled(clientState), unitEnabled(serverState), serverState != ""
	case "windows":
		return c.windowsStatus()
	default:
		return false, false, false
	}
}

// SetClient enables or disables the Client registration.
func (c *Controller) SetClient(enabled bool) error {
	switch c.GOOS {
	case "linux":
		return c.setUnit(ClientUnit, enabled)
	case "windows":
		return c.setClientWindows(enabled)
	default:
		return unsupported(c.GOOS)
	}
}

// SetServer enables or disables the local Server registration.
func (c *Controller) SetServer(enabled bool) error {
	switch c.GOOS {
	case "linux":
		return c.setUnit(ServerUnit, enabled)
	case "windows":
		return c.setServerWindows(enabled)
	default:
		return unsupported(c.GOOS)
	}
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

// windowsStatus reports the state of the two Task Scheduler tasks. The Client
// is also on when the HKCU Run fallback is registered: both launch at logon.
func (c *Controller) windowsStatus() (clientEnabled, serverEnabled, serverAvailable bool) {
	_, clientEnabled = c.taskState(ClientTask)
	if c.RunKey != nil {
		if present, err := c.RunKey.Present(); err == nil && present {
			clientEnabled = true
		}
	}
	serverRegistered, serverEnabled := c.taskState(ServerTask)
	return clientEnabled, serverEnabled, serverRegistered
}

// setClientWindows disables the task in place so turning autostart back on does
// not need the installer again, and clears the HKCU Run fallback. Enabling a
// fully missing registration recreates the task the installer would have made.
func (c *Controller) setClientWindows(enabled bool) error {
	registered, _ := c.taskState(ClientTask)
	if !enabled {
		if registered {
			if err := c.setTask(ClientTask, false); err != nil {
				return err
			}
		}
		return c.removeRunValue()
	}
	if registered {
		return c.setTask(ClientTask, true)
	}
	present, err := c.runValuePresent()
	if err != nil {
		return err
	}
	if present {
		return nil
	}
	if c.Register != nil {
		return c.Register()
	}
	return errors.New("the Client logon task is not registered; run kitsune-client install-autostart")
}

func (c *Controller) runValuePresent() (bool, error) {
	if c.RunKey == nil {
		return false, nil
	}
	present, err := c.RunKey.Present()
	if err != nil {
		return false, fmt.Errorf("read HKCU Run value: %w", err)
	}
	return present, nil
}

func (c *Controller) removeRunValue() error {
	if c.RunKey == nil {
		return nil
	}
	if err := c.RunKey.Remove(); err != nil {
		return fmt.Errorf("remove HKCU Run value: %w", err)
	}
	return nil
}

// setServerWindows toggles the Server task the bundle installer registered.
// Without it the local Server is not supervised in this session.
func (c *Controller) setServerWindows(enabled bool) error {
	registered, _ := c.taskState(ServerTask)
	if !registered {
		if enabled {
			return errors.New("the local Server logon task is not registered; reinstall the Server bundle")
		}
		return nil
	}
	return c.setTask(ServerTask, enabled)
}

func (c *Controller) setTask(task string, enabled bool) error {
	flag := "/DISABLE"
	if enabled {
		flag = "/ENABLE"
	}
	if _, err := c.Run("schtasks", "/Change", "/TN", task, flag); err != nil {
		return fmt.Errorf("schtasks /Change /TN %s %s: %w", task, flag, err)
	}
	return nil
}

// taskState reports whether a Task Scheduler task is registered and whether it
// is enabled. A disabled task keeps its registration but stops triggering at
// logon, which is exactly the Startup switch's "off" state.
func (c *Controller) taskState(task string) (registered, enabled bool) {
	out, err := c.Run("schtasks", "/Query", "/TN", task, "/XML")
	if err != nil {
		return false, false
	}
	return true, taskEnabled(out)
}

// taskEnabled reads the task-level <Settings><Enabled> flag. schtasks omits the
// element when it is true and prints <Enabled>false</Enabled> after
// /Change /DISABLE, so an absent flag means enabled.
func taskEnabled(output string) bool {
	var definition struct {
		Settings struct {
			Enabled *bool `xml:"Enabled"`
		} `xml:"Settings"`
	}
	decoder := xml.NewDecoder(strings.NewReader(decodeTaskOutput(output)))
	// schtasks declares UTF-16 but writes a single-byte, ASCII-compatible
	// stream to a pipe. The declaration is stale once the bytes are decoded.
	decoder.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) {
		return input, nil
	}
	if err := decoder.Decode(&definition); err != nil {
		return true
	}
	return definition.Settings.Enabled == nil || *definition.Settings.Enabled
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
