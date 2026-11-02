//go:build desktop && !windows

package wails

// quitApp dispatches Quit asynchronously. Wails' Quit waits for the GTK main
// loop to run the destroy callback, while the shell calls it from its watch
// goroutine; a tray update already queued on the main loop can then deadlock the
// two. Quitting in the background lets the shell stop the runtime and return,
// and the process exits even if the platform loop is still draining.
func (d *desktop) quitApp() {
	go d.app.Quit()
}
