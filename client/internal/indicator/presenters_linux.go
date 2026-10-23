//go:build linux

package indicator

// PlatformFactories returns the presenters available on Linux.
func PlatformFactories() Factories {
	return Factories{
		LayerShell: NewLayerShell,
		X11:        NewX11,
	}
}
