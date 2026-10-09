package inject_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/inject"
)

// --- fakes ---

type fakeClipboard struct {
	text       string
	canRestore bool
	readErr    error
	writeErr   error

	reads  int
	writes []string

	primaryText     string
	primaryWrites   []string
	readPrimaryErr  error
	writePrimaryErr error
}

func (c *fakeClipboard) Read() (string, error) {
	c.reads++
	if c.readErr != nil {
		return "", c.readErr
	}
	return c.text, nil
}

func (c *fakeClipboard) Write(text string) error {
	if c.writeErr != nil {
		return c.writeErr
	}
	c.writes = append(c.writes, text)
	c.text = text
	return nil
}

func (c *fakeClipboard) CanRestore() bool { return c.canRestore }

func (c *fakeClipboard) ReadPrimary() (string, error) {
	if c.readPrimaryErr != nil {
		return "", c.readPrimaryErr
	}
	return c.primaryText, nil
}

func (c *fakeClipboard) WritePrimary(text string) error {
	if c.writePrimaryErr != nil {
		return c.writePrimaryErr
	}
	c.primaryWrites = append(c.primaryWrites, text)
	c.primaryText = text
	return nil
}

type fakePaster struct {
	shortcuts []inject.Shortcut
	err       error
}

func (p *fakePaster) Paste(shortcut inject.Shortcut) error {
	p.shortcuts = append(p.shortcuts, shortcut)
	return p.err
}

type fakeFocus struct{ app string }

func (f fakeFocus) ForegroundApp() string { return f.app }

// --- helpers ---

type harness struct {
	clipboard *fakeClipboard
	paster    *fakePaster
	focus     *fakeFocus
	injector  *inject.Injector
}

func newHarness(t *testing.T, opts inject.Options) *harness {
	t.Helper()
	clipboard := &fakeClipboard{canRestore: true}
	paster := &fakePaster{}
	focus := &fakeFocus{}
	opts.Clipboard = clipboard
	opts.Paster = paster
	opts.Focus = focus
	opts.Sleep = func(time.Duration) {}
	if opts.PasteShortcut == "" {
		opts.PasteShortcut = "auto"
	}
	if opts.ClipboardRestore == "" {
		opts.ClipboardRestore = "auto"
	}
	injector, err := inject.New(opts)
	if err != nil {
		t.Fatalf("new injector: %v", err)
	}
	return &harness{clipboard: clipboard, paster: paster, focus: focus, injector: injector}
}

func wantWrites(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(want) == 0 {
		want = nil
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("clipboard writes = %#v, want %#v", got, want)
	}
}

// --- empty guard ---

func TestEmptyTranscriptionLeavesClipboardUntouched(t *testing.T) {
	for _, text := range []string{"", "   ", "\n\t"} {
		h := newHarness(t, inject.Options{Paste: true})
		if err := h.injector.Inject(text); err != nil {
			t.Fatalf("inject %q: %v", text, err)
		}
		wantWrites(t, h.clipboard.writes)
		if len(h.paster.shortcuts) != 0 {
			t.Fatalf("inject %q synthesized a paste", text)
		}
	}
}

// --- clipboard-only ---

func TestPasteDisabledWritesClipboardOnly(t *testing.T) {
	h := newHarness(t, inject.Options{Paste: false})
	if err := h.injector.Inject("hello"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	wantWrites(t, h.clipboard.writes, "hello")
	if len(h.paster.shortcuts) != 0 {
		t.Fatal("paste disabled but a paste was synthesized")
	}
	if h.clipboard.reads != 0 {
		t.Fatal("paste disabled but the clipboard was read for restore")
	}
}

// --- terminal-aware shortcut ---

func TestAutoShortcutDetectsTerminals(t *testing.T) {
	cases := []struct {
		name string
		app  string
		want inject.Shortcut
	}{
		{name: "gui app", app: "firefox", want: inject.CtrlV},
		{name: "code editor", app: "Code", want: inject.CtrlV},
		{name: "file manager", app: "explorer.exe", want: inject.CtrlV},
		{name: "unknown focus", app: "", want: inject.CtrlV},
		{name: "gnome-terminal", app: "Gnome-terminal", want: inject.CtrlShiftV},
		{name: "konsole", app: "konsole", want: inject.CtrlShiftV},
		{name: "xterm", app: "xterm", want: inject.CtrlShiftV},
		{name: "windows terminal process", app: "WindowsTerminal.exe", want: inject.CtrlShiftV},
		{name: "windows terminal class", app: "CASCADIA_HOSTING_WINDOW_CLASS", want: inject.CtrlShiftV},
		{name: "cmd", app: "cmd.exe", want: inject.CtrlShiftV},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, inject.Options{Paste: true})
			h.clipboard.canRestore = false
			h.focus.app = tc.app
			if err := h.injector.Inject("hi"); err != nil {
				t.Fatalf("inject: %v", err)
			}
			if len(h.paster.shortcuts) != 1 || h.paster.shortcuts[0] != tc.want {
				t.Fatalf("pasted %v, want %v", h.paster.shortcuts, tc.want)
			}
		})
	}
}

