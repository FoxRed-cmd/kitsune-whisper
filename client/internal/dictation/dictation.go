// Package dictation is the Settings service for the Dictation screens.
//
// It sits above the settings store and the runtime: it renders the effective
// Client settings with their source, lock and apply mode; validates and saves
// changes through the store (the single writer of the "client:" section);
// applies hot changes to the running runtime without a restart; validates
// hotkeys and conflicts; enumerates microphones and streams a live Input level
// from an injected capture source; and exposes the English and Russian
// catalogs.
//
// The Wails Settings window and the Tray call this package. Everything here is
// headless and tested with a real settings store on a temporary file plus
// fake runtime, microphone and clock ports, so no test needs a display, a
// microphone, or a real hotkey.
package dictation

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/hotkey"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/mic"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
)

// Store is the settings store the service reads and writes. *settings.Store
// implements it.
type Store interface {
	Snapshot() settings.Snapshot
	Save(patch map[string]any) (settings.SaveResult, error)
}

// Applier is the running runtime the service asks to rebuild its adapters.
// *runtime.Runtime implements it.
type Applier interface {
	// Apply rebuilds the hot adapters from cfg, or returns an error and keeps
	// the running ones.
	Apply(cfg config.ClientConfig) error
}

// Recorder is a capture source used only for the microphone level test.
// *mic.Recorder implements it.
type Recorder interface {
	Start() error
	Cancel() error
	InputLevel() float64
	Close() error
}

// RecorderFactory opens one capture device for the level test. Production
// wraps mic.New; tests return a fake.
type RecorderFactory func(device string) (Recorder, error)

// DeviceLister enumerates capture devices. Production wraps mic.ListDevices;
// tests return a fixed list.
type DeviceLister func() ([]mic.Device, error)

// SpecParser validates one hotkey spec. Production uses hotkey.ParseSpec; the
// injected form keeps the tests independent of the platform key tables.
type SpecParser func(spec string) error

// Clock supplies the level-test ticker.
type Clock interface {
	NewTicker(d time.Duration) Ticker
}

// Ticker is a stoppable ticker, matching time.Ticker's useful surface.
type Ticker interface {
	C() <-chan time.Time
	Stop()
}

// levelInterval is how often the microphone test samples the Input level.
const levelInterval = 50 * time.Millisecond

// Options wires the service to the store and the running Client.
type Options struct {
	// Store is the settings store. It is required.
	Store Store
	// Runtime receives hot changes. Nil means changes are only saved.
	Runtime Applier
	// Devices enumerates microphones. Nil makes the device list unavailable.
	Devices DeviceLister
	// Recorders opens a capture device for the level test. Nil disables the
	// test with an actionable message.
	Recorders RecorderFactory
	// ParseHotkey validates a hotkey spec. Nil uses hotkey.ParseSpec.
	ParseHotkey SpecParser
	// Clock supplies the level-test ticker. Nil uses the real clock.
	Clock Clock
	// Env is the process environment used for locale detection. Nil reads
	// os.Environ.
	Env map[string]string
	// OnLog receives diagnostics; nil is silent.
	OnLog func(string)
}

// Service is the Dictation settings service.
type Service struct {
	opts   Options
	env    map[string]string
	parser SpecParser
	clock  Clock

	levelMu sync.Mutex
	level   *LevelTest
}

// New builds a Service. It performs no I/O.
func New(opts Options) *Service {
	env := opts.Env
	if env == nil {
		env = environ()
	}
	parser := opts.ParseHotkey
	if parser == nil {
		parser = func(spec string) error {
			_, _, err := hotkey.ParseSpec(spec)
			return err
		}
	}
	clock := opts.Clock
	if clock == nil {
		clock = realClock{}
	}
	return &Service{opts: opts, env: env, parser: parser, clock: clock}
}

func (s *Service) logf(format string, args ...any) {
	if s.opts.OnLog != nil {
		s.opts.OnLog(fmt.Sprintf(format, args...))
	}
}

// Catalog returns the catalog for the current ui_language setting.
func (s *Service) Catalog() i18n.Catalog {
	return s.catalogFor(s.opts.Store.Snapshot())
}

// Config returns the stored Client configuration. The Settings shell uses it
// to hand the current hotkeys to the GNOME custom-keybinding manager.
func (s *Service) Config() config.ClientConfig {
	return s.opts.Store.Snapshot().Config
}

func (s *Service) catalogFor(snapshot settings.Snapshot) i18n.Catalog {
	return i18n.For(i18n.Resolve(snapshot.Config.UILanguage, s.env))
}

// microphones enumerates capture devices, reporting an unavailable audio
// backend to the caller.
func (s *Service) microphones() ([]mic.Device, error) {
	if s.opts.Devices == nil {
		return nil, errors.New("no audio backend is configured")
	}
	return s.opts.Devices()
}

func environ() map[string]string {
	env := map[string]string{}
	for _, kv := range os.Environ() {
		if idx := strings.IndexByte(kv, '='); idx >= 0 {
			env[kv[:idx]] = kv[idx+1:]
		}
	}
	return env
}

type realClock struct{}

func (realClock) NewTicker(d time.Duration) Ticker {
	return realTicker{ticker: time.NewTicker(d)}
}

type realTicker struct{ ticker *time.Ticker }

func (t realTicker) C() <-chan time.Time { return t.ticker.C }
func (t realTicker) Stop()               { t.ticker.Stop() }
