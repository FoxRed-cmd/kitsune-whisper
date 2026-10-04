package hotkey

import (
	"fmt"
	"strings"

	xhotkey "golang.design/x/hotkey"
)

// keyNames and modifierNames are provided per platform (spec_keys_*.go,
// spec_modifiers_*.go), because golang.design/x/hotkey exposes a different key
// and modifier set on each platform, and none without cgo on Unix.

// ParseSpec parses a config hotkey such as "Ctrl+Shift+Space" or "Esc" into the
// modifiers and key expected by golang.design/x/hotkey. Names are matched
// case-insensitively; the final "+"-separated token is the key and every
// preceding token must be a modifier.
func ParseSpec(spec string) ([]xhotkey.Modifier, xhotkey.Key, error) {
	parts := strings.Split(spec, "+")
	var mods []xhotkey.Modifier
	seen := map[xhotkey.Modifier]bool{}
	for i, raw := range parts {
		token := strings.ToLower(strings.TrimSpace(raw))
		if token == "" {
			return nil, 0, fmt.Errorf("hotkey %q has an empty token", spec)
		}
		if i == len(parts)-1 {
			key, ok := keyNames[token]
			if !ok {
				return nil, 0, fmt.Errorf("hotkey %q: unknown key %q", spec, raw)
			}
			return mods, key, nil
		}
		mod, ok := modifierNames[token]
		if !ok {
			return nil, 0, fmt.Errorf("hotkey %q: unknown modifier %q", spec, raw)
		}
		if seen[mod] {
			return nil, 0, fmt.Errorf("hotkey %q: duplicate modifier %q", spec, raw)
		}
		seen[mod] = true
		mods = append(mods, mod)
	}
	return nil, 0, fmt.Errorf("hotkey %q must name a key", spec)
}
