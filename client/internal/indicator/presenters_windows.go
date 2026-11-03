//go:build windows

package indicator

// PlatformFactories returns the presenters available on Windows.
func PlatformFactories() Factories {
	return Factories{Windows: NewWin32}
}

// PlatformEnvironment reports the host desktop. Windows is decisive: display
// environment variables from a bundled X or Wayland server stay ignored.
func PlatformEnvironment(_, _ bool) Environment {
	return Environment{Windows: true}
}
