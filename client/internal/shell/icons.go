package shell

import (
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/brand"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/theme"
)

// Icon returns the Tray image for a state as PNG bytes. The images are rendered
// from the SVG brand mark in-process, with the palette matching the Settings
// window, so every platform and both colour schemes see the same smooth shape.
func Icon(state State) []byte {
	return brand.TrayPNG(brand.TrayState(state), theme.Detect())
}
