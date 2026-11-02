// Package localserver starts and stops the native Local Server through its OS
// supervisor.
//
// The Local Server profile is hard-stopped: the process is terminated, not
// soft-stopped, so the Client's Stop button actually releases the daemon and its
// memory. A Remote Server keeps the Admin API's soft-stop because the Client
// cannot drive another machine's supervisor.
package localserver

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/startup"
)

const (
	// LinuxUnit is the systemd user unit the Server bundle installs.
	LinuxUnit = startup.ServerUnit
	// WindowsTask is the per-user Task Scheduler task the Server bundle installs.
	WindowsTask = startup.ServerTask
)

// Runner executes one supervisor command and returns its combined output.
type Runner func(name string, args ...string) (string, error)

// Controller drives the Local Server's supervisor.
type Controller struct {
	// GOOS selects the platform ("linux", "windows", ...).
	GOOS string
	// Run executes systemctl or schtasks. It is a field so tests avoid a real
	// user manager and Task Scheduler.
	Run Runner
}

// Default returns a controller for this process.
func Default() *Controller {
	return New(runtime.GOOS, runCommand)
}

// New builds a controller with an explicit platform and command runner.
func New(goos string, run Runner) *Controller {
	if run == nil {
		run = runCommand
	}
	return &Controller{GOOS: goos, Run: run}
}

// Available reports whether the Local Server is supervised on this machine.
// Without a registration the Client cannot start it again after a hard stop.
func (c *Controller) Available() bool {
	switch c.GOOS {
	case "linux":
		out, err := c.Run("systemctl", "--user", "show", "-p", "UnitFileState", "--value", LinuxUnit)
		return err == nil && strings.TrimSpace(out) != ""
	case "windows":
		_, err := c.Run("schtasks", "/Query", "/TN", WindowsTask, "/FO", "LIST")
		return err == nil
	default:
		return false
	}
}

// Stop hard-stops the Local Server. Stopping an already stopped Server is not
// an error.
func (c *Controller) Stop() error {
	switch c.GOOS {
	case "linux":
		return c.check("systemctl", "--user", "stop", LinuxUnit)
	case "windows":
		// schtasks /End reports an error for a task that is not running; a
		// second stop must still succeed.
		_, _ = c.Run("schtasks", "/End", "/TN", WindowsTask)
		return nil
	default:
		return unsupported(c.GOOS)
	}
}

// Start starts the Local Server and lets it load its model.
func (c *Controller) Start() error {
	switch c.GOOS {
	case "linux":
		return c.check("systemctl", "--user", "start", LinuxUnit)
	case "windows":
		return c.check("schtasks", "/Run", "/TN", WindowsTask)
	default:
		return unsupported(c.GOOS)
	}
}

// Restart replaces the Local Server process.
func (c *Controller) Restart() error {
	switch c.GOOS {
	case "linux":
		return c.check("systemctl", "--user", "restart", LinuxUnit)
	case "windows":
		// A stopped task makes /End fail; only /Run matters for a restart.
		_, _ = c.Run("schtasks", "/End", "/TN", WindowsTask)
		return c.check("schtasks", "/Run", "/TN", WindowsTask)
	default:
		return unsupported(c.GOOS)
	}
}

func (c *Controller) check(name string, args ...string) error {
	if _, err := c.Run(name, args...); err != nil {
		return fmt.Errorf("localserver: %s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

func unsupported(goos string) error {
	return fmt.Errorf("localserver: no Local Server supervisor on %s", goos)
}

func runCommand(name string, args ...string) (string, error) {
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
