//go:build desktop && linux && !gtk3

package wails

// desktopLibraries are the sonames the GTK4/WebKitGTK 6.0 build loads at
// runtime, plus a representative symbol from each.
var desktopLibraries = []struct {
	name    string
	symbols []string
}{
	{"libgtk-4.so.1", []string{"gtk_application_new"}},
	{"libwebkitgtk-6.0.so.4", []string{"webkit_web_view_new"}},
}
