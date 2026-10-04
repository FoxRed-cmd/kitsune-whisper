package autostart_test

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/autostart"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/hotkey"
)

// utf16le encodes s as Task Scheduler stores it (UTF-16LE, no BOM).
func utf16le(s string) []byte {
	var buf []byte
	for _, unit := range utf16.Encode([]rune(s)) {
		buf = binary.LittleEndian.AppendUint16(buf, unit)
	}
	return buf
}

type call struct {
	name string
	args []string
}

// fake is an autostart.Environment whose side effects are captured instead of
// touching a real session.
type fake struct {
	env        autostart.Environment
	calls      []call
	runKeys    map[string]string
	failRun    func(name string, args []string) error
	failRunKey error
}

func newFake(t *testing.T, goos string) *fake {
	t.Helper()
	base := t.TempDir()
	f := &fake{runKeys: map[string]string{}}
	f.env = autostart.Environment{
		GOOS:       goos,
		ConfigHome: filepath.Join(base, "config"),
		DataHome:   filepath.Join(base, "data"),
		User:       `DOMAIN\user`,
		Run: func(name string, args ...string) error {
			f.calls = append(f.calls, call{name: name, args: append([]string(nil), args...)})
			if f.failRun != nil {
				return f.failRun(name, args)
			}
			return nil
		},
		SetRunKey: func(name, command string) error {
			if f.failRunKey != nil {
				return f.failRunKey
			}
			f.runKeys[name] = command
			return nil
		},
		DeleteRunKey: func(name string) error {
			delete(f.runKeys, name)
			return nil
		},
	}
	return f
}

