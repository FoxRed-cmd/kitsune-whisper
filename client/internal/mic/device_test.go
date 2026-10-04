package mic_test

import (
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/mic"
)

func devices() []mic.Device {
	return []mic.Device{
		{Index: 0, Name: "Built-in Microphone"},
		{Index: 1, Name: "USB Yeti", IsDefault: true},
		{Index: 2, Name: "Webcam Mic"},
	}
}

func TestSelectDeviceEmptyUsesDefault(t *testing.T) {
	got, err := mic.SelectDevice("", devices())
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if got.Index != 1 {
		t.Fatalf("index = %d, want the default (1)", got.Index)
	}
}

func TestSelectDeviceEmptyWithoutFlaggedDefaultUsesSystemDefault(t *testing.T) {
	// miniaudio warns that devices[0] is not necessarily the default, so with
	// no device flagged we must defer to the OS (-1), not pick the first.
	got, err := mic.SelectDevice("", []mic.Device{{Index: 0, Name: "Only One"}})
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if got.Index != -1 {
		t.Fatalf("index = %d, want -1 (system default)", got.Index)
	}
}

func TestSelectDeviceByIndex(t *testing.T) {
	got, err := mic.SelectDevice("2", devices())
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if got.Name != "Webcam Mic" {
		t.Fatalf("name = %q, want Webcam Mic", got.Name)
	}
}

func TestSelectDeviceByIndexOutOfRange(t *testing.T) {
	if _, err := mic.SelectDevice("9", devices()); err == nil {
		t.Fatal("expected an out-of-range error")
	}
}

func TestSelectDeviceByNameCaseInsensitive(t *testing.T) {
	got, err := mic.SelectDevice("usb yeti", devices())
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	if got.Index != 1 {
		t.Fatalf("index = %d, want 1", got.Index)
	}
}

func TestSelectDeviceUnknownNameListsAvailable(t *testing.T) {
	_, err := mic.SelectDevice("Nope", devices())
	if err == nil {
		t.Fatal("expected an unknown-device error")
	}
	if !strings.Contains(err.Error(), "Webcam Mic") {
		t.Fatalf("error %q should list available devices", err)
	}
}

func TestSelectDeviceNoneFound(t *testing.T) {
	if _, err := mic.SelectDevice("", nil); err == nil {
		t.Fatal("expected an error when no devices exist")
	}
}
