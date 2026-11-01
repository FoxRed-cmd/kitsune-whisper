package dictation

import (
	"errors"
	"fmt"
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/mic"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
)

// Change is one field a save changed.
type Change struct {
	// Path is the config path, e.g. "client.hotkey".
	Path string `json:"path"`
	// Label is the translated field label.
	Label string `json:"label"`
	// Mode says whether the change applied to the running Client or needs a
	// restart.
	Mode settings.ApplyMode `json:"mode"`
}

// Outcome reports what a save did, in the words the Settings window shows.
type Outcome struct {
	// Written is false when nothing changed and the file is untouched.
	Written bool `json:"written"`
	// Applied lists the changes the running Client picked up without a
	// restart.
	Applied []Change `json:"applied,omitempty"`
	// RestartRequired lists the changes that need a Client restart.
	RestartRequired []Change `json:"restartRequired,omitempty"`
	// Notices are translated success messages.
	Notices []string `json:"notices,omitempty"`
	// Errors are translated, actionable failures. A rejected save writes
	// nothing; the Outcome still renders so the window can show the messages.
	Errors []string `json:"errors,omitempty"`
}

// Save validates and persists a patch shaped like the "client:" section, then
// applies the hot changes to the running Client.
//
// Validation failures and apply failures are returned as translated messages
// in Outcome.Errors rather than Go errors: they are expected user-facing
// states, not transport failures. The returned error is reserved for an
// unexpected store failure.
func (s *Service) Save(patch map[string]any) (Outcome, error) {
	var outcome Outcome
	catalog := s.Catalog()

	// Hotkey rules are checked here, not only in config, so the window can say
	// why a combination is unusable. Only the hotkeys present in the patch are
	// checked strictly; the other is compared for conflicts when it parses.
	if err := s.validateHotkeyPatch(catalog, patch); err != nil {
		outcome.Errors = append(outcome.Errors, err.Error())
		return outcome, nil
	}
	if err := s.validateDevicePatch(catalog, patch); err != nil {
		outcome.Errors = append(outcome.Errors, err.Error())
		return outcome, nil
	}

	result, err := s.opts.Store.Save(patch)
	if err != nil {
		var fieldErr *settings.Error
		if !errors.As(err, &fieldErr) {
			s.logf("settings save failed: %v", err)
		}
		outcome.Errors = append(outcome.Errors, localizeStoreError(catalog, err)...)
		return outcome, nil
	}
	outcome.Written = result.Written
	// The save may have changed the interface language; use the new catalog.
	catalog = s.Catalog()
	if !result.Written {
		outcome.Notices = append(outcome.Notices, catalog.T("notice.noChanges"))
		return outcome, nil
	}

	var hot []Change
	var restart []string
	for _, change := range result.Changes {
		rendered := Change{
			Path:  change.Path,
			Label: fieldLabel(catalog, change.Path),
			Mode:  change.Mode,
		}
		if change.Mode == settings.ApplyHot {
			hot = append(hot, rendered)
			continue
		}
		outcome.RestartRequired = append(outcome.RestartRequired, rendered)
		restart = append(restart, rendered.Label)
	}

	// A hot change the shell renders itself is applied by re-rendering; every
	// other hot change needs the runtime to rebuild its adapters. Without a
	// runtime (a headless build) those changes take effect on the next start.
	var runtimeChanges []Change
	for _, change := range hot {
		if shellApplied[change.Path] {
			outcome.Applied = append(outcome.Applied, change)
			continue
		}
		if s.opts.Runtime == nil {
			outcome.RestartRequired = append(outcome.RestartRequired, change)
			restart = append(restart, change.Label)
			continue
		}
		runtimeChanges = append(runtimeChanges, change)
	}
	if len(runtimeChanges) > 0 {
		if err := s.opts.Runtime.Apply(s.opts.Store.Snapshot().Config); err != nil {
			s.logf("apply settings: %v", err)
			outcome.Errors = append(outcome.Errors, catalog.T("error.apply", err.Error()))
		} else {
			outcome.Applied = append(outcome.Applied, runtimeChanges...)
		}
	}

	outcome.Notices = append(outcome.Notices, catalog.T("notice.saved"))
	if len(restart) > 0 {
		outcome.Notices = append(outcome.Notices, catalog.T("notice.restartRequired", strings.Join(restart, ", ")))
	}
	return outcome, nil
}

