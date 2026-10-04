//go:build !windows && !cgo

package hotkey

import xhotkey "golang.design/x/hotkey"

// Without cgo on Unix, golang.design/x/hotkey exposes no key constants (it
// compiles a stub whose Register always fails). Parsing still compiles, but no
// key name resolves.
var keyNames = map[string]xhotkey.Key{}
