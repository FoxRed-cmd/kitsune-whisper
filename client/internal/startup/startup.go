// Package startup controls whether the Client and the local Server start with
// the user's desktop session. On Linux it toggles the systemd user units the
// installer registered; on other platforms the feature is unavailable.
package startup

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
)

const (
	// ClientUnit is the systemd user unit that runs the Client.
	ClientUnit = "kitsune-client.service"
	// ServerUnit is the systemd user unit that runs the local Server.
	ServerUnit = "kitsune-server.service"
)

// Controller reads and changes the login autostart state.
type Controller struct {
	// run executes systemctl and returns its combined output. It is a field so
	// tests can avoid the real user manager.
	run func(name string, args ...string) (string, error)
}

// Default returns a controller backed by systemctl --user.
func Default() *Controller {
	return &Controller{run: runSystemctl}
}

// Status reports the Client and Server autostart state. serverAvailable is
// false when this session has no local Server unit (a remote-only setup).
func (c *Controller) Status() (clientEnabled, serverEnabled, serverAvailable bool) {
	if runtime.GOOS != "linux" {
		return false, false, false
	}
	clientState := c.unitState(ClientUnit)
	serverState := c.unitState(ServerUnit)
	return unitEnabled(clientState), unitEnabled(serverState), serverState != ""
}

// SetClient enables or disables the Client unit.
func (c *Controller) SetClient(enabled bool) error {
	return c.setUnit(ClientUnit, enabled)
}

// SetServer enables or disables the local Server unit.
func (c *Controller) SetServer(enabled bool) error {
	return c.setUnit(ServerUnit, enabled)
}

func (c *Controller) setUnit(unit string, enabled bool) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("autostart is only available on Linux")
	}
	action := "disable"
	if enabled {
		action = "enable"
	}
	if _, err := c.run("systemctl", "--user", action, unit); err != nil {
		return fmt.Errorf("systemctl --user %s %s: %w", action, unit, err)
	}
	return nil
}

// unitState returns the UnitFileState of one systemd user unit, or "" when the
// unit does not exist in this session.
func (c *Controller) unitState(unit string) string {
	out, err := c.run("systemctl", "--user", "show", "-p", "UnitFileState", "--value", unit)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func unitEnabled(state string) bool {
	return state == "enabled" || state == "enabled-runtime"
}

func runSystemctl(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(out))
		if message == "" {
			return "", err
		}
		return "", fmt.Errorf("%s: %w", message, err)
	}
	return string(out), nil
}
