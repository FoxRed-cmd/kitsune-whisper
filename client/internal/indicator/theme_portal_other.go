//go:build !linux

package indicator

// readPortalColorScheme is unavailable outside Linux; DetectTheme falls back to
// the environment and the dark palette.
func readPortalColorScheme() (uint32, bool) { return 0, false }
