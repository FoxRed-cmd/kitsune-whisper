//go:build !windows

package startup

// The HKCU Run fallback exists only on Windows. The controller never consults
// it elsewhere, but the type must still compile for every platform.
type registryRunKey struct{}

func (registryRunKey) Present() (bool, error) { return false, nil }

func (registryRunKey) Remove() error { return nil }
