//go:build windows

package earcon

import (
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	sndMemory    = 0x0004
	sndNoDefault = 0x0008
)

var playSound = windows.NewLazySystemDLL("winmm.dll").NewProc("PlaySoundW")

// play renders the WAV on the default output device. PlaySound is synchronous,
// so it runs on a worker goroutine; that keeps the buffer alive for the whole
// playback and leaves the Dictation cycle unblocked.
func play(wav []byte) error {
	if len(wav) == 0 {
		return nil
	}
	buffer := append([]byte(nil), wav...)
	go func() {
		defer runtime.KeepAlive(buffer)
		_, _, _ = playSound.Call(0, uintptr(unsafe.Pointer(&buffer[0])), sndMemory|sndNoDefault)
	}()
	return nil
}
