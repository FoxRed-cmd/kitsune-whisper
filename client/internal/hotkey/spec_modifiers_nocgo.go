//go:build !windows && !cgo

package hotkey

import xhotkey "golang.design/x/hotkey"

// Without cgo on Unix, the hotkey package exposes no modifier constants, so no
// modifier name resolves.
var modifierNames = map[string]xhotkey.Modifier{}