func (f *fake) ran(name string, args ...string) bool {
	for _, c := range f.calls {
		if c.name != name || len(c.args) != len(args) {
			continue
		}
		match := true
		for i := range args {
			if c.args[i] != args[i] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestAppIDMatchesPortalAppID(t *testing.T) {
	if autostart.AppID != hotkey.DefaultAppID {
		t.Fatalf("autostart.AppID = %q, hotkey.DefaultAppID = %q", autostart.AppID, hotkey.DefaultAppID)
	}
}

func TestInstallRejectsEmptyBinary(t *testing.T) {
	f := newFake(t, "linux")
	if err := autostart.Install(f.env, autostart.Options{}); err == nil {
		t.Fatal("Install with no binary should fail")
	}
}

func TestInstallRejectsUnsupportedPlatform(t *testing.T) {
	f := newFake(t, "darwin")
	if err := autostart.Install(f.env, autostart.Options{Binary: "/x/kitsune-client"}); err == nil {
		t.Fatal("Install on an unsupported platform should fail")
	}
}

func TestInstallLinuxWritesUnitDesktopAndEnables(t *testing.T) {
	f := newFake(t, "linux")
	binary := "/home/u/.local/bin/kitsune-client"
	if err := autostart.Install(f.env, autostart.Options{Binary: binary}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	unit := filepath.Join(f.env.ConfigHome, "systemd", "user", autostart.UnitName)
	data, err := os.ReadFile(unit)
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	for _, want := range []string{"graphical-session.target", "Restart=on-failure", "ExecStart=" + binary + " run"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("unit missing %q:\n%s", want, data)
		}
	}

	desktop := filepath.Join(f.env.DataHome, "applications", autostart.AppID+".desktop")
	if _, err := os.Stat(desktop); err != nil {
		t.Fatalf("desktop entry basename must be the app id: %v", err)
	}

	if !f.ran("systemctl", "--user", "daemon-reload") {
		t.Error("missing systemctl --user daemon-reload")
	}
	if !f.ran("systemctl", "--user", "enable", "--now", autostart.UnitName) {
		t.Error("missing systemctl --user enable --now")
	}
}

func TestInstallLinuxPinsExplicitConfig(t *testing.T) {
	f := newFake(t, "linux")
	if err := autostart.Install(f.env, autostart.Options{
		Binary: "/home/u/.local/bin/kitsune-client",
		Config: "/home/u/kitsune.yaml",
	}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	unit := filepath.Join(f.env.ConfigHome, "systemd", "user", autostart.UnitName)
	data, err := os.ReadFile(unit)
	if err != nil {
		t.Fatalf("read unit: %v", err)
	}
	if !strings.Contains(string(data), "--config /home/u/kitsune.yaml run") {
		t.Fatalf("unit did not pin the config:\n%s", data)
	}
}

func TestUninstallLinuxStopsAndRemoves(t *testing.T) {
	f := newFake(t, "linux")
	opts := autostart.Options{Binary: "/home/u/.local/bin/kitsune-client"}
	if err := autostart.Install(f.env, opts); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := autostart.Uninstall(f.env); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !f.ran("systemctl", "--user", "disable", "--now", autostart.UnitName) {
		t.Error("missing systemctl --user disable --now")
	}
	unit := filepath.Join(f.env.ConfigHome, "systemd", "user", autostart.UnitName)
	if _, err := os.Stat(unit); !os.IsNotExist(err) {
		t.Errorf("unit still present after uninstall: %v", err)
	}
	desktop := filepath.Join(f.env.DataHome, "applications", autostart.AppID+".desktop")
	if _, err := os.Stat(desktop); !os.IsNotExist(err) {
		t.Errorf("desktop entry still present after uninstall: %v", err)
	}
}

func TestUninstallIsIdempotent(t *testing.T) {
	f := newFake(t, "linux")
	f.failRun = func(string, []string) error { return os.ErrNotExist }
	if err := autostart.Uninstall(f.env); err != nil {
		t.Fatalf("Uninstall without a prior install: %v", err)
	}
}

func TestInstallWindowsRegistersTask(t *testing.T) {
	f := newFake(t, "windows")
	binary := `C:\Users\u\AppData\Local\kitsune-whisper\bin\kitsune-client.exe`
	if err := autostart.Install(f.env, autostart.Options{Binary: binary}); err != nil {
		t.Fatalf("Install: %v", err)
	}

	xmlPath := filepath.Join(f.env.ConfigHome, autostart.ConfigDirName, "autostart-task.xml")
	data, err := os.ReadFile(xmlPath)
	if err != nil {
		t.Fatalf("read task xml: %v", err)
	}
	if len(data) < 2 || data[0] != 0xFF || data[1] != 0xFE {
		t.Fatalf("task xml is not UTF-16LE with a BOM: % x", data)
	}
	if !bytes.Contains(data, utf16le(`<UserId>DOMAIN\user</UserId>`)) {
		t.Error("task xml is not scoped to the installing user")
	}
	if !f.ran("schtasks", "/Create", "/TN", autostart.TaskName, "/XML", xmlPath, "/F") {
		t.Error("missing schtasks /Create")
	}
	if len(f.runKeys) != 0 {
		t.Errorf("Run key should not be written when the task registers: %v", f.runKeys)
	}
}

func TestInstallWindowsFallsBackToRunKey(t *testing.T) {
	f := newFake(t, "windows")
	f.failRun = func(name string, _ []string) error {
		if name == "schtasks" {
			return os.ErrPermission
		}
		return nil
	}
	binary := `C:\Program Files\kitsune-whisper\kitsune-client.exe`
	if err := autostart.Install(f.env, autostart.Options{Binary: binary}); err != nil {
		t.Fatalf("Install should fall back, got %v", err)
	}
	command, ok := f.runKeys[autostart.RunValueName]
	if !ok {
		t.Fatalf("Run key %q not written", autostart.RunValueName)
	}
	if !strings.Contains(command, binary) || !strings.HasSuffix(command, " run") {
		t.Fatalf("Run key command = %q", command)
	}
}

func TestInstallWindowsReportsBothFailures(t *testing.T) {
	f := newFake(t, "windows")
	f.failRun = func(string, []string) error { return os.ErrPermission }
	f.failRunKey = os.ErrPermission
	err := autostart.Install(f.env, autostart.Options{Binary: `C:\kw\kitsune-client.exe`})
	if err == nil {
		t.Fatal("Install should fail when both task and Run key fail")
	}
}

func TestUninstallWindowsDeletesTaskAndRunKey(t *testing.T) {
	f := newFake(t, "windows")
	f.runKeys[autostart.RunValueName] = `C:\kw\kitsune-client.exe run`
	if err := autostart.Uninstall(f.env); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	if !f.ran("schtasks", "/Delete", "/TN", autostart.TaskName, "/F") {
		t.Error("missing schtasks /Delete")
	}
	if _, ok := f.runKeys[autostart.RunValueName]; ok {
		t.Error("Run key not removed")
	}
}
