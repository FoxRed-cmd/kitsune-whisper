package dictation

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/mic"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
)

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
	Language   i18n.Lang `json:"language"`
	Title      string    `json:"title"`
	Subtitle   string    `json:"subtitle"`
	Actions    Actions   `json:"actions"`
	Sections   []Section `json:"sections"`
	ConfigPath string    `json:"configPath"`
	// ConfigTitle labels the configuration path in the page.
	ConfigTitle string `json:"configTitle,omitempty"`
	DeviceError string `json:"deviceError,omitempty"`
}

// fieldSpec describes one field of a Settings screen before its value, source,
// and translated labels are filled in.
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
				path:     "client.hotkey_backend",
				kind:     KindSelect,
				labelKey: "field.hotkey_backend",
				helpKey:  "help.hotkey_backend",
				options: []optionSpec{
					{value: "auto", key: "backend.auto"},
					{value: "portal", key: "backend.portal"},
					{value: "x11", key: "backend.x11"},
					{value: "external", key: "backend.external"},
				},
			},
			{path: "client.audio.device", kind: KindMicrophone, labelKey: "field.audio.device", helpKey: "help.audio.device"},
			{path: "client.language", kind: KindSelect, labelKey: "field.language", helpKey: "help.language"},
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
		Language:    catalog.Lang(),
		Title:       catalog.T("app.title"),
		Subtitle:    catalog.T("app.subtitle"),
		ConfigPath:  snapshot.Path,
		ConfigTitle: catalog.T("settings.configPath"),
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
				if spec.path == "client.language" {
					viewField.Options = LanguageOptions(catalog, viewField.Value)
				} else {
					viewField.Options = catalogOptions(catalog, spec.options)
				}
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
