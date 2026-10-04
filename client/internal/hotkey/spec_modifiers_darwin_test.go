//go:build darwin && cgo

package hotkey_test

import (
	"testing"

	xhotkey "golang.design/x/hotkey"
)

func TestParsePlatformModifiers(t *testing.T) {
	assertParse(t, "Alt+D", []xhotkey.Modifier{xhotkey.ModOption}, xhotkey.KeyD)
	assertParse(t, "Cmd+A", []xhotkey.Modifier{xhotkey.ModCmd}, xhotkey.KeyA)
}
