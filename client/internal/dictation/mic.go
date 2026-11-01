package dictation

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/mic"
)

// LevelStream is a running microphone level test as the Settings window sees
// it: a stream of Input levels and a stop that releases the device.
type LevelStream interface {
	Levels() <-chan float64
	Stop() error
}

// LevelTest is a running microphone level test. Levels streams the latest
// Input level in [0,1]; Stop releases the capture device.
type LevelTest struct {
	levels   chan float64
	done     chan struct{}
	finished chan struct{}
	once     sync.Once
}

// Levels returns the live Input level stream. The channel closes when the test
// stops or the device fails.
func (t *LevelTest) Levels() <-chan float64 { return t.levels }

// Stop releases the capture device and waits for the test goroutine to finish.
// It is idempotent.
func (t *LevelTest) Stop() error {
	t.once.Do(func() { close(t.done) })
	<-t.finished
	return nil
}

// StartLevelTest opens the selected microphone and starts streaming its Input
// level. Starting a second test stops the first. An unavailable, unplugged, or
// busy device returns one translated, actionable message and leaves nothing
// open.
func (s *Service) StartLevelTest(device string) (LevelStream, error) {
	catalog := s.Catalog()
	_ = s.stopLevelTest()

	devices, err := s.microphones()
	if err != nil {
		return nil, errors.New(catalog.T("error.mic.list", err.Error()))
	}
	chosen, err := mic.SelectDevice(device, devices)
	if err != nil {
		if len(devices) == 0 || errors.Is(err, mic.ErrNoDevices) {
			return nil, errors.New(catalog.T("error.mic.none"))
		}
		return nil, fmt.Errorf(catalog.T("error.mic.unknown"), deviceLabel(catalog, device, chosen), deviceNames(devices))
	}
	label := deviceLabel(catalog, device, chosen)
	if s.opts.Recorders == nil {
		return nil, fmt.Errorf(catalog.T("error.mic.start"), label, "no audio backend is configured")
	}
	recorder, err := s.opts.Recorders(device)
	if err != nil {
		return nil, fmt.Errorf(catalog.T("error.mic.start"), label, err.Error())
	}
	if err := recorder.Start(); err != nil {
		_ = recorder.Close()
		return nil, fmt.Errorf(catalog.T("error.mic.start"), label, err.Error())
	}

	test := &LevelTest{
		levels:   make(chan float64, 1),
		done:     make(chan struct{}),
		finished: make(chan struct{}),
	}
	s.levelMu.Lock()
	s.level = test
	s.levelMu.Unlock()

	go s.streamLevels(test, recorder)
	return test, nil
}

// StopLevelTest stops a running level test, releasing its microphone. It is a
// no-op when no test is running.
func (s *Service) StopLevelTest() error {
	return s.stopLevelTest()
}

func (s *Service) stopLevelTest() error {
	s.levelMu.Lock()
	test := s.level
	s.level = nil
	s.levelMu.Unlock()
	if test == nil {
		return nil
	}
	return test.Stop()
}

// streamLevels samples the recorder on every tick until the test stops, then
// releases the device and closes the stream.
func (s *Service) streamLevels(test *LevelTest, recorder Recorder) {
	ticker := s.clock.NewTicker(levelInterval)
	defer ticker.Stop()
	for {
		select {
		case <-test.done:
			_ = recorder.Cancel()
			_ = recorder.Close()
			close(test.levels)
			close(test.finished)
			return
		case <-ticker.C():
			level := recorder.InputLevel()
			// Keep only the latest level: a slow consumer must not back up the
			// audio thread or make the meter lag.
			select {
			case test.levels <- level:
			default:
			}
		}
	}
}

// deviceLabel names the device a test is using in a message.
func deviceLabel(catalog i18n.Catalog, selector string, chosen mic.Device) string {
	if strings.TrimSpace(selector) == "" {
		if chosen.Name != "" {
			return chosen.Name
		}
		return catalog.T("mic.default")
	}
	if chosen.Name != "" {
		return chosen.Name
	}
	return selector
}
