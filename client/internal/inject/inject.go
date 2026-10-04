// Package inject places a Transcription into the focused field via the
// clipboard plus a synthetic paste, restoring the previous clipboard where the
// desktop allows.
//
// The orchestration -- the empty guard, the terminal-aware paste shortcut, and
// the restore policy -- is pure and driven here; the desktop adapters (the
// system clipboard, the synthetic paste keystroke, and the focused-app probe)
// are thin, platform-specific, and supplied through Options.
package inject

import (
	"fmt"
	"strings"
	"time"
)

// Shortcut is a synthetic-paste chord.
type Shortcut int

const (
	// CtrlV is the paste chord for GUI applications.
	CtrlV Shortcut = iota
	// CtrlShiftV is the paste chord for terminals.
	CtrlShiftV
)

// Paster synthesizes a paste keystroke.
type Paster interface {
	Paste(shortcut Shortcut) error
}

type pasteMode int

const (
	pasteAuto pasteMode = iota
	pasteCtrlV
	pasteCtrlShiftV
)

func parsePasteShortcut(value string) (pasteMode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return pasteAuto, nil
	case "ctrl_v":
		return pasteCtrlV, nil
	case "ctrl_shift_v":
		return pasteCtrlShiftV, nil
	default:
		return 0, fmt.Errorf("paste_shortcut: unknown value %q", value)
	}
}

type restorePolicy int

const (
	restoreAuto restorePolicy = iota
	restoreAlways
	restoreNever
)

func parseClipboardRestore(value string) (restorePolicy, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "auto":
		return restoreAuto, nil
	case "always":
		return restoreAlways, nil
	case "never":
		return restoreNever, nil
	default:
		return 0, fmt.Errorf("clipboard_restore: unknown value %q", value)
	}
}

// Clipboard is the system clipboard port.
type Clipboard interface {
	// Read returns the current clipboard text, or an error when it holds no
	// readable text.
	Read() (string, error)
	// Write replaces the clipboard contents with text.
	Write(text string) error
	// CanRestore reports whether the previous clipboard can be safely restored
	// after a synthetic paste on this desktop.
	CanRestore() bool
}

// Focus reports the focused application, for the terminal-aware default.
type Focus interface {
	// ForegroundApp returns a class or process name for the focused window, or
	// "" when it cannot be determined.
	ForegroundApp() string
}

// Options configures an Injector. The Paste, PasteShortcut, and
// ClipboardRestore values come straight from the Client config; the ports are
// overridden in tests and default to the platform implementations.
type Options struct {
	// Paste synthesizes a paste after writing the clipboard; false is
	// clipboard-only.
	Paste bool
	// PasteShortcut is "auto", "ctrl_v", or "ctrl_shift_v"; "auto" selects
	// ctrl_shift_v in terminals and ctrl_v elsewhere.
	PasteShortcut string
	// ClipboardRestore is "auto", "always", or "never".
	ClipboardRestore string

	// Clipboard, Paster, and Focus override the platform ports; nil uses the
	// real implementation.
	Clipboard Clipboard
	Paster    Paster
	Focus     Focus

	// OnLog receives human-readable progress; nil is silent.
	OnLog func(string)
	// Sleep performs the pause between writing the clipboard and the paste (and
	// again before restoring), so the target consumes the transcript first; nil
	// uses time.Sleep and tests inject a no-op.
	Sleep func(time.Duration)
}

// defaultDelay leaves the target application time to consume the paste before
// the clipboard is reused or restored.
const defaultDelay = 80 * time.Millisecond

// Injector implements cycle.Injector.
type Injector struct {
	paste         bool
	pasteShortcut pasteMode
	restore       restorePolicy

	clipboard Clipboard
	paster    Paster
	focus     Focus

	log   func(string)
	sleep func(time.Duration)
}

