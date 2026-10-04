package hotkey_test

import (
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/hotkey"
)

func expect(t *testing.T, got cycle.Trigger, ok bool, want cycle.Trigger, wantOK bool) {
	t.Helper()
	if ok != wantOK || got != want {
		t.Fatalf("Feed = (%v, %v), want (%v, %v)", got, ok, want, wantOK)
	}
}

func TestToggleEmitsOncePerPress(t *testing.T) {
	r := hotkey.NewReducer(hotkey.ModeToggle)

	got, ok := r.Feed(hotkey.Main, hotkey.Down)
	expect(t, got, ok, cycle.Toggle, true)

	// Auto-repeat while the key is held must not bounce the toggle.
	got, ok = r.Feed(hotkey.Main, hotkey.Down)
	expect(t, got, ok, 0, false)

	got, ok = r.Feed(hotkey.Main, hotkey.Up)
	expect(t, got, ok, 0, false)

	got, ok = r.Feed(hotkey.Main, hotkey.Down)
	expect(t, got, ok, cycle.Toggle, true)
}

func TestToggleAbsorbsRepeatedKeyups(t *testing.T) {
	r := hotkey.NewReducer(hotkey.ModeToggle)

	if _, ok := r.Feed(hotkey.Main, hotkey.Down); !ok {
		t.Fatal("first keydown should emit")
	}
	// The hotkey library documents repeated keyups while a key auto-repeats on
	// X11; they must not re-arm the toggle. Only a keydown after a settled
	// release opens a new cycle.
	for i := 0; i < 3; i++ {
		if _, ok := r.Feed(hotkey.Main, hotkey.Up); ok {
			t.Fatal("keyup should not emit")
		}
	}
	got, ok := r.Feed(hotkey.Main, hotkey.Down)
	expect(t, got, ok, cycle.Toggle, true)
}

func TestHoldStartsOnDownStopsOnUp(t *testing.T) {
	r := hotkey.NewReducer(hotkey.ModeHold)

	got, ok := r.Feed(hotkey.Main, hotkey.Down)
	expect(t, got, ok, cycle.Start, true)

	got, ok = r.Feed(hotkey.Main, hotkey.Down)
	expect(t, got, ok, 0, false)

	got, ok = r.Feed(hotkey.Main, hotkey.Up)
	expect(t, got, ok, cycle.Stop, true)

	// A spurious keyup (X11 auto-repeat) must not emit a second Stop.
	got, ok = r.Feed(hotkey.Main, hotkey.Up)
	expect(t, got, ok, 0, false)

	got, ok = r.Feed(hotkey.Main, hotkey.Down)
	expect(t, got, ok, cycle.Start, true)
}

func TestCancelEmitsOncePerPress(t *testing.T) {
	r := hotkey.NewReducer(hotkey.ModeToggle)

	got, ok := r.Feed(hotkey.Cancel, hotkey.Down)
	expect(t, got, ok, cycle.Cancel, true)

	got, ok = r.Feed(hotkey.Cancel, hotkey.Down)
	expect(t, got, ok, 0, false)

	got, ok = r.Feed(hotkey.Cancel, hotkey.Up)
	expect(t, got, ok, 0, false)

	got, ok = r.Feed(hotkey.Cancel, hotkey.Down)
	expect(t, got, ok, cycle.Cancel, true)
}

func TestMainAndCancelAreIndependent(t *testing.T) {
	r := hotkey.NewReducer(hotkey.ModeToggle)

	if _, ok := r.Feed(hotkey.Main, hotkey.Down); !ok {
		t.Fatal("main keydown should emit")
	}
	// Cancel must fire while the main key is still held.
	got, ok := r.Feed(hotkey.Cancel, hotkey.Down)
	expect(t, got, ok, cycle.Cancel, true)

	if _, ok := r.Feed(hotkey.Main, hotkey.Up); ok {
		t.Fatal("main keyup in toggle mode should not emit")
	}
	if _, ok := r.Feed(hotkey.Cancel, hotkey.Up); ok {
		t.Fatal("cancel keyup should not emit")
	}
}

func TestParseMode(t *testing.T) {
	cases := []struct {
		in      string
		want    hotkey.Mode
		wantErr bool
	}{
		{in: "toggle", want: hotkey.ModeToggle},
		{in: "hold", want: hotkey.ModeHold},
		{in: "Toggle", wantErr: true},
		{in: "", wantErr: true},
		{in: "push", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := hotkey.ParseMode(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseMode(%q) = %v, want error", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseMode(%q) error: %v", tc.in, err)
			}
			if got != tc.want {
				t.Fatalf("ParseMode(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}
