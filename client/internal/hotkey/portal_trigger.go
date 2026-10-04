package hotkey

import (
	"fmt"
	"strconv"
	"strings"
)

// portalModifiers maps config modifier aliases onto the freedesktop shortcuts
// specification's modifier identifiers (CTRL, ALT, SHIFT, NUM, LOGO).
var portalModifiers = map[string]string{
	"ctrl":    "CTRL",
	"control": "CTRL",
	"shift":   "SHIFT",
	"alt":     "ALT",
	"mod1":    "ALT",
	"win":     "LOGO",
	"super":   "LOGO",
	"meta":    "LOGO",
	"cmd":     "LOGO",
	"mod4":    "LOGO",
}

// portalKeys maps the non-alphanumeric config key aliases onto their xkbcommon
// keysym names, which the shortcuts specification uses with the XKB_KEY_ prefix
// stripped. Letters, digits, and function keys are derived below.
var portalKeys = map[string]string{
	"space":  "space",
	"escape": "Escape",
	"esc":    "Escape",
	"return": "Return",
	"enter":  "Return",
	"tab":    "Tab",
	"delete": "Delete",
	"left":   "Left",
	"right":  "Right",
	"up":     "Up",
	"down":   "Down",

	"volumeup":   "XF86AudioRaiseVolume",
	"volumedown": "XF86AudioLowerVolume",
	"volumemute": "XF86AudioMute",

	"mediaplaypause": "XF86AudioPlay",
	"medianext":      "XF86AudioNext",
	"mediaprev":      "XF86AudioPrev",
	"mediastop":      "XF86AudioStop",
}

// PortalTrigger converts a config hotkey spec such as "Ctrl+Shift+Space" into
// the accelerator string the GlobalShortcuts portal expects, such as
// "CTRL+SHIFT+space" (see the freedesktop shortcuts specification). It fails on
// a spec that ParseSpec would reject and on keys with no portal equivalent.
func PortalTrigger(spec string) (string, error) {
	parts := strings.Split(spec, "+")
	if len(parts) == 0 {
		return "", fmt.Errorf("hotkey %q must name a key", spec)
	}
	accel := make([]string, 0, len(parts))
	for i, raw := range parts {
		token := strings.ToLower(strings.TrimSpace(raw))
		if token == "" {
			return "", fmt.Errorf("hotkey %q has an empty token", spec)
		}
		if i == len(parts)-1 {
			key, err := portalKey(token)
			if err != nil {
				return "", fmt.Errorf("hotkey %q: %w", spec, err)
			}
			accel = append(accel, key)
			continue
		}
		mod, ok := portalModifiers[token]
		if !ok {
			return "", fmt.Errorf("hotkey %q: modifier %q has no portal equivalent", spec, raw)
		}
		accel = append(accel, mod)
	}
	if len(accel) == 1 && len(parts) == 1 {
		return accel[0], nil
	}
	return strings.Join(accel, "+"), nil
}

func portalKey(token string) (string, error) {
	if name, ok := portalKeys[token]; ok {
		return name, nil
	}
	if len(token) == 1 {
		if isLowerLetter(token[0]) || isDigit(token[0]) {
			return token, nil
		}
	}
	if strings.HasPrefix(token, "f") {
		if n, err := strconv.Atoi(token[1:]); err == nil && n >= 1 && n <= 20 {
			return "F" + strconv.Itoa(n), nil
		}
	}
	return "", fmt.Errorf("key %q has no portal equivalent", token)
}

func isLowerLetter(b byte) bool { return b >= 'a' && b <= 'z' }
func isDigit(b byte) bool       { return b >= '0' && b <= '9' }
