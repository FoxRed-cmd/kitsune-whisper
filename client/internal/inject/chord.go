package inject

// key is a paste-chord component, independent of any desktop backend.
type key int

const (
	keyCtrl key = iota
	keyShift
	keyV
	keyInsert
)

// keyEvent is one synthetic keystroke: a chord key going down or up.
type keyEvent struct {
	key  key
	down bool
}

// chordEvents returns the ordered keystrokes for a paste shortcut -- Ctrl
// down, optional Shift down, V/Insert down, V/Insert up, optional Shift up,
// Ctrl up (there is no Ctrl for Shift+Insert) -- so the Windows and X11
// backends only translate key names into native events.
func chordEvents(shortcut Shortcut) []keyEvent {
	if shortcut == ShiftInsert {
		return []keyEvent{
			{keyShift, true},
			{keyInsert, true}, {keyInsert, false},
			{keyShift, false},
		}
	}
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
