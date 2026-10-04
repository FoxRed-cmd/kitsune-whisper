package autostart

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
)

// Default builds the production Environment for the current process.
func Default(logf func(format string, args ...any)) Environment {
	return Environment{
		GOOS:         runtime.GOOS,
		ConfigHome:   configHome(),
		DataHome:     dataHome(),
		User:         currentUser(),
		Run:          runExec,
		SetRunKey:    setRunKey,
		DeleteRunKey: deleteRunKey,
		Logf:         logf,
	}
}

// currentUser names the account for the per-user Windows task; "" lets Task
// Scheduler default to the registering user.
func currentUser() string {
	u, err := user.Current()
	if err != nil {
		return ""
	}
	return u.Username
}

// runExec runs a command and folds its combined output into the error, so a
// failing systemctl/schtasks call explains itself.
func runExec(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, trimOutput(output))
	}
	return nil
}

func trimOutput(output []byte) string {
	trimmed := string(output)
	for len(trimmed) > 0 && (trimmed[len(trimmed)-1] == '\n' || trimmed[len(trimmed)-1] == '\r') {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if trimmed == "" {
		return "(no output)"
	}
	return trimmed
}

func configHome() string {
	if runtime.GOOS == "windows" {
		if base := os.Getenv("APPDATA"); base != "" {
			return base
		}
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, "AppData", "Roaming")
		}
		return "."
	}
	if base := os.Getenv("XDG_CONFIG_HOME"); base != "" {
		return base
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".config")
	}
	return "."
}

func dataHome() string {
	if runtime.GOOS == "windows" {
		return configHome()
	}
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return base
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share")
	}
	return "."
}
