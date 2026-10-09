//go:build linux

package inject

import (
	"strings"
	"testing"
)

func TestCommandClipboardPrimary(t *testing.T) {
	var gotName string
	var gotArgs []string
	var gotStdin []byte
	run := func(name string, args []string, stdin []byte) ([]byte, error) {
		gotName, gotArgs, gotStdin = name, args, stdin
		if name == "wl-paste" {
			return []byte("pasted primary"), nil
		}
		return nil, nil
	}
	clip := commandClipboard{readCommand: "wl-paste", writeCommand: "wl-copy", run: run}

	text, err := clip.ReadPrimary()
	if err != nil || text != "pasted primary" {
		t.Fatalf("ReadPrimary = (%q, %v), want (pasted primary, nil)", text, err)
	}
	if gotName != "wl-paste" || !hasFlag(gotArgs, "--primary") || !hasFlag(gotArgs, "--no-newline") {
		t.Fatalf("read invoked %q %v, want wl-paste --no-newline --primary", gotName, gotArgs)
	}

	if err := clip.WritePrimary("hello"); err != nil {
		t.Fatalf("WritePrimary: %v", err)
	}
	if gotName != "wl-copy" || !hasFlag(gotArgs, "--primary") || string(gotStdin) != "hello" {
		t.Fatalf("write invoked %q %v with stdin %q, want wl-copy --primary hello", gotName, gotArgs, gotStdin)
	}
}

func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if strings.EqualFold(a, flag) {
			return true
		}
	}
	return false
}
