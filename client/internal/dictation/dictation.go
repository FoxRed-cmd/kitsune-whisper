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
	"sort"
	"strconv"
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

// LevelInterval is how often the microphone test samples the Input level.
const LevelInterval = 50 * time.Millisecond

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

func (s *Service) catalogFor(snapshot settings.Snapshot) i18n.Catalog {
	return i18n.For(i18n.Resolve(snapshot.Config.UILanguage, s.env))
}

// Kind is the control a Settings field renders as.
type Kind string

const (
	// KindText is a free-form text field.
	KindText Kind = "text"
	// KindSelect is a fixed list of choices.
	KindSelect Kind = "select"
	// KindToggle is a checkbox.
	KindToggle Kind = "toggle"
	// KindHotkey is a hotkey recorder.
	KindHotkey Kind = "hotkey"
	// KindMicrophone is a device selector with a level test.
	KindMicrophone Kind = "microphone"
)

// Option is one selectable value.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field is one editable setting with everything the window needs to render it
// without knowing the configuration schema.
type Field struct {
	Path        string             `json:"path"`
	Label       string             `json:"label"`
	Help        string             `json:"help,omitempty"`
	Kind        Kind               `json:"kind"`
	Value       string             `json:"value"`
	Options     []Option           `json:"options,omitempty"`
	Locked      bool               `json:"locked"`
	Source      string             `json:"source"`
	SourceLabel string             `json:"sourceLabel"`
	LockReason  string             `json:"lockReason,omitempty"`
	Mode        settings.ApplyMode `json:"mode"`
	Badge       string             `json:"badge,omitempty"`
}

// Section groups the fields of one Settings screen.
type Section struct {
	ID     string  `json:"id"`
	Title  string  `json:"title"`
	Fields []Field `json:"fields"`
}

// Actions are the translated buttons and hints the Settings page renders.
type Actions struct {
	Save              string `json:"save"`
	Restart           string `json:"restart"`
	Test              string `json:"test"`
	Stop              string `json:"stop"`
	Level             string `json:"level"`
	RestartHint       string `json:"restartHint"`
	HotkeyPlaceholder string `json:"hotkeyPlaceholder"`
}

// View is the rendered Settings state. Labels are already translated, so the
// window and the page never look up a message themselves.
type View struct {
	Language    i18n.Lang `json:"language"`
	Title       string    `json:"title"`
	Subtitle    string    `json:"subtitle"`
	Actions     Actions   `json:"actions"`
	Sections    []Section `json:"sections"`
	ConfigPath  string    `json:"configPath"`
	DeviceError string    `json:"deviceError,omitempty"`
}

// Component is the field/section description before values are filled in.
type fieldSpec struct {
	path     string
	kind     Kind
	labelKey string
	helpKey  string
	options  []optionSpec
}

type optionSpec struct {
	value string
	key   string
}

type sectionSpec struct {
	id       string
	titleKey string
	fields   []fieldSpec
}

// specs is the Dictation screen layout. It is the same for both languages;
// only the catalog changes.
var specs = []sectionSpec{
	{
		id:       "general",
		titleKey: "section.general",
		fields: []fieldSpec{
			{
				path:     "client.ui_language",
				kind:     KindSelect,
				labelKey: "field.ui_language",
				helpKey:  "help.ui_language",
				options: []optionSpec{
					{value: "auto", key: "language.system"},
					{value: "en", key: "language.en"},
					{value: "ru", key: "language.ru"},
				},
			},
		},
	},
	{
		id:       "dictation",
		titleKey: "section.dictation",
		fields: []fieldSpec{
			{path: "client.hotkey", kind: KindHotkey, labelKey: "field.hotkey", helpKey: "help.hotkey"},
			{path: "client.cancel_hotkey", kind: KindHotkey, labelKey: "field.cancel_hotkey", helpKey: "help.cancel_hotkey"},
			{
				path:     "client.trigger",
				kind:     KindSelect,
				labelKey: "field.trigger",
				helpKey:  "help.trigger",
				options: []optionSpec{
					{value: "toggle", key: "trigger.toggle"},
					{value: "hold", key: "trigger.hold"},
				},
			},
			{
				path:     "client.hotkey_backend",
				kind:     KindSelect,
				labelKey: "field.hotkey_backend",
				helpKey:  "help.hotkey_backend",
				options: []optionSpec{
					{value: "auto", key: "backend.auto"},
					{value: "portal", key: "backend.portal"},
					{value: "x11", key: "backend.x11"},
				},
			},
			{path: "client.audio.device", kind: KindMicrophone, labelKey: "field.audio.device", helpKey: "help.audio.device"},
			{path: "client.language", kind: KindText, labelKey: "field.language", helpKey: "help.language"},
			{path: "client.paste", kind: KindToggle, labelKey: "field.paste", helpKey: "help.paste"},
			{
				path:     "client.paste_shortcut",
				kind:     KindSelect,
				labelKey: "field.paste_shortcut",
				options: []optionSpec{
					{value: "auto", key: "paste_shortcut.auto"},
					{value: "ctrl_v", key: "paste_shortcut.ctrl_v"},
					{value: "ctrl_shift_v", key: "paste_shortcut.ctrl_shift_v"},
					{value: "shift_insert", key: "paste_shortcut.shift_insert"},
				},
			},
			{
				path:     "client.clipboard_restore",
				kind:     KindSelect,
				labelKey: "field.clipboard_restore",
				options: []optionSpec{
					{value: "auto", key: "clipboard.auto"},
					{value: "always", key: "clipboard.always"},
					{value: "never", key: "clipboard.never"},
				},
			},
			{path: "client.feedback.earcons", kind: KindToggle, labelKey: "field.feedback.earcons", helpKey: "help.feedback.earcons"},
			{
				path:     "client.feedback.indicator",
				kind:     KindSelect,
				labelKey: "field.feedback.indicator",
				options: []optionSpec{
					{value: "auto", key: "indicator.auto"},
					{value: "layer_shell", key: "indicator.layer_shell"},
					{value: "x11", key: "indicator.x11"},
					{value: "off", key: "indicator.off"},
				},
			},
			{path: "client.spool_dir", kind: KindText, labelKey: "field.spool_dir", helpKey: "help.spool_dir"},
		},
	},
}

