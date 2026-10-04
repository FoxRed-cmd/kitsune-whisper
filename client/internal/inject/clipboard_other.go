//go:build !linux

package inject

// wrapClipboard returns the native clipboard unchanged off Linux; the
// wl-copy/wl-paste fallback is a Wayland-only concern.
func wrapClipboard(primary Clipboard) Clipboard { return primary }
