//go:build !linux && !windows

package indicator

// PlatformFactories returns inert presenters on platforms without a Recording
// indicator implementation.
func PlatformFactories() Factories {
	unsupported := func() (Presenter, error) { return nil, ErrUnsupported }
	return Factories{LayerShell: unsupported, X11: unsupported}
}

// PlatformEnvironment reports no display the presenters can use.
func PlatformEnvironment(_, _ bool) Environment { return Environment{} }
