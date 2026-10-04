//go:build windows

package inject

import (
	"fmt"
	"unsafe"

	"golang.org/x/sys/windows"
)

// windowsPaster synthesizes the paste keystroke with SendInput, matching the
// prototype's approach (cgo-free).
type windowsPaster struct{}

func newPaster(string) (Paster, error) { return windowsPaster{}, nil }

const (
	inputKeyboard  = 1
	keyeventfKeyup = 0x0002
	vkControl      = 0x11
	vkShift        = 0x10
	vkV            = 0x56
)

type keybdInput struct {
	vk        uint16
	scan      uint16
	flags     uint32
	time      uint32
	extraInfo uintptr
}

// input is the KEYBDINPUT-bearing INPUT struct: 40 bytes on amd64, matching the
// Win32 layout (type, padding, union, padding).
type input struct {
	typ uint32
	_   uint32
	ki  keybdInput
	_   [8]byte
}

var (
	user32        = windows.NewLazySystemDLL("user32.dll")
	procSendInput = user32.NewProc("SendInput")
)

func (windowsPaster) Paste(shortcut Shortcut) error {
	virtualKeys := map[key]uint16{keyCtrl: vkControl, keyShift: vkShift, keyV: vkV}
	events := chordEvents(shortcut)
	inputs := make([]input, 0, len(events))
	for _, event := range events {
		var flags uint32
		if !event.down {
			flags = keyeventfKeyup
		}
		inputs = append(inputs, input{typ: inputKeyboard, ki: keybdInput{vk: virtualKeys[event.key], flags: flags}})
	}

	sent, _, err := procSendInput.Call(
		uintptr(len(inputs)),
		uintptr(unsafe.Pointer(&inputs[0])),
		unsafe.Sizeof(inputs[0]),
	)
	if sent != uintptr(len(inputs)) {
		return fmt.Errorf("SendInput sent %d/%d events: %w", sent, len(inputs), err)
	}
	return nil
}
