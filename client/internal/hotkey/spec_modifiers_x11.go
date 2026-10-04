//go:build (linux || openbsd) && cgo

package hotkey

import xhotkey "golang.design/x/hotkey"

// modifierNames maps config modifier aliases to the platform's constants.
// On X11, Alt is normally Mod1 and Super/Win is Mod4.
var modifierNames = map[string]xhotkey.Modifier{
	"ctrl":    xhotkey.ModCtrl,
	"control": xhotkey.ModCtrl,
	"shift":   xhotkey.ModShift,
	"alt":     xhotkey.Mod1,
	"win":     xhotkey.Mod4,
	"super":   xhotkey.Mod4,
	"meta":    xhotkey.Mod4,
	"cmd":     xhotkey.Mod4,
	"mod1":    xhotkey.Mod1,
	"mod2":    xhotkey.Mod2,
	"mod3":    xhotkey.Mod3,
	"mod4":    xhotkey.Mod4,
	"mod5":    xhotkey.Mod5,
}
