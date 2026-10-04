// Package hotkey turns global key events into Dictation-cycle triggers.
//
// The package has two halves: a pure Reducer that gates raw key edges into
// cycle.Trigger values (fully tested), and a thin Source adapter over
// golang.design/x/hotkey that produces those edges (untested; it touches the
// real desktop).
package hotkey

import (
	"fmt"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
)

// Mode selects how the main hotkey drives the Dictation cycle.
type Mode int

const (
	// ModeToggle emits Toggle on keydown: press once to start, again to stop.
	ModeToggle Mode = iota
	// ModeHold emits Start on keydown and Stop on keyup (hold-to-talk).
	ModeHold
)

// ParseMode resolves the configured trigger string.
func ParseMode(s string) (Mode, error) {
	switch s {
	case "toggle":
		return ModeToggle, nil
	case "hold":
		return ModeHold, nil
	default:
		return 0, fmt.Errorf("unknown trigger mode %q", s)
	}
}

// Edge is a physical key state change.
type Edge int

const (
	// Down is a keydown.
	Down Edge = iota
	// Up is a keyup.
	Up
)

// Slot identifies which configured hotkey produced an edge.
type Slot int

const (
	// Main is the toggle / hold-to-talk hotkey.
	Main Slot = iota
	// Cancel is the cancel hotkey (Esc by default).
	Cancel
)

// Reducer converts raw key edges into Dictation triggers.
//
// It gates every hotkey on a keyup: the first keydown emits, a keydown while
// the key is already held is dropped, and the next keydown emits only after the
// key is released. A keyup with no preceding keydown is ignored, which absorbs
// the repeated keyups X11 delivers while a key auto-repeats. In hold mode the
// main hotkey additionally emits Stop on keyup. The main and cancel hotkeys
// keep independent held state.
type Reducer struct {
	mode       Mode
	mainDown   bool
	cancelDown bool
}

// NewReducer returns a Reducer for the given trigger mode.
func NewReducer(mode Mode) *Reducer {
	return &Reducer{mode: mode}
}

// Feed consumes one key edge and reports the trigger to emit, if any.
func (r *Reducer) Feed(slot Slot, edge Edge) (cycle.Trigger, bool) {
	if slot == Cancel {
		switch edge {
		case Down:
			if r.cancelDown {
				return 0, false
			}
			r.cancelDown = true
			return cycle.Cancel, true
		case Up:
			r.cancelDown = false
		}
		return 0, false
	}

	switch edge {
	case Down:
		if r.mainDown {
			return 0, false
		}
		r.mainDown = true
		if r.mode == ModeHold {
			return cycle.Start, true
		}
		return cycle.Toggle, true
	case Up:
		if !r.mainDown {
			return 0, false
		}
		r.mainDown = false
		if r.mode == ModeHold {
			return cycle.Stop, true
		}
	}
	return 0, false
}
