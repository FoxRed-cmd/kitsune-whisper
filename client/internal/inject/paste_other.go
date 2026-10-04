//go:build !windows && !linux

package inject

import "errors"

// unsupportedPaster is the fallback on platforms without a paste backend.
type unsupportedPaster struct{}

func newPaster() Paster { return unsupportedPaster{} }

func (unsupportedPaster) Paste(Shortcut) error {
	return errors.New("synthetic paste is not supported on this platform")
}

// unsupportedFocus cannot identify the focused app.
type unsupportedFocus struct{}

func newFocus() Focus { return unsupportedFocus{} }

func (unsupportedFocus) ForegroundApp() string { return "" }
