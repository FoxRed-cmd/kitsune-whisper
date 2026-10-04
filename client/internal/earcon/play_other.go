//go:build !windows && !linux

package earcon

// play is a no-op where no supported player exists.
func play([]byte) error { return nil }
