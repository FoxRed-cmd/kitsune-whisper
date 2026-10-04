//go:build windows

package hotkey_test

import (
	"testing"

	xhotkey "golang.design/x/hotkey"
)

func TestParsePlatformModifiers(t *testing.T) {
	assertParse(t, "Alt+D", []xhotkey.Modifier{xhotkey.ModAlt}, xhotkey.KeyD)
	assertParse(t, "Win+A", []xhotkey.Modifier{xhotkey.ModWin}, xhotkey.KeyA)
	assertParse(t, "Ctrl+Alt+Delete", []xhotkey.Modifier{xhotkey.ModCtrl, xhotkey.ModAlt}, xhotkey.KeyDelete)
}