func TestExplicitShortcutOverridesDetection(t *testing.T) {
	cases := []struct {
		name     string
		shortcut string
		app      string
		want     inject.Shortcut
	}{
		{name: "ctrl_v in terminal", shortcut: "ctrl_v", app: "xterm", want: inject.CtrlV},
		{name: "ctrl_shift_v in gui", shortcut: "ctrl_shift_v", app: "firefox", want: inject.CtrlShiftV},
		{name: "shift_insert in gui", shortcut: "shift_insert", app: "firefox", want: inject.ShiftInsert},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, inject.Options{Paste: true, PasteShortcut: tc.shortcut})
			h.clipboard.canRestore = false
			h.focus.app = tc.app
			if err := h.injector.Inject("hi"); err != nil {
				t.Fatalf("inject: %v", err)
			}
			if len(h.paster.shortcuts) != 1 || h.paster.shortcuts[0] != tc.want {
				t.Fatalf("pasted %v, want %v", h.paster.shortcuts, tc.want)
			}
		})
	}
}

// --- primary selection ---

func TestShiftInsertMirrorsTranscriptionToPrimarySelection(t *testing.T) {
	h := newHarness(t, inject.Options{Paste: true, PasteShortcut: "shift_insert"})
	h.clipboard.canRestore = false
	if err := h.injector.Inject("hi"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	wantWrites(t, h.clipboard.writes, "hi")
	if !reflect.DeepEqual(h.clipboard.primaryWrites, []string{"hi"}) {
		t.Fatalf("primary writes = %#v, want [hi]", h.clipboard.primaryWrites)
	}
}

func TestNonShiftInsertLeavesPrimarySelectionUntouched(t *testing.T) {
	for _, tc := range []struct {
		name     string
		shortcut string
		app      string
	}{
		{name: "ctrl_v", shortcut: "ctrl_v", app: "xterm"},
		{name: "ctrl_shift_v", shortcut: "ctrl_shift_v", app: "xterm"},
		{name: "auto in terminal", shortcut: "auto", app: "xterm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, inject.Options{Paste: true, PasteShortcut: tc.shortcut})
			h.clipboard.canRestore = false
			h.focus.app = tc.app
			if err := h.injector.Inject("hi"); err != nil {
				t.Fatalf("inject: %v", err)
			}
			if len(h.clipboard.primaryWrites) != 0 {
				t.Fatalf("primary writes = %#v, want none", h.clipboard.primaryWrites)
			}
		})
	}
}

