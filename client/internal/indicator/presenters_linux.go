//go:build linux

package indicator

// PlatformFactories returns the presenters available on Linux.
func PlatformFactories() Factories {
	return Factories{
		LayerShell: NewLayerShell,
		X11:        NewX11,
	}
}

// PlatformEnvironment turns the detected Linux session into presenter choices.
func PlatformEnvironment(wayland, x11 bool) Environment {
	return Environment{LayerShell: wayland, X11: x11}
}
