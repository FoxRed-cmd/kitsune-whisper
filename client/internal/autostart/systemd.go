package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

func systemdUnitPath(configHome string) string {
	return filepath.Join(configHome, "systemd", "user", UnitName)
}

func desktopEntryPath(dataHome string) string {
	return filepath.Join(dataHome, "applications", AppID+".desktop")
}

// installLinux writes the systemd user unit and the portal .desktop file, then
// enables and starts the unit. It binds to graphical-session.target and never
// enables linger.
func installLinux(env Environment, opts Options) error {
	unitPath := systemdUnitPath(env.ConfigHome)
	if err := writeFile(unitPath, unitFile(opts)); err != nil {
		return err
	}
	desktopPath := desktopEntryPath(env.DataHome)
	if err := writeFile(desktopPath, desktopEntry(opts)); err != nil {
		return err
	}
	env.logf("autostart: wrote %s", unitPath)
	env.logf("autostart: wrote %s", desktopPath)

	if env.Run == nil {
		return nil
	}
	if err := env.Run("systemctl", "--user", "daemon-reload"); err != nil {
		return fmt.Errorf("autostart: systemctl --user daemon-reload: %w", err)
	}
	if err := env.Run("systemctl", "--user", "enable", "--now", UnitName); err != nil {
		return fmt.Errorf("autostart: systemctl --user enable %s: %w", UnitName, err)
	}
	return nil
}

// uninstallLinux stops and disables the unit, removes the unit and .desktop
// files, and reloads systemd. A unit that was never enabled is not an error.
func uninstallLinux(env Environment) error {
	if env.Run != nil {
		if err := env.Run("systemctl", "--user", "disable", "--now", UnitName); err != nil {
			env.logf("autostart: systemctl --user disable %s: %v (ignored)", UnitName, err)
		}
	}
	if err := removeFile(systemdUnitPath(env.ConfigHome)); err != nil {
		return err
	}
	if err := removeFile(desktopEntryPath(env.DataHome)); err != nil {
		return err
	}
	if env.Run != nil {
		if err := env.Run("systemctl", "--user", "daemon-reload"); err != nil {
			env.logf("autostart: systemctl --user daemon-reload: %v (ignored)", err)
		}
	}
	return nil
}

func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("autostart: create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("autostart: write %s: %w", path, err)
	}
	return nil
}

// removeFile deletes path, treating an absent file as success so uninstall is
// idempotent.
func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("autostart: remove %s: %w", path, err)
	}
	return nil
}