// New builds an Injector, defaulting the ports to the platform implementations.
func New(opts Options) (*Injector, error) {
	mode, err := parsePasteShortcut(opts.PasteShortcut)
	if err != nil {
		return nil, err
	}
	restore, err := parseClipboardRestore(opts.ClipboardRestore)
	if err != nil {
		return nil, err
	}

	clip := opts.Clipboard
	if clip == nil {
		clip, err = newClipboard()
		if err != nil {
			return nil, err
		}
	}
	paste := opts.Paster
	if paste == nil {
		paste = newPaster()
	}
	focus := opts.Focus
	if focus == nil {
		focus = newFocus()
	}
	sleep := opts.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	log := opts.OnLog
	if log == nil {
		log = func(string) {}
	}
	return &Injector{
		paste:         opts.Paste,
		pasteShortcut: mode,
		restore:       restore,
		clipboard:     clip,
		paster:        paste,
		focus:         focus,
		log:           log,
		sleep:         sleep,
	}, nil
}

// Inject places text into the focused field. An empty or whitespace-only
// transcription is a no-op that leaves the clipboard untouched. With paste
// disabled the transcript is left on the clipboard for the user to paste
// manually; otherwise the clipboard is written, the paste is synthesized, and
// the previous clipboard is restored where the policy and desktop allow. A
// failed paste leaves the transcript on the clipboard so it is not lost.
func (i *Injector) Inject(text string) error {
	if strings.TrimSpace(text) == "" {
		i.log("empty transcription: clipboard untouched")
		return nil
	}

	if !i.paste {
		if err := i.clipboard.Write(text); err != nil {
			return fmt.Errorf("write clipboard: %w", err)
		}
		i.log("paste disabled: transcription is on the clipboard")
		return nil
	}

	shortcut := i.shortcut()
	restorePrevious := i.shouldRestore()
	var previous string
	if restorePrevious {
		prev, err := i.clipboard.Read()
		switch {
		case err != nil:
			i.log(fmt.Sprintf("clipboard restore skipped: %v", err))
			restorePrevious = false
		case prev == "":
			restorePrevious = false
		default:
			previous = prev
		}
	}

	if err := i.clipboard.Write(text); err != nil {
		return fmt.Errorf("write clipboard: %w", err)
	}
	i.sleep(defaultDelay)
	if err := i.paster.Paste(shortcut); err != nil {
		return fmt.Errorf("synthesize paste: %w", err)
	}

	if restorePrevious {
		i.sleep(defaultDelay)
		if err := i.clipboard.Write(previous); err != nil {
			i.log(fmt.Sprintf("clipboard restore failed: %v", err))
		} else {
			i.log("pasted; previous clipboard restored")
		}
		return nil
	}
	if i.clipboard.CanRestore() {
		i.log("pasted")
	} else {
		i.log("pasted; clipboard now holds the transcription (restore is impossible on this platform)")
	}
	return nil
}

// shortcut resolves the paste chord: an explicit setting wins, "auto" picks the
// terminal chord in terminals.
func (i *Injector) shortcut() Shortcut {
	switch i.pasteShortcut {
	case pasteCtrlV:
		return CtrlV
	case pasteCtrlShiftV:
		return CtrlShiftV
	default:
		app := i.focus.ForegroundApp()
		if isTerminal(app) {
			i.log(fmt.Sprintf("paste shortcut ctrl_shift_v (terminal %q)", app))
			return CtrlShiftV
		}
		i.log(fmt.Sprintf("paste shortcut ctrl_v (focused app %q)", app))
		return CtrlV
	}
}

// shouldRestore resolves the restore policy. "auto" restores only where the
// desktop can; "always" attempts a restore even where it is unreliable (such as
// Windows, where the OS never reports when a synthetic paste is consumed).
func (i *Injector) shouldRestore() bool {
	switch i.restore {
	case restoreNever:
		return false
	case restoreAlways:
		return true
	default:
		return i.clipboard.CanRestore()
	}
}
