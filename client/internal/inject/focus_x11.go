//go:build linux

package inject

import (
	"bytes"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// x11Focus reads the focused window's WM_CLASS from the X server, for the
// terminal-aware default paste shortcut.
type x11Focus struct{}

func newFocus() Focus { return x11Focus{} }

func (x11Focus) ForegroundApp() string {
	conn, err := xgb.NewConn()
	if err != nil {
		return ""
	}
	defer conn.Close()

	root := xproto.Setup(conn).DefaultScreen(conn).Root
	active, err := atomValue(conn, root, "_NET_ACTIVE_WINDOW")
	if err != nil || active == 0 {
		return ""
	}
	class, err := byteProperty(conn, xproto.Window(active), "WM_CLASS")
	if err != nil {
		return ""
	}
	// WM_CLASS carries "instance\0class", both NUL-terminated; the class is the
	// meaningful half.
	fields := bytes.Split(class, []byte{0})
	if len(fields) >= 2 && len(fields[1]) > 0 {
		return string(fields[1])
	}
	if len(fields) >= 1 {
		return string(fields[0])
	}
	return ""
}

func internAtom(conn *xgb.Conn, name string) (xproto.Atom, error) {
	reply, err := xproto.InternAtom(conn, true, uint16(len(name)), name).Reply()
	if err != nil {
		return 0, err
	}
	return reply.Atom, nil
}

// atomValue reads a single 32-bit property value, such as a window id.
func atomValue(conn *xgb.Conn, window xproto.Window, name string) (uint32, error) {
	atom, err := internAtom(conn, name)
	if err != nil {
		return 0, err
	}
	reply, err := xproto.GetProperty(conn, false, window, atom, xproto.GetPropertyTypeAny, 0, 1).Reply()
	if err != nil || len(reply.Value) < 4 {
		return 0, err
	}
	return xgb.Get32(reply.Value), nil
}

func byteProperty(conn *xgb.Conn, window xproto.Window, name string) ([]byte, error) {
	atom, err := internAtom(conn, name)
	if err != nil {
		return nil, err
	}
	reply, err := xproto.GetProperty(conn, false, window, atom, xproto.AtomAny, 0, 1024).Reply()
	if err != nil {
		return nil, err
	}
	return reply.Value, nil
}
