//go:build cgo || windows

package hotkey_test

import (
	"reflect"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/hotkey"

	xhotkey "golang.design/x/hotkey"
)

func assertParse(t *testing.T, spec string, mods []xhotkey.Modifier, key xhotkey.Key) {
	t.Helper()
	gotMods, gotKey, err := hotkey.ParseSpec(spec)
	if err != nil {
		t.Fatalf("ParseSpec(%q) error: %v", spec, err)
	}
	if !reflect.DeepEqual(gotMods, mods) {
		t.Fatalf("ParseSpec(%q) mods = %v, want %v", spec, gotMods, mods)
	}
	if gotKey != key {
		t.Fatalf("ParseSpec(%q) key = %v, want %v", spec, gotKey, key)
	}
}

func TestParseSpec(t *testing.T) {
	cases := []struct {
		name    string
		spec    string
		mods    []xhotkey.Modifier
		key     xhotkey.Key
		wantErr bool
	}{
		{name: "default toggle", spec: "Ctrl+Shift+Space", mods: []xhotkey.Modifier{xhotkey.ModCtrl, xhotkey.ModShift}, key: xhotkey.KeySpace},
		{name: "cancel", spec: "Esc", key: xhotkey.KeyEscape},
		{name: "escape alias", spec: "Escape", key: xhotkey.KeyEscape},
		{name: "case insensitive", spec: "ctrl+shift+space", mods: []xhotkey.Modifier{xhotkey.ModCtrl, xhotkey.ModShift}, key: xhotkey.KeySpace},
		{name: "function key", spec: "Ctrl+F5", mods: []xhotkey.Modifier{xhotkey.ModCtrl}, key: xhotkey.KeyF5},
		{name: "digit", spec: "Ctrl+1", mods: []xhotkey.Modifier{xhotkey.ModCtrl}, key: xhotkey.Key1},
		{name: "return alias", spec: "Enter", key: xhotkey.KeyReturn},
		{name: "tab", spec: "Tab", key: xhotkey.KeyTab},
		{name: "bare key", spec: "A", key: xhotkey.KeyA},
		{name: "empty", spec: "", wantErr: true},
		{name: "modifier only", spec: "Ctrl+Shift", wantErr: true},
		{name: "unknown key", spec: "Ctrl+Banana", wantErr: true},
		{name: "unknown modifier", spec: "Hyper+A", wantErr: true},
		{name: "two keys", spec: "A+B", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mods, key, err := hotkey.ParseSpec(tc.spec)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseSpec(%q) = (%v, %v), want error", tc.spec, mods, key)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseSpec(%q) error: %v", tc.spec, err)
			}
			if !reflect.DeepEqual(mods, tc.mods) {
				t.Fatalf("ParseSpec(%q) mods = %v, want %v", tc.spec, mods, tc.mods)
			}
			if key != tc.key {
				t.Fatalf("ParseSpec(%q) key = %v, want %v", tc.spec, key, tc.key)
			}
		})
	}
}
