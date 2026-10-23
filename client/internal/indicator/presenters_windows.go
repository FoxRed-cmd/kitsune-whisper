//go:build windows

package indicator

// PlatformFactories returns inert presenters on Windows: the native Win32
// layered-window presenter is deferred (#42), so the Client keeps dictating
// silently.
func PlatformFactories() Factories {
	unsupported := func() (Presenter, error) { return nil, ErrUnsupported }
	return Factories{LayerShell: unsupported, X11: unsupported}
}
