//go:build desktop && !linux

package wails

// desktopAvailable is true off Linux: WebView2 and the macOS frameworks are
// present whenever the build can link them.
func desktopAvailable() bool { return true }
