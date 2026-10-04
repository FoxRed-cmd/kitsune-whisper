//go:build windows

package hotkey

import xhotkey "golang.design/x/hotkey"

// modifierNames maps config modifier aliases to the platform's constants.
// golang.design/x/hotkey defines different modifier sets per platform, so each
// platform provides its own map.
var modifierNames = map[string]xhotkey.Modifier{
	"ctrl":    xhotkey.ModCtrl,
	"control": xhotkey.ModCtrl,
	"shift":   xhotkey.ModShift,
	"alt":     xhotkey.ModAlt,
	"win":     xhotkey.ModWin,
	"super":   xhotkey.ModWin,
	"meta":    xhotkey.ModWin,
	"cmd":     xhotkey.ModWin,
}