// shellApplied are the hot fields the Settings shell itself renders from the
// new value; they need no runtime adapter rebuilt. Rebuilding the hotkey and
// the microphone just to change a label would interrupt dictation for nothing.
var shellApplied = map[string]bool{
	"client.ui_language": true,
}

// validateDevicePatch checks a microphone change against the enumerated
// devices. An unavailable audio backend does not block the save: without a
// backend the value cannot be checked, and a stale enumeration must not make
// the rest of the settings unsavable.
func (s *Service) validateDevicePatch(catalog i18n.Catalog, patch map[string]any) error {
	value, ok := nestedString(patch, "audio", "device")
	if !ok {
		return nil
	}
	devices, err := s.microphones()
	if err != nil {
		s.logf("cannot verify microphone %q: %v", value, err)
		return nil
	}
	return checkDevice(catalog, value, devices)
}

// checkDevice resolves one microphone selector against the device list and
// returns one actionable message when it cannot be used.
func checkDevice(catalog i18n.Catalog, selector string, devices []mic.Device) error {
	if strings.TrimSpace(selector) == "" {
		return nil
	}
	if len(devices) == 0 {
		return errors.New(catalog.T("error.mic.none"))
	}
	if _, err := mic.SelectDevice(selector, devices); err != nil {
		return fmt.Errorf(catalog.T("error.mic.unknown"), selector, deviceNames(devices))
	}
	return nil
}

// localizeStoreError turns the settings store's field errors into translated
// messages. Every field is reported, so one save shows all problems at once.
func localizeStoreError(catalog i18n.Catalog, err error) []string {
	var fieldErr *settings.Error
	if !errors.As(err, &fieldErr) {
		return []string{catalog.T("error.generic", err.Error())}
	}
	messages := make([]string, 0, len(fieldErr.Fields))
	for _, field := range fieldErr.Fields {
		messages = append(messages, localizeFieldError(catalog, field))
	}
	return messages
}

func localizeFieldError(catalog i18n.Catalog, field settings.FieldError) string {
	var localized string
	switch field.Reason {
	case settings.ReasonUnknownKey:
		localized = catalog.T("error.field.unknown")
	case settings.ReasonNotMapping:
		localized = catalog.T("error.field.mapping")
	case settings.ReasonRequired:
		localized = catalog.T("error.field.required")
	case settings.ReasonChoice:
		localized = catalog.T("error.field.choice", field.Detail)
	case settings.ReasonLocked:
		localized = catalog.T("error.field.locked", lockReason(catalog, field.Detail))
	case settings.ReasonText:
		localized = catalog.T("error.field.text")
	case settings.ReasonBoolean:
		localized = catalog.T("error.field.boolean")
	case settings.ReasonNumber:
		localized = catalog.T("error.field.number")
	case settings.ReasonRange:
		localized = catalog.T("error.field.range")
	default:
		localized = catalog.T("error.field.invalid", field.Message)
	}
	if field.Path == "" {
		return localized
	}
	return field.Path + ": " + localized
}

// nestedString reads one nested string value from a patch shaped like the
// "client:" section.
func nestedString(patch map[string]any, group, key string) (string, bool) {
	raw, ok := patch[group]
	if !ok {
		return "", false
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := values[key]
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

// stringValue renders a patch value as a string when it is one.
func stringValue(patch map[string]any, key string) (string, bool) {
	raw, ok := patch[key]
	if !ok {
		return "", false
	}
	text, ok := raw.(string)
	return text, ok
}
