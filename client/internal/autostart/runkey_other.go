//go:build !windows

package autostart

import "errors"

// The HKCU Run key is a Windows fallback only; elsewhere these are inert so the
// package still compiles for other platforms.

func setRunKey(string, string) error {
	return errors.New("autostart: the HKCU Run key is only available on Windows")
}

func deleteRunKey(string) error { return nil }
