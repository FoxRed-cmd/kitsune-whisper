//go:build windows

package inject

import (
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
)

// windowsFocus resolves the foreground window's process image name, for the
// terminal-aware default paste shortcut.
type windowsFocus struct{}

func newFocus() Focus { return windowsFocus{} }

func (windowsFocus) ForegroundApp() string {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		return ""
	}
	var pid uint32
	if _, _, _ = procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid))); pid == 0 {
		return ""
	}
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	buf := make([]uint16, windows.MAX_PATH)
	size := uint32(len(buf))
	if err := windows.QueryFullProcessImageName(handle, 0, &buf[0], &size); err != nil {
		return ""
	}
	return filepath.Base(windows.UTF16ToString(buf[:size]))
}
