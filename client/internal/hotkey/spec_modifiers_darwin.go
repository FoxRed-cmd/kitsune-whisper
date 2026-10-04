//go:build darwin && cgo

package hotkey

import xhotkey "golang.design/x/hotkey"

// modifierNames maps config modifier aliases to the platform's constants.
// On macOS, Alt is the Option key and Command is the platform "Win" key.
var modifierNames = map[string]xhotkey.Modifier{
	"ctrl":    xhotkey.ModCtrl,
	"control": xhotkey.ModCtrl,
	"shift":   xhotkey.ModShift,
	"alt":     xhotkey.ModOption,
	"option":  xhotkey.ModOption,
	"win":     xhotkey.ModCmd,
	"super":   xhotkey.ModCmd,
	"meta":    xhotkey.ModCmd,
	"cmd":     xhotkey.ModCmd,
	"command": xhotkey.ModCmd,
}
