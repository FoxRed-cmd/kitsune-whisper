package desktop

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/autostart"
)

func TestInstallWritesMenuEntryAndIcons(t *testing.T) {
	dataHome := t.TempDir()
	env := Environment{
		GOOS:     "linux",
		DataHome: dataHome,
		Binary:   "/home/u/.local/bin/kitsune-client",
	}
	if err := Install(env); err != nil {
		t.Fatalf("Install: %v", err)
	}

	entry := filepath.Join(dataHome, "applications", EntryFile)
	content, err := os.ReadFile(entry)
	if err != nil {
		t.Fatalf("read menu entry: %v", err)
	}
	for _, want := range []string{
		"Exec=/home/u/.local/bin/kitsune-client gui\n",
		"Icon=" + autostart.AppID,
		"Categories=AudioVideo;Audio;",
		"StartupNotify=true",
	} {
		if !bytes.Contains(content, []byte(want)) {
			t.Errorf("menu entry missing %q:\n%s", want, content)
		}
	}
	if bytes.Contains(content, []byte("NoDisplay=true")) {
		t.Errorf("menu entry is hidden:\n%s", content)
	}
	if bytes.Contains(content, []byte("--hidden")) {
		t.Errorf("menu entry starts the Tray without a window:\n%s", content)
	}

	svg := filepath.Join(dataHome, "icons", "hicolor", "scalable", "apps", autostart.AppID+".svg")
	svgContent, err := os.ReadFile(svg)
	if err != nil {
		t.Fatalf("read SVG icon: %v", err)
	}
	if !bytes.Contains(svgContent, []byte("<svg")) {
		t.Fatalf("SVG icon = %q", svgContent)
	}

	png := filepath.Join(dataHome, "icons", "hicolor", "256x256", "apps", autostart.AppID+".png")
	pngContent, err := os.ReadFile(png)
	if err != nil {
		t.Fatalf("read PNG icon: %v", err)
	}
	if !bytes.HasPrefix(pngContent, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("PNG icon does not start with the PNG signature")
	}
}

func TestInstallQuotesAnExecutableWithSpaces(t *testing.T) {
	dataHome := t.TempDir()
	if err := Install(Environment{
		GOOS:     "linux",
		DataHome: dataHome,
		Binary:   "/home/u/My Apps/kitsune-client",
	}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dataHome, "applications", EntryFile))
	if err != nil {
		t.Fatalf("read menu entry: %v", err)
	}
	if !bytes.Contains(content, []byte(`Exec="/home/u/My Apps/kitsune-client" gui`)) {
		t.Fatalf("menu entry did not quote the binary:\n%s", content)
	}
}

func TestInstallRequiresABinaryAndLinux(t *testing.T) {
	if err := Install(Environment{GOOS: "linux", DataHome: t.TempDir()}); err == nil {
		t.Fatal("Install without a binary succeeded")
	}
	if err := Install(Environment{GOOS: "windows", DataHome: t.TempDir(), Binary: "x"}); err == nil {
		t.Fatal("Install on Windows succeeded")
	}
}

func TestUninstallRemovesEverythingAndIsIdempotent(t *testing.T) {
	dataHome := t.TempDir()
	env := Environment{
		GOOS:     "linux",
		DataHome: dataHome,
		Binary:   "/home/u/.local/bin/kitsune-client",
	}
	if err := Install(env); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if err := Uninstall(env); err != nil {
		t.Fatalf("Uninstall: %v", err)
	}
	for _, path := range []string{
		filepath.Join(dataHome, "applications", EntryFile),
		filepath.Join(dataHome, "icons", "hicolor", "scalable", "apps", autostart.AppID+".svg"),
		filepath.Join(dataHome, "icons", "hicolor", "256x256", "apps", autostart.AppID+".png"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s still exists after uninstall: %v", path, err)
		}
	}
	if err := Uninstall(env); err != nil {
		t.Fatalf("second Uninstall: %v", err)
	}
}

func TestEntryFileIsSeparateFromThePortalEntry(t *testing.T) {
	if EntryFile == autostart.AppID+".desktop" {
		t.Fatal("the visible menu entry would overwrite the hidden portal entry")
	}
}
