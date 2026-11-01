package hotkey

import (
	"fmt"
	"strings"
)

// gtkModifiers maps config modifier aliases onto GTK accelerator modifiers.
var gtkModifiers = map[string]string{
	"ctrl":    "<Control>",
	"control": "<Control>",
	"shift":   "<Shift>",
	"alt":     "<Alt>",
	"mod1":    "<Alt>",
	"win":     "<Super>",
	"super":   "<Super>",
	"meta":    "<Super>",
	"cmd":     "<Super>",
	"mod4":    "<Super>",
}

// GTKAccelerator converts a config hotkey spec such as "Ctrl+Shift+Space" into
// the accelerator GNOME's custom-keybinding GSettings expect, such as
// "<Control><Shift>space". It fails on a spec ParseSpec would reject and on a
// key without a GTK name.
func GTKAccelerator(spec string) (string, error) {
	parts := strings.Split(spec, "+")
	if len(parts) == 0 {
		return "", fmt.Errorf("hotkey %q must name a key", spec)
	}
	var out strings.Builder
	seen := map[string]bool{}
	for i, raw := range parts {
		token := strings.ToLower(strings.TrimSpace(raw))
		if token == "" {
			return "", fmt.Errorf("hotkey %q has an empty token", spec)
		}
		if i == len(parts)-1 {
			key, err := portalKey(token)
			if err != nil {
				return "", fmt.Errorf("hotkey %q: unknown key %q", spec, raw)
			}
			out.WriteString(key)
			continue
		}
		modifier, ok := gtkModifiers[token]
		if !ok {
			return "", fmt.Errorf("hotkey %q: unknown modifier %q", spec, raw)
		}
		if seen[modifier] {
			return "", fmt.Errorf("hotkey %q: duplicate modifier %q", spec, raw)
		}
		seen[modifier] = true
		out.WriteString(modifier)
	}
	return out.String(), nil
}
