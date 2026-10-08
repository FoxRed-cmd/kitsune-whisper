//go:build linux

package inject

import (
	"fmt"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
)

// X keysyms for the chord components.
const (
	keysymControlL = 0xffe3
	keysymShiftL   = 0xffe1
	keysymV        = 0x0076
	keysymInsert   = 0xff63
)

// x11Paster synthesizes the paste keystroke through the XTEST extension. Under
// a Wayland session it connects through XWayland; a compositor without XWayland
// yields an error, and the transcript stays on the clipboard.
type x11Paster struct{}

func (x11Paster) Paste(shortcut Shortcut) error {
	conn, err := xgb.NewConn()
	if err != nil {
		return fmt.Errorf("connect to the X server: %w", err)
	}
	defer conn.Close()

	if err := xtest.Init(conn); err != nil {
		return fmt.Errorf("init XTEST: %w", err)
	}

	keycodes := map[key]xproto.Keycode{}
	for component, keysym := range map[key]xproto.Keysym{
		keyCtrl:   keysymControlL,
		keyShift:  keysymShiftL,
		keyV:      keysymV,
		keyInsert: keysymInsert,
	} {
		code, err := keysymToKeycode(conn, keysym)
		if err != nil {
			return err
		}
		keycodes[component] = code
	}

	for _, event := range chordEvents(shortcut) {
		typ := byte(xproto.KeyPress)
		if !event.down {
			typ = xproto.KeyRelease
		}
		if err := xtest.FakeInputChecked(conn, typ, byte(keycodes[event.key]), 0, 0, 0, 0, 0).Check(); err != nil {
			return fmt.Errorf("synthesize key event: %w", err)
		}
	}
	conn.Sync()
	return nil
}

// keysymToKeycode maps a keysym to the current keyboard's keycode by walking
// the keyboard mapping, so remapped layouts still paste.
func keysymToKeycode(conn *xgb.Conn, keysym xproto.Keysym) (xproto.Keycode, error) {
	setup := xproto.Setup(conn)
	minKeycode := int(setup.MinKeycode)
	maxKeycode := int(setup.MaxKeycode)
	if maxKeycode < minKeycode {
		return 0, fmt.Errorf("invalid keyboard range %d..%d", minKeycode, maxKeycode)
	}
	count := maxKeycode - minKeycode + 1
	if count > 255 {
		count = 255
	}
	reply, err := xproto.GetKeyboardMapping(conn, setup.MinKeycode, byte(count)).Reply()
	if err != nil {
		return 0, fmt.Errorf("get keyboard mapping: %w", err)
	}
	per := int(reply.KeysymsPerKeycode)
	if per == 0 {
		return 0, fmt.Errorf("empty keyboard mapping")
	}
	for i := 0; i < count; i++ {
		for col := 0; col < per; col++ {
			idx := i*per + col
			if idx >= len(reply.Keysyms) {
				break
			}
			if reply.Keysyms[idx] == keysym {
				return xproto.Keycode(minKeycode + i), nil
			}
		}
	}
	return 0, fmt.Errorf("keysym 0x%x not found on the keyboard", keysym)
}
