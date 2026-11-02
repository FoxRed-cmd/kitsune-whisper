//go:build !linux

package theme

// readPortalColorScheme is unavailable outside Linux; Detect falls back to the
// environment and the dark palette.
func readPortalColorScheme() (uint32, bool) { return 0, false }
