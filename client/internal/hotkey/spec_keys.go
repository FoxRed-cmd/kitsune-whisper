//go:build cgo || windows

package hotkey

import (
	"strconv"

	xhotkey "golang.design/x/hotkey"
)

// keyNames maps config key aliases to the platform's constants. The constant
// names are identical across platforms, but their values differ, so they are
// referenced directly rather than derived from ASCII.
var keyNames = map[string]xhotkey.Key{
	"space":  xhotkey.KeySpace,
	"escape": xhotkey.KeyEscape,
	"esc":    xhotkey.KeyEscape,
	"return": xhotkey.KeyReturn,
	"enter":  xhotkey.KeyReturn,
	"tab":    xhotkey.KeyTab,
	"delete": xhotkey.KeyDelete,
	"left":   xhotkey.KeyLeft,
	"right":  xhotkey.KeyRight,
	"up":     xhotkey.KeyUp,
	"down":   xhotkey.KeyDown,

	"volumeup":   xhotkey.KeyVolumeUp,
	"volumedown": xhotkey.KeyVolumeDown,
	"volumemute": xhotkey.KeyVolumeMute,

	"mediaplaypause": xhotkey.KeyMediaPlayPause,
	"medianext":      xhotkey.KeyMediaNext,
	"mediaprev":      xhotkey.KeyMediaPrev,
	"mediastop":      xhotkey.KeyMediaStop,
}

// letterKeys, digitKeys, and functionKeys are listed explicitly: macOS Carbon
// keycodes are not ASCII-derived, so arithmetic on the constants is unsafe.
var (
	letterKeys = []xhotkey.Key{
		xhotkey.KeyA, xhotkey.KeyB, xhotkey.KeyC, xhotkey.KeyD, xhotkey.KeyE,
		xhotkey.KeyF, xhotkey.KeyG, xhotkey.KeyH, xhotkey.KeyI, xhotkey.KeyJ,
		xhotkey.KeyK, xhotkey.KeyL, xhotkey.KeyM, xhotkey.KeyN, xhotkey.KeyO,
		xhotkey.KeyP, xhotkey.KeyQ, xhotkey.KeyR, xhotkey.KeyS, xhotkey.KeyT,
		xhotkey.KeyU, xhotkey.KeyV, xhotkey.KeyW, xhotkey.KeyX, xhotkey.KeyY,
		xhotkey.KeyZ,
	}
	digitKeys = []xhotkey.Key{
		xhotkey.Key0, xhotkey.Key1, xhotkey.Key2, xhotkey.Key3, xhotkey.Key4,
		xhotkey.Key5, xhotkey.Key6, xhotkey.Key7, xhotkey.Key8, xhotkey.Key9,
	}
	functionKeys = []xhotkey.Key{
		xhotkey.KeyF1, xhotkey.KeyF2, xhotkey.KeyF3, xhotkey.KeyF4, xhotkey.KeyF5,
		xhotkey.KeyF6, xhotkey.KeyF7, xhotkey.KeyF8, xhotkey.KeyF9, xhotkey.KeyF10,
		xhotkey.KeyF11, xhotkey.KeyF12, xhotkey.KeyF13, xhotkey.KeyF14, xhotkey.KeyF15,
		xhotkey.KeyF16, xhotkey.KeyF17, xhotkey.KeyF18, xhotkey.KeyF19, xhotkey.KeyF20,
	}
)

func init() {
	for i := range letterKeys {
		keyNames[string(rune('a'+i))] = letterKeys[i]
	}
	for i, key := range digitKeys {
		keyNames[strconv.Itoa(i)] = key
	}
	for i, key := range functionKeys {
		keyNames["f"+strconv.Itoa(i+1)] = key
	}
}
