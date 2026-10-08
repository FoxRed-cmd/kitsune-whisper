package inject

import "fmt"

// errNoWaylandTool means no Wayland input helper is available, so the
// transcription can only be left on the clipboard.
var errNoWaylandTool = fmt.Errorf("no Wayland input helper available")

// waylandTool is a Wayland synthetic-input helper.
type waylandTool int

const (
	// toolWtype types through the wlroots virtual-keyboard protocol.
	toolWtype waylandTool = iota
	// toolYdotool synthesizes input through /dev/uinput.
	toolYdotool
)

// resolveWaylandTools resolves the configured wayland_tool against the detected
// helpers, in the order the injection chain should try them. "auto" lists
// wtype then ydotool; an explicit helper lists only itself. A requested helper
// that is not installed yields an empty chain, which degrades to clipboard-only
// rather than failing, keeping Wayland injection best-effort.
func resolveWaylandTools(requested string, hasWtype, hasYdotool bool) ([]waylandTool, error) {
	switch requested {
	case "", "auto":
		return presentTools(hasWtype, hasYdotool), nil
	case "wtype":
		if hasWtype {
			return []waylandTool{toolWtype}, nil
		}
		return nil, nil
	case "ydotool":
		if hasYdotool {
			return []waylandTool{toolYdotool}, nil
		}
		return nil, nil
	case "none":
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown wayland_tool %q", requested)
	}
}

func presentTools(hasWtype, hasYdotool bool) []waylandTool {
	var tools []waylandTool
	if hasWtype {
		tools = append(tools, toolWtype)
	}
	if hasYdotool {
		tools = append(tools, toolYdotool)
	}
	return tools
}

// runChain tries each tool in order, returning nil on the first success. When
// every tool fails it returns the last error, or errNoWaylandTool for an empty
// chain; the caller then leaves the transcription on the clipboard.
func runChain(tools []waylandTool, shortcut Shortcut, run func(waylandTool, Shortcut) error) error {
	var lastErr error
	for _, tool := range tools {
		if err := run(tool, shortcut); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	if lastErr == nil {
		return errNoWaylandTool
	}
	return lastErr
}

// waylandArgs returns the command and arguments that synthesize a paste chord
// with a Wayland helper. Keycodes are Linux input-event codes (Ctrl=29,
// Shift=42, V=47, Insert=110); keysyms are the wtype names.
func waylandArgs(tool waylandTool, shortcut Shortcut) (string, []string) {
	switch tool {
	case toolWtype:
		switch shortcut {
		case CtrlShiftV:
			return "wtype", []string{"-M", "ctrl", "-M", "shift", "-k", "v", "-m", "shift", "-m", "ctrl"}
		case ShiftInsert:
			return "wtype", []string{"-M", "shift", "-k", "Insert", "-m", "shift"}
		default:
			return "wtype", []string{"-M", "ctrl", "-k", "v", "-m", "ctrl"}
		}
	case toolYdotool:
		switch shortcut {
		case CtrlShiftV:
			return "ydotool", []string{"key", "29:1", "42:1", "47:1", "47:0", "42:0", "29:0"}
		case ShiftInsert:
			return "ydotool", []string{"key", "42:1", "110:1", "110:0", "42:0"}
		default:
			return "ydotool", []string{"key", "29:1", "47:1", "47:0", "29:0"}
		}
	default:
		return "", nil
	}
}
