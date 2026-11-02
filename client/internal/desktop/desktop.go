// Package desktop integrates the Client with the Linux desktop: a visible menu
// entry in the application launcher and the fox icon it points at. Autostart
// owns the login registration and its separate, hidden portal entry; this
// package owns the entry a user clicks.
package desktop

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/autostart"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/brand"
)

// EntryFile is the basename of the visible menu entry. It is deliberately
// separate from autostart's AppID-named file: that one must stay hidden and
// launch the Tray without a window, while this one opens Settings.
const EntryFile = "kitsune-whisper.desktop"

// IconPNGSize is the pixel size of the raster fallback installed next to the
// scalable SVG.
const IconPNGSize = 256

// Environment is the side-effect surface Install and Uninstall act on.
// Production callers pass Default; tests pass a temporary data directory.
type Environment struct {
	// GOOS selects the platform. Only Linux has a menu entry today.
	GOOS string
	// DataHome is the XDG data root (~/.local/share).
	DataHome string
	// Binary is the absolute path to the installed kitsune-client executable.
	Binary string
	// Logf receives progress messages; nil is silent.
	Logf func(format string, args ...any)
}

// Default builds the production Environment for the current process.
func Default(logf func(format string, args ...any)) Environment {
	return Environment{
		GOOS:     runtime.GOOS,
		DataHome: dataHome(),
		Logf:     logf,
	}
}

func (e Environment) logf(format string, args ...any) {
	if e.Logf != nil {
		e.Logf(format, args...)
	}
}

// Install writes the application icon and a menu entry that opens Settings. It
// is idempotent: a second run replaces the files with the current binary path.
func Install(env Environment) error {
	if env.GOOS != "linux" {
		return fmt.Errorf("desktop: unsupported platform %q", env.GOOS)
	}
	if env.Binary == "" {
		return errors.New("desktop: binary path is required")
	}
	appID := autostart.AppID
	svgPath := iconPath(env.DataHome, "scalable", appID+".svg")
	if err := writeFile(svgPath, brand.AppSVG()); err != nil {
		return err
	}
	pngPath := iconPath(env.DataHome, fmt.Sprintf("%dx%d", IconPNGSize, IconPNGSize), appID+".png")
	if png := brand.AppPNG(IconPNGSize); png != nil {
		if err := writeFile(pngPath, png); err != nil {
			return err
		}
	}
	entryPath := entryPath(env.DataHome)
	if err := writeFile(entryPath, []byte(entry(env.Binary))); err != nil {
		return err
	}
	env.logf("desktop: wrote %s", entryPath)
	env.logf("desktop: wrote %s", svgPath)
	return nil
}

// Uninstall removes the menu entry and both icon files. A missing file is not
// an error, so an upgrade or a repeated uninstall stays quiet.
func Uninstall(env Environment) error {
	if env.GOOS != "linux" {
		return fmt.Errorf("desktop: unsupported platform %q", env.GOOS)
	}
	appID := autostart.AppID
	paths := []string{
		entryPath(env.DataHome),
		iconPath(env.DataHome, "scalable", appID+".svg"),
		iconPath(env.DataHome, fmt.Sprintf("%dx%d", IconPNGSize, IconPNGSize), appID+".png"),
	}
	for _, path := range paths {
		if err := removeFile(path); err != nil {
			return err
		}
	}
	return nil
}

func entryPath(dataHome string) string {
	return filepath.Join(dataHome, "applications", EntryFile)
}

func iconPath(dataHome, size, name string) string {
	return filepath.Join(dataHome, "icons", "hicolor", size, "apps", name)
}

// entry renders the menu entry. Exec launches the visible shell, so clicking
// the launcher opens Settings; a second launch forwards to the running Tray.
func entry(binary string) string {
	return fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=kitsune-whisper
GenericName=Dictation
Comment=Private, LAN-local speech-to-text dictation
Exec=%s gui
Icon=%s
Terminal=false
Categories=AudioVideo;Audio;
Keywords=dictation;speech-to-text;transcription;voice;
StartupNotify=true
StartupWMClass=%s
`, autostart.ExecQuote(binary), autostart.AppID, autostart.AppID)
}

func writeFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("desktop: create %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("desktop: write %s: %w", path, err)
	}
	return nil
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("desktop: remove %s: %w", path, err)
	}
	return nil
}

// dataHome follows the same XDG resolution as autostart, so both installers
// write into the same tree.
func dataHome() string {
	if base := os.Getenv("XDG_DATA_HOME"); base != "" {
		return base
	}
	if home, err := os.UserHomeDir(); err == nil {
		return filepath.Join(home, ".local", "share")
	}
	return "."
}
