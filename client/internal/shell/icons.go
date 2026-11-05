package shell

import (
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/brand"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/theme"
)

// Icon returns the Tray image for a state as PNG bytes. The images are rendered
// from the SVG brand mark in-process, with the palette matching the Settings
// window, so every platform sees the same smooth shape. An empty or system
// setting follows the desktop colour scheme.
func Icon(state State, setting string) []byte {
	return brand.TrayPNG(brand.TrayState(state), theme.Resolve(setting))
}
