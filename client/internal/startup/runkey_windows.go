//go:build windows

package startup

import (
	"errors"

	"golang.org/x/sys/windows/registry"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/autostart"
)

const (
	runKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName = autostart.RunValueName
)

// registryRunKey is the HKCU Run fallback the Windows installer writes when
// Task Scheduler registration is denied (docs/adr/0001). Disabling autostart
// deletes the value; the Startup switch recreates a full registration.
type registryRunKey struct{}

func (registryRunKey) Present() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	defer key.Close()
	if _, _, err := key.GetStringValue(runValueName); err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (registryRunKey) Remove() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer key.Close()
	if err := key.DeleteValue(runValueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}