// View renders the current effective settings.
func (s *Service) View() View {
	snapshot := s.opts.Store.Snapshot()
	catalog := s.catalogFor(snapshot)
	devices, deviceErr := s.microphones()

	view := View{
		Language:   catalog.Lang(),
		Title:      catalog.T("app.title"),
		Subtitle:   catalog.T("app.subtitle"),
		ConfigPath: snapshot.Path,
		Actions: Actions{
			Save:              catalog.T("action.save"),
			Restart:           catalog.T("action.restart"),
			Test:              catalog.T("action.test"),
			Stop:              catalog.T("action.stop"),
			Level:             catalog.T("mic.level"),
			RestartHint:       catalog.T("page.restartHint"),
			HotkeyPlaceholder: catalog.T("hotkey.capture"),
		},
	}
	if deviceErr != nil {
		view.DeviceError = catalog.T("error.mic.list", deviceErr.Error())
	}

	for _, section := range specs {
		rendered := Section{ID: section.id, Title: catalog.T(section.titleKey)}
		for _, spec := range section.fields {
			field, ok := snapshot.Field(spec.path)
			if !ok {
				continue
			}
			viewField := Field{
				Path:        spec.path,
				Label:       catalog.T(spec.labelKey),
				Kind:        spec.kind,
				Value:       fieldValue(field.Value),
				Locked:      field.Locked,
				Source:      string(field.Source),
				SourceLabel: catalog.T("source." + string(field.Source)),
				Mode:        field.Mode,
			}
			if spec.helpKey != "" {
				viewField.Help = catalog.T(spec.helpKey)
			}
			if field.Locked {
				viewField.LockReason = lockReason(catalog, field.Reason)
			}
			if field.Mode == settings.ApplyRestart {
				viewField.Badge = catalog.T("badge.restart")
			}
			switch spec.kind {
			case KindSelect:
				viewField.Options = catalogOptions(catalog, spec.options)
			case KindMicrophone:
				viewField.Options = microphoneOptions(catalog, viewField.Value, devices)
			}
			rendered.Fields = append(rendered.Fields, viewField)
		}
		view.Sections = append(view.Sections, rendered)
	}
	return view
}

// fieldLabel returns the translated label for a config path, falling back to
// the path itself for fields the Dictation screen does not render.
func fieldLabel(catalog i18n.Catalog, path string) string {
	for _, section := range specs {
		for _, spec := range section.fields {
			if spec.path == path {
				return catalog.T(spec.labelKey)
			}
		}
	}
	return path
}

// lockReason translates the settings store's lock explanation. Unknown
// explanations are shown verbatim after a translated prefix.
func lockReason(catalog i18n.Catalog, reason string) string {
	const envPrefix = "overridden by environment variable "
	const cliPrefix = "overridden by the command line"
	switch {
	case strings.HasPrefix(reason, envPrefix):
		return catalog.T("lock.env", strings.TrimPrefix(reason, envPrefix))
	case reason == cliPrefix:
		return catalog.T("lock.cli")
	case reason == "":
		return catalog.T("settings.locked")
	default:
		return catalog.T("lock.other", reason)
	}
}

func catalogOptions(catalog i18n.Catalog, specs []optionSpec) []Option {
	options := make([]Option, 0, len(specs))
	for _, spec := range specs {
		options = append(options, Option{Value: spec.value, Label: catalog.T(spec.key)})
	}
	return options
}

// microphoneOptions lists the system default first, then every capture device.
// A configured selector that is not in the list (a hand-written device name,
// or an unplugged device) keeps an option of its own so saving another field
// does not silently change the microphone.
func microphoneOptions(catalog i18n.Catalog, configured string, devices []mic.Device) []Option {
	options := []Option{{Value: "", Label: catalog.T("mic.default")}}
	present := configured == ""
	for _, device := range devices {
		value := strconv.Itoa(device.Index)
		label := device.Name
		if label == "" {
			label = value
		}
		if device.IsDefault {
			label += " (" + catalog.T("mic.default") + ")"
		}
		if value == configured {
			present = true
		}
		options = append(options, Option{Value: value, Label: label})
	}
	if !present {
		options = append(options, Option{Value: configured, Label: configured})
	}
	return options
}

// microphones enumerates capture devices, reporting an unavailable audio
// backend to the caller.
func (s *Service) microphones() ([]mic.Device, error) {
	if s.opts.Devices == nil {
		return nil, errors.New("no audio backend is configured")
	}
	return s.opts.Devices()
}

// deviceNames lists device names for an error message.
func deviceNames(devices []mic.Device) string {
	names := make([]string, 0, len(devices))
	for _, device := range devices {
		if device.Name != "" {
			names = append(names, device.Name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "—"
	}
	return strings.Join(names, ", ")
}

// fieldValue renders an effective value for the form.
func fieldValue(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		return v
	case bool:
		return strconv.FormatBool(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	default:
		return fmt.Sprint(v)
	}
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
