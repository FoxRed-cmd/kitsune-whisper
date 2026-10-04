//go:build windows

package autostart

import (
	"errors"

	"golang.org/x/sys/windows/registry"
)

const runKeyPath = `Software\Microsoft\Windows\CurrentVersion\Run`

// setRunKey writes the HKCU Run value, the fallback when Task Scheduler
// registration is denied. A per-user Run entry runs in the user session, so the
// client's hotkeys and clipboard keep working; a service would not.
func setRunKey(name, command string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	return key.SetStringValue(name, command)
}

func deleteRunKey(name string) error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer key.Close()
	if err := key.DeleteValue(name); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}
