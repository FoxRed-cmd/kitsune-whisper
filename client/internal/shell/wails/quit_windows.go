//go:build desktop && windows

package wails

// quitApp runs Wails' Quit synchronously. Its native handler removes the Tray
// with Shell_NotifyIcon(NIM_DELETE) before the message loop ends. The process
// must not exit first: a notification icon whose owner vanished stays in the
// notification area as a ghost that ignores clicks and never changes colour,
// so the next launch looks dead next to the live Tray in the overflow.
func (d *desktop) quitApp() {
	d.app.Quit()
}
