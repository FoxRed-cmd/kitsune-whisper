// Package autostart registers the Client to launch at login inside the user's
// desktop session, and removes that registration again.
//
// On Linux it writes a systemd user unit bound to graphical-session.target and
// the .desktop file the xdg-desktop-portal GlobalShortcuts interface needs; on
// Windows it registers a per-user Task Scheduler task at logon, falling back to
// the HKCU Run key when task registration is denied. It never installs a system
// or SCM service, which cannot see the user's hotkeys or clipboard
// (docs/adr/0001-client-runs-in-user-session.md).
package autostart

import "fmt"

const (
	// AppID is the stable application id registered with the GlobalShortcuts
	// portal. The .desktop file is named after it, so the two must stay in sync.
	AppID = "io.github.FoxRed-cmd.kitsune-whisper"
	// UnitName is the systemd user unit that runs the Client.
	UnitName = "kitsune-client.service"
	// TaskName is the Windows Task Scheduler task that runs the Client.
	TaskName = "kitsune-client"
	// RunValueName is the HKCU Run value used when task registration fails.
	RunValueName = "kitsune-whisper"
	// ConfigDirName is the per-user config directory the Client discovers.
	ConfigDirName = "kitsune-whisper"
)

// Options identify what the autostart entry runs.
type Options struct {
	// Binary is the absolute path to the installed kitsune-client executable.
	Binary string
	// Config pins an absolute kitsune.yaml path in the command line. Empty lets
	// the Client discover the config from the user config directory at startup.
	Config string
}

// Environment is the side-effect surface Install and Uninstall act on.
// Production callers pass Default; tests pass temporary directories and fake
// command runners, so the logic is exercised without touching a real session.
type Environment struct {
	// GOOS selects the platform ("windows", "linux", ...).
	GOOS string
	// ConfigHome is the XDG config root (~/.config or %APPDATA%).
	ConfigHome string
	// DataHome is the XDG data root (~/.local/share).
	DataHome string
	// User is the account name the Windows task is scoped to (e.g.
	// "DOMAIN\\user"); empty lets Task Scheduler default to the current user.
	User string
	// Run executes a command, returning its error. It carries systemctl and
	// schtasks calls.
	Run func(name string, args ...string) error
	// SetRunKey and DeleteRunKey manage the Windows HKCU Run value. They are
	// nil off Windows.
	SetRunKey    func(name, command string) error
	DeleteRunKey func(name string) error
	// Logf receives progress and fallback messages; nil is silent.
	Logf func(format string, args ...any)
}

func (e Environment) logf(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
	}
}

// Install registers the Client to run at login.
func Install(env Environment, opts Options) error {
	if opts.Binary == "" {
		return fmt.Errorf("autostart: binary path is required")
	}
	switch env.GOOS {
	case "windows":
		return installWindows(env, opts)
	case "linux":
		return installLinux(env, opts)
	default:
		return fmt.Errorf("autostart: unsupported platform %q", env.GOOS)
	}
}

// Uninstall removes the login registration. It is idempotent: a missing
// registration is not an error.
func Uninstall(env Environment) error {
	switch env.GOOS {
	case "windows":
		return uninstallWindows(env)
	case "linux":
		return uninstallLinux(env)
	default:
		return fmt.Errorf("autostart: unsupported platform %q", env.GOOS)
	}
}
