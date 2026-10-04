package inject

import "strings"

// terminalSubstrings match anywhere in a focused-app name; terminals name
// themselves in many ways but share these fragments.
var terminalSubstrings = []string{"terminal", "console", "conhost", "cascadia"}

// terminalNames match a focused-app name exactly.
var terminalNames = map[string]bool{
	"cmd": true, "cmd.exe": true,
	"powershell": true, "powershell.exe": true, "pwsh": true, "pwsh.exe": true,
	"mintty": true, "mintty.exe": true,
	"xterm": true, "urxvt": true, "rxvt": true,
	"kitty": true, "alacritty": true,
	"wezterm": true, "wezterm-gui": true, "wezterm-gui.exe": true,
	"st": true, "st-256color": true, "konsole": true, "guake": true,
	"terminator": true, "tilix": true, "termite": true, "foot": true,
	"contour": true, "xfce4-terminal": true, "lxterminal": true,
	"mate-terminal": true, "gnome-terminal": true, "gnome-terminal-server": true,
	"windowsterminal": true, "windowsterminal.exe": true, "wt": true, "wt.exe": true,
}

// isTerminal reports whether a focused-app class or process name names a
// terminal emulator, for the terminal-aware default paste shortcut.
func isTerminal(app string) bool {
	name := strings.ToLower(strings.TrimSpace(app))
	if name == "" {
		return false
	}
	for _, needle := range terminalSubstrings {
		if strings.Contains(name, needle) {
			return true
		}
	}
	return terminalNames[name]
}
