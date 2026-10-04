package inject

// key is a paste-chord component, independent of any desktop backend.
type key int

const (
	keyCtrl key = iota
	keyShift
	keyV
)

// keyEvent is one synthetic keystroke: a chord key going down or up.
type keyEvent struct {
	key  key
	down bool
}

// chordEvents returns the ordered keystrokes for a paste shortcut -- Ctrl
// down, optional Shift down, V down, V up, optional Shift up, Ctrl up -- so the
// Windows and X11 backends only translate key names into native events.
func chordEvents(shortcut Shortcut) []keyEvent {
	events := []keyEvent{{keyCtrl, true}}
	if shortcut == CtrlShiftV {
		events = append(events, keyEvent{keyShift, true})
	}
	events = append(events, keyEvent{keyV, true}, keyEvent{keyV, false})
	if shortcut == CtrlShiftV {
		events = append(events, keyEvent{keyShift, false})
	}
	return append(events, keyEvent{keyCtrl, false})
}
