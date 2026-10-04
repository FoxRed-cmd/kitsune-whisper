// Package mic is the microphone-source adapter: it captures the configured
// input device at its native rate and hands the Dictation cycle canonical
// 16 kHz mono PCM WAV utterances.
//
// The Recorder implements cycle.Recorder. Device access goes through the
// Backend seam, so the capture/resample logic is tested with a fake device and
// only the malgo binding (malgo.go) touches real hardware.
package mic

import (
	"fmt"
	"strconv"
	"strings"
)

// Device is one selectable capture device as enumeration returns it.
type Device struct {
	Index     int
	Name      string
	IsDefault bool
}

// systemDefault marks the "let the OS choose" selector. Its Index is -1, which
// the malgo backend opens without a device ID.
func systemDefault() Device {
	return Device{Index: -1, Name: "system default", IsDefault: true}
}

// SelectDevice resolves the configured selector against enumerated devices:
// an empty selector picks the system default, a decimal picks by index, and
// anything else is matched against the device name case-insensitively.
//
// When the OS flags one device as the default we return it; otherwise we return
// the systemDefault sentinel rather than guessing devices[0], which miniaudio
// explicitly warns is not necessarily the default.
func SelectDevice(selector string, devices []Device) (Device, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		if len(devices) == 0 {
			return Device{}, fmt.Errorf("no capture devices found")
		}
		for _, device := range devices {
			if device.IsDefault {
				return device, nil
			}
		}
		return systemDefault(), nil
	}
	if index, err := strconv.Atoi(selector); err == nil {
		if index < 0 || index >= len(devices) {
			return Device{}, fmt.Errorf("audio device index %d out of range (have %d devices)", index, len(devices))
		}
		return devices[index], nil
	}
	for _, device := range devices {
		if strings.EqualFold(device.Name, selector) {
			return device, nil
		}
	}
	return Device{}, fmt.Errorf("audio device %q not found; available: %s", selector, deviceNames(devices))
}

func deviceNames(devices []Device) string {
	names := make([]string, len(devices))
	for i, device := range devices {
		names[i] = device.Name
	}
	return strings.Join(names, ", ")
}
