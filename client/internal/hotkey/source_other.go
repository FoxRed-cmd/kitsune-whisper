//go:build !linux

package hotkey

// newPlatformSource returns the native global-hotkey source. The Wayland
// backends are Linux-only, so the configured backend is ignored here.
func newPlatformSource(opts Options) (Source, error) {
	return newKeySource(opts)
}
