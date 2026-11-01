//go:build desktop && linux

package wails

import "github.com/ebitengine/purego"

// desktopAvailable reports whether the GTK/WebKitGTK libraries this build needs
// are loadable. Wails' purego backend records a dlopen failure in an internal
// error and exits from inside its event loop, which would bypass the browser
// Settings fallback; probing first lets New return nil and the shell degrade
// cleanly. A library is opened with the same flags Wails uses.
func desktopAvailable() bool {
	for _, check := range desktopLibraries {
		handle, err := purego.Dlopen(check.name, purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil || handle == 0 {
			return false
		}
		for _, symbol := range check.symbols {
			if _, err := purego.Dlsym(handle, symbol); err != nil {
				_ = purego.Dlclose(handle)
				return false
			}
		}
		_ = purego.Dlclose(handle)
	}
	return true
}
