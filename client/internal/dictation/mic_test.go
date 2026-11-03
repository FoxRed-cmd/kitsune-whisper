package dictation_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/mic"
)

// fakeRecorder is the level-test capture source. It never opens a device.
type fakeRecorder struct {
	mu       sync.Mutex
	level    float64
	startErr error
	starts   int
	cancels  int
	closes   int
}

func (r *fakeRecorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.starts++
	return r.startErr
}

func (r *fakeRecorder) Cancel() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancels++
	return nil
}

func (r *fakeRecorder) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closes++
	return nil
}

func (r *fakeRecorder) InputLevel() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.level
}

func (r *fakeRecorder) setLevel(level float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.level = level
}

func (r *fakeRecorder) counts() (starts, cancels, closes int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.starts, r.cancels, r.closes
}

// fakeClock drives the level ticker by hand.
type fakeClock struct {
	tick chan time.Time
}

func newFakeClock() *fakeClock { return &fakeClock{tick: make(chan time.Time, 4)} }

func (c *fakeClock) NewTicker(time.Duration) dictation.Ticker { return fakeTicker{c.tick} }

type fakeTicker struct{ tick chan time.Time }

func (t fakeTicker) C() <-chan time.Time { return t.tick }
func (t fakeTicker) Stop()               {}

func testDevices() []mic.Device {
	return []mic.Device{
		{Index: 0, Name: "Built-in Microphone"},
		{Index: 1, Name: "USB Yeti", IsDefault: true},
	}
}

func TestStartLevelTestStreamsLevelsAndStopReleasesTheDevice(t *testing.T) {
	recorder := &fakeRecorder{}
	clock := newFakeClock()
	var opened string
	service := dictation.New(dictation.Options{
		Store: openStore(t, configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n"), nil, nil),
		Env:   map[string]string{"LANG": "en"},
		Devices: func() ([]mic.Device, error) {
			return testDevices(), nil
		},
		Recorders: func(device string) (dictation.Recorder, error) {
			opened = device
			return recorder, nil
		},
		Clock: clock,
	})

	test, err := service.StartLevelTest("1")
	if err != nil {
		t.Fatalf("start level test: %v", err)
	}
	if opened != "1" {
		t.Fatalf("opened device = %q, want the configured selector", opened)
	}
	if starts, _, _ := recorder.counts(); starts != 1 {
		t.Fatalf("recorder starts = %d, want 1", starts)
	}

	recorder.setLevel(0.42)
	clock.tick <- time.Now()
	select {
	case level := <-test.Levels():
		if level != 0.42 {
			t.Fatalf("level = %v, want 0.42", level)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a level")
	}

	if err := test.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if err := test.Stop(); err != nil {
		t.Fatalf("second stop: %v", err)
	}
	if _, cancels, closes := recorder.counts(); cancels != 1 || closes != 1 {
		t.Fatalf("recorder cancels/closes = %d/%d, want 1/1", cancels, closes)
	}
	if _, ok := <-test.Levels(); ok {
		t.Fatal("levels channel must close after Stop")
	}
}

func TestStartLevelTestRejectsAnUnavailableDevice(t *testing.T) {
	service := dictation.New(dictation.Options{
		Store:   openStore(t, configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n"), nil, nil),
		Env:     map[string]string{"LANG": "en"},
		Devices: func() ([]mic.Device, error) { return testDevices(), nil },
		Recorders: func(string) (dictation.Recorder, error) {
			t.Fatal("an unavailable device must not be opened")
			return nil, nil
		},
	})

	_, err := service.StartLevelTest("9")
	if err == nil {
		t.Fatal("expected an error for an unavailable device")
	}
	for _, want := range []string{"“9” is not available", "Built-in Microphone", "USB Yeti"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
}

func TestStartLevelTestReportsAFailedDevice(t *testing.T) {
	recorder := &fakeRecorder{startErr: errDeviceBusy}
	service := dictation.New(dictation.Options{
		Store:     openStore(t, configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n"), nil, nil),
		Env:       map[string]string{"LANG": "en"},
		Devices:   func() ([]mic.Device, error) { return testDevices(), nil },
		Recorders: func(string) (dictation.Recorder, error) { return recorder, nil },
		Clock:     newFakeClock(),
	})

	_, err := service.StartLevelTest("0")
	if err == nil {
		t.Fatal("expected an error when the device cannot start")
	}
	if !strings.Contains(err.Error(), "Could not open microphone “Built-in Microphone”") ||
		!strings.Contains(err.Error(), "device busy") ||
		!strings.Contains(err.Error(), "Choose another device") {
		t.Fatalf("error = %q, want an actionable message", err)
	}
	if _, _, closes := recorder.counts(); closes != 1 {
		t.Fatalf("failed recorder closes = %d, want 1", closes)
	}
}

func TestStartLevelTestWithoutDevices(t *testing.T) {
	service := dictation.New(dictation.Options{
		Store:   openStore(t, configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n"), nil, nil),
		Env:     map[string]string{"LANG": "en"},
		Devices: func() ([]mic.Device, error) { return nil, nil },
	})
	_, err := service.StartLevelTest("")
	if err == nil || !strings.Contains(err.Error(), "No microphone was found") {
		t.Fatalf("error = %v, want a no-microphone message", err)
	}
}

func TestStartLevelTestMessagesAreTranslated(t *testing.T) {
	service := dictation.New(dictation.Options{
		Store:   openStore(t, configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n"), nil, nil),
		Env:     map[string]string{"LANG": "ru_RU.UTF-8"},
		Devices: func() ([]mic.Device, error) { return testDevices(), nil },
	})
	_, err := service.StartLevelTest("9")
	if err == nil || !strings.Contains(err.Error(), "Микрофон «9» недоступен") {
		t.Fatalf("error = %v, want the Russian message", err)
	}
}

func TestStartLevelTestReplacesThePreviousTest(t *testing.T) {
	recorders := []*fakeRecorder{{}, {}}
	next := 0
	service := dictation.New(dictation.Options{
		Store:   openStore(t, configFile(t, "client:\n  hotkey: Ctrl+Alt+D\n"), nil, nil),
		Env:     map[string]string{"LANG": "en"},
		Devices: func() ([]mic.Device, error) { return testDevices(), nil },
		Recorders: func(string) (dictation.Recorder, error) {
			recorder := recorders[next]
			next++
			return recorder, nil
		},
		Clock: newFakeClock(),
	})
	first, err := service.StartLevelTest("")
	if err != nil {
		t.Fatalf("first start: %v", err)
	}
	_ = first
	if _, err := service.StartLevelTest("1"); err != nil {
		t.Fatalf("second start: %v", err)
	}
	if _, _, closes := recorders[0].counts(); closes != 1 {
		t.Fatalf("previous recorder closes = %d, want 1", closes)
	}
	if err := service.StopLevelTest(); err != nil {
		t.Fatalf("stop level test: %v", err)
	}
}

// errDeviceBusy is a stand-in for a real capture failure.
var errDeviceBusy = errorString("device busy")

type errorString string

func (e errorString) Error() string { return string(e) }