func TestShiftInsertRestoresPrimarySelection(t *testing.T) {
	h := newHarness(t, inject.Options{Paste: true, PasteShortcut: "shift_insert"})
	h.clipboard.text = "old-clip"
	h.clipboard.primaryText = "old-primary"
	h.clipboard.canRestore = true
	if err := h.injector.Inject("hi"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	wantWrites(t, h.clipboard.writes, "hi", "old-clip")
	if !reflect.DeepEqual(h.clipboard.primaryWrites, []string{"hi", "old-primary"}) {
		t.Fatalf("primary writes = %#v, want [hi old-primary]", h.clipboard.primaryWrites)
	}
}

func TestPasteDisabledShiftInsertPublishesPrimary(t *testing.T) {
	h := newHarness(t, inject.Options{Paste: false, PasteShortcut: "shift_insert"})
	if err := h.injector.Inject("hi"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	wantWrites(t, h.clipboard.writes, "hi")
	if !reflect.DeepEqual(h.clipboard.primaryWrites, []string{"hi"}) {
		t.Fatalf("primary writes = %#v, want [hi]", h.clipboard.primaryWrites)
	}
}

// --- restore policy ---

func TestRestoreAutoRestoresOnCapableDesktop(t *testing.T) {
	h := newHarness(t, inject.Options{Paste: true})
	h.clipboard.text = "previous"
	h.clipboard.canRestore = true
	if err := h.injector.Inject("transcript"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	wantWrites(t, h.clipboard.writes, "transcript", "previous")
}

func TestRestoreAutoKeepsTranscriptWhereRestoreIsImpossible(t *testing.T) {
	h := newHarness(t, inject.Options{Paste: true})
	h.clipboard.text = "previous"
	h.clipboard.canRestore = false
	if err := h.injector.Inject("transcript"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	wantWrites(t, h.clipboard.writes, "transcript")
	if h.clipboard.reads != 0 {
		t.Fatal("restore-incapable desktop should not read the clipboard")
	}
}

func TestRestoreNeverKeepsTranscript(t *testing.T) {
	h := newHarness(t, inject.Options{Paste: true, ClipboardRestore: "never"})
	h.clipboard.text = "previous"
	h.clipboard.canRestore = true
	if err := h.injector.Inject("transcript"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	wantWrites(t, h.clipboard.writes, "transcript")
}

func TestRestoreAlwaysForcesRestore(t *testing.T) {
	h := newHarness(t, inject.Options{Paste: true, ClipboardRestore: "always"})
	h.clipboard.text = "previous"
	h.clipboard.canRestore = false
	if err := h.injector.Inject("transcript"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	wantWrites(t, h.clipboard.writes, "transcript", "previous")
}

func TestRestoreSkippedWhenClipboardHoldsNothing(t *testing.T) {
	h := newHarness(t, inject.Options{Paste: true})
	h.clipboard.text = ""
	h.clipboard.canRestore = true
	if err := h.injector.Inject("transcript"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	wantWrites(t, h.clipboard.writes, "transcript")
}

func TestRestoreSkippedWhenClipboardUnreadable(t *testing.T) {
	h := newHarness(t, inject.Options{Paste: true})
	h.clipboard.readErr = errors.New("no data")
	h.clipboard.canRestore = true
	if err := h.injector.Inject("transcript"); err != nil {
		t.Fatalf("inject: %v", err)
	}
	wantWrites(t, h.clipboard.writes, "transcript")
}

// --- failure paths ---

func TestPasteFailureLeavesTranscriptOnClipboard(t *testing.T) {
	h := newHarness(t, inject.Options{Paste: true})
	h.clipboard.text = "previous"
	h.clipboard.canRestore = true
	h.paster.err = errors.New("SendInput failed")

	err := h.injector.Inject("transcript")
	if err == nil {
		t.Fatal("expected an error when the paste fails")
	}
	wantWrites(t, h.clipboard.writes, "transcript")
}

func TestClipboardWriteFailureReturnsErrorAndSkipsPaste(t *testing.T) {
	h := newHarness(t, inject.Options{Paste: true})
	h.clipboard.writeErr = errors.New("clipboard busy")
	if err := h.injector.Inject("transcript"); err == nil {
		t.Fatal("expected an error when the clipboard write fails")
	}
	if len(h.paster.shortcuts) != 0 {
		t.Fatal("paste ran despite a failed clipboard write")
	}
}

// --- construction ---

func TestNewRejectsUnknownSettings(t *testing.T) {
	if _, err := inject.New(inject.Options{PasteShortcut: "bogus"}); err == nil {
		t.Fatal("expected an error for an unknown paste_shortcut")
	}
	if _, err := inject.New(inject.Options{ClipboardRestore: "bogus"}); err == nil {
		t.Fatal("expected an error for an unknown clipboard_restore")
	}
}
