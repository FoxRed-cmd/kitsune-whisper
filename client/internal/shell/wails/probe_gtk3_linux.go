//go:build desktop && linux && gtk3

package wails

// desktopLibraries are the sonames the legacy GTK3/WebKit2GTK 4.1 build loads
// at runtime, plus a representative symbol from each.
var desktopLibraries = []struct {
	name    string
	symbols []string
}{
	{"libgtk-3.so.0", []string{"gtk_application_new"}},
	{"libwebkit2gtk-4.1.so.0", []string{"webkit_web_view_new"}},
}
