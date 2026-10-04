//go:build !windows && !linux

package inject

// newPaster returns no paster: this platform has no synthetic-paste backend, so
// the Injector degrades to clipboard-only.
func newPaster(string) (Paster, error) { return nil, nil }

// unsupportedFocus cannot identify the focused app.
type unsupportedFocus struct{}

func newFocus() Focus { return unsupportedFocus{} }

func (unsupportedFocus) ForegroundApp() string { return "" }
