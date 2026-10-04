//go:build (linux || openbsd) && cgo

package hotkey_test

import (
	"testing"

	xhotkey "golang.design/x/hotkey"
)

func TestParsePlatformModifiers(t *testing.T) {
	assertParse(t, "Alt+D", []xhotkey.Modifier{xhotkey.Mod1}, xhotkey.KeyD)
	assertParse(t, "Super+A", []xhotkey.Modifier{xhotkey.Mod4}, xhotkey.KeyA)
	assertParse(t, "Mod3+A", []xhotkey.Modifier{xhotkey.Mod3}, xhotkey.KeyA)
}
