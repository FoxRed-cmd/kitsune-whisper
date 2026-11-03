//go:build windows

package i18n

import (
	"syscall"
	"unsafe"
)

var (
	kernel32                     = syscall.NewLazyDLL("kernel32.dll")
	procGetUserDefaultLocaleName = kernel32.NewProc("GetUserDefaultLocaleName")
)

// systemLocale returns the Windows user locale, e.g. "ru-RU". Windows does
// not export LANG through the process environment, so the locale has to come
// from the OS.
func systemLocale() string {
	const localeNameMaxLength = 85
	buf := make([]uint16, localeNameMaxLength)
	length, _, _ := procGetUserDefaultLocaleName.Call(
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
	)
	if length == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:length-1])
}
