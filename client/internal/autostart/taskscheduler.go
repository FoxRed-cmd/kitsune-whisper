package autostart

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"unicode/utf16"
)

func taskXMLPath(configHome string) string {
	return filepath.Join(configHome, ConfigDirName, "autostart-task.xml")
}

// installWindows registers a per-user Task Scheduler task at logon. If task
// registration is denied, it falls back to the HKCU Run key, per
// docs/adr/0001-client-runs-in-user-session.md. It never registers a service.
func installWindows(env Environment, opts Options) error {
	xmlPath := taskXMLPath(env.ConfigHome)
	if err := writeUTF16File(xmlPath, taskXML(opts, env.User)); err != nil {
		return err
	}
	env.logf("autostart: wrote %s", xmlPath)

	if env.Run == nil {
		return nil
	}
	if err := env.Run("schtasks", "/Create", "/TN", TaskName, "/XML", xmlPath, "/F"); err != nil {
		env.logf("autostart: task registration failed: %v; falling back to HKCU Run", err)
		if env.SetRunKey == nil {
			return fmt.Errorf("autostart: register task %s: %w", TaskName, err)
		}
		if runErr := env.SetRunKey(RunValueName, runCommand(opts)); runErr != nil {
			return fmt.Errorf("autostart: register task %s: %w; HKCU Run fallback: %w", TaskName, err, runErr)
		}
		env.logf("autostart: registered HKCU Run value %q", RunValueName)
		return nil
	}
	env.logf("autostart: registered Task Scheduler task %q at logon", TaskName)
	if env.DeleteRunKey != nil {
		if err := env.DeleteRunKey(RunValueName); err != nil {
			env.logf("autostart: remove stale HKCU Run value %q: %v (ignored)", RunValueName, err)
		}
	}
	return nil
}

// uninstallWindows deletes the task and the HKCU Run fallback, then removes the
// cached task definition. Missing registrations are not errors.
func uninstallWindows(env Environment) error {
	if env.Run != nil {
		if err := env.Run("schtasks", "/Delete", "/TN", TaskName, "/F"); err != nil {
			env.logf("autostart: schtasks /Delete %s: %v (ignored)", TaskName, err)
		}
	}
	if env.DeleteRunKey != nil {
		if err := env.DeleteRunKey(RunValueName); err != nil {
			env.logf("autostart: delete HKCU Run value %q: %v (ignored)", RunValueName, err)
		}
	}
	return removeFile(taskXMLPath(env.ConfigHome))
}

// writeUTF16File writes content as UTF-16LE with a BOM, the encoding Task
// Scheduler expects for an /XML import.
func writeUTF16File(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("autostart: create %s: %w", filepath.Dir(path), err)
	}
	units := utf16.Encode([]rune(content))
	buf := make([]byte, 0, 2+len(units)*2)
	buf = append(buf, 0xFF, 0xFE)
	for _, unit := range units {
		buf = binary.LittleEndian.AppendUint16(buf, unit)
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		return fmt.Errorf("autostart: write %s: %w", path, err)
	}
	return nil
}
