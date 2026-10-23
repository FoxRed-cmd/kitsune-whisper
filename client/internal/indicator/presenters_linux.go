//go:build linux

package indicator

// PlatformFactories returns the presenters available on Linux.
func PlatformFactories() Factories {
	return Factories{
		LayerShell: func() (Presenter, error) { return NewLayerShell() },
		X11:        func() (Presenter, error) { return NewX11() },
	}
}
