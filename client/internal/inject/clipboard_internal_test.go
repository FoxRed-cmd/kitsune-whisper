package inject

import (
	"errors"
	"strings"
	"testing"
)

type stubClipboard struct {
	text       string
	canRestore bool
	readErr    error
	writeErr   error
	reads      int
	writes     []string
}

func (c *stubClipboard) Read() (string, error) {
	c.reads++
	if c.readErr != nil {
		return "", c.readErr
	}
	return c.text, nil
}

func (c *stubClipboard) Write(text string) error {
	if c.writeErr != nil {
		return c.writeErr
	}
	c.writes = append(c.writes, text)
	c.text = text
	return nil
}

func (c *stubClipboard) CanRestore() bool { return c.canRestore }

func TestFallbackClipboardPrefersPrimary(t *testing.T) {
	primary := &stubClipboard{text: "primary", canRestore: true}
	fallback := &stubClipboard{text: "fallback"}
	clip := fallbackClipboard{primary: primary, fallback: fallback}

	text, err := clip.Read()
	if err != nil || text != "primary" {
		t.Fatalf("Read = (%q, %v), want (primary, nil)", text, err)
	}
	if fallback.reads != 0 {
		t.Fatal("fallback was read although the primary succeeded")
	}
	if err := clip.Write("hello"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(fallback.writes) != 0 {
		t.Fatal("fallback was written although the primary succeeded")
	}
	if !clip.CanRestore() {
		t.Fatal("CanRestore should be true when the primary can restore")
	}
}

func TestFallbackClipboardUsesFallbackOnError(t *testing.T) {
	primary := &stubClipboard{readErr: errors.New("no native clipboard"), writeErr: errors.New("no native clipboard")}
	fallback := &stubClipboard{text: "from wl-paste", canRestore: true}
	clip := fallbackClipboard{primary: primary, fallback: fallback}

	text, err := clip.Read()
	if err != nil || text != "from wl-paste" {
		t.Fatalf("Read = (%q, %v), want the fallback text", text, err)
	}
	if err := clip.Write("hello"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if len(fallback.writes) != 1 || fallback.writes[0] != "hello" {
		t.Fatalf("fallback writes = %v, want [hello]", fallback.writes)
	}
}

func TestCommandClipboard(t *testing.T) {
	var gotName string
	var gotArgs []string
	var gotStdin []byte
	run := func(name string, args []string, stdin []byte) ([]byte, error) {
		gotName, gotArgs, gotStdin = name, args, stdin
		if name == "wl-paste" {
			return []byte("pasted"), nil
		}
		return nil, nil
	}
	clip := commandClipboard{readCommand: "wl-paste", writeCommand: "wl-copy", run: run}

	text, err := clip.Read()
	if err != nil || text != "pasted" {
		t.Fatalf("Read = (%q, %v), want (pasted, nil)", text, err)
	}
	if gotName != "wl-paste" || !strings.Contains(strings.Join(gotArgs, " "), "--no-newline") {
		t.Fatalf("read invoked %q %v, want wl-paste --no-newline", gotName, gotArgs)
	}

	if err := clip.Write("hello"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if gotName != "wl-copy" || string(gotStdin) != "hello" {
		t.Fatalf("write invoked %q with stdin %q, want wl-copy hello", gotName, gotStdin)
	}
	if !clip.CanRestore() {
		t.Fatal("command clipboard should be able to restore")
	}
}
