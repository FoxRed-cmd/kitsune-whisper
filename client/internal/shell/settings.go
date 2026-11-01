package shell

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
)

// Settings is the Dictation settings service the window renders and edits.
// *dictation.Service implements it.
type Settings interface {
	View() dictation.View
	Catalog() i18n.Catalog
	Save(patch map[string]any) (dictation.Outcome, error)
	ValidateHotkey(path, value string) error
	StartLevelTest(device string) (dictation.LevelStream, error)
}

// catalog returns the active user-interface catalog, falling back to English
// when this build has no settings service.
func (s *Shell) catalog() i18n.Catalog {
	if s.opts.Settings == nil {
		return i18n.For(i18n.English)
	}
	return s.opts.Settings.Catalog()
}

// settingsView renders the Settings screens, or a translated empty view when
// no settings service is wired.
func (s *Shell) settingsView() dictation.View {
	if s.opts.Settings == nil {
		catalog := i18n.For(i18n.English)
		return dictation.View{
			Language: catalog.Lang(),
			Title:    catalog.T("app.title"),
			Subtitle: catalog.T("app.subtitle"),
		}
	}
	return s.opts.Settings.View()
}

// Intent is the JSON the Settings page sends for one user action. The page
// collects values and sends them; Go validates, saves, and applies them.
type Intent struct {
	// Action is one of IntentSave, IntentValidate, IntentTestMic,
	// IntentStopMic, or IntentRestart.
	Action string `json:"action"`
	// Patch is a nested "client:"-shaped patch for ActionSave.
	Patch json.RawMessage `json:"patch,omitempty"`
	// Path and Value carry one field for ActionValidate.
	Path  string `json:"path,omitempty"`
	Value string `json:"value,omitempty"`
	// Device is the microphone selector for ActionTestMic.
	Device string `json:"device,omitempty"`
}

// Intent actions.
const (
	IntentSave     = "save"
	IntentValidate = "validate"
	IntentTestMic  = "test-mic"
	IntentStopMic  = "stop-mic"
	IntentRestart  = "restart"
)

// HandleIntent applies one page intent. Validation failures are shown in the
// window rather than returned as errors; an error means the intent itself was
// malformed or no settings service is available.
func (s *Shell) HandleIntent(payload []byte) error {
	if s.opts.Settings == nil {
		return errors.New("settings are not available in this build")
	}
	var intent Intent
	if err := json.Unmarshal(payload, &intent); err != nil {
		return fmt.Errorf("settings intent: %w", err)
	}
	switch intent.Action {
	case IntentSave:
		patch := map[string]any{}
		if len(intent.Patch) > 0 {
			if err := json.Unmarshal(intent.Patch, &patch); err != nil {
				return fmt.Errorf("settings patch: %w", err)
			}
		}
		s.save(patch)
		return nil
	case IntentValidate:
		if err := s.opts.Settings.ValidateHotkey(intent.Path, intent.Value); err != nil {
			s.pushMessages(nil, []string{err.Error()})
			return nil
		}
		s.pushMessages(nil, nil)
		return nil
	case IntentTestMic:
		s.startLevelTest(intent.Device)
		return nil
	case IntentStopMic:
		s.stopLevelTest()
		s.pushMessages(nil, nil)
		return nil
	case IntentRestart:
		s.requestRestart()
		return nil
	default:
		return fmt.Errorf("settings intent: unknown action %q", intent.Action)
	}
}

// save runs one settings save and pushes the resulting view and messages.
func (s *Shell) save(patch map[string]any) {
	outcome, err := s.opts.Settings.Save(patch)
	notices := outcome.Notices
	failures := outcome.Errors
	if err != nil {
		s.log.Warning("settings save: %v", err)
		failures = append(failures, s.catalog().T("error.generic", err.Error()))
	}
	// A written save may have changed the language and the values; re-render
	// the form. A rejected save writes nothing, so the form is left alone.
	if outcome.Written {
		s.pushSettings()
	}
	s.pushMessages(notices, failures)
	// The Tray follows the interface language.
	s.update(s.opts.Runtime.Status())
}

// pushSettings renders the current settings view into the window.
func (s *Shell) pushSettings() {
	view := s.settingsView()
	s.mu.Lock()
	window := s.window
	s.mu.Unlock()
	if window != nil {
		window.UpdateSettings(view)
	}
}

// pushMessages renders save and validation messages without touching the form.
func (s *Shell) pushMessages(notices, failures []string) {
	s.mu.Lock()
	window := s.window
	s.mu.Unlock()
	if window != nil {
		window.UpdateMessages(notices, failures)
	}
}

// startLevelTest opens a microphone level test and streams its levels to the
// window.
func (s *Shell) startLevelTest(device string) {
	s.stopLevelTest()
	test, err := s.opts.Settings.StartLevelTest(device)
	if err != nil {
		s.pushMessages(nil, []string{err.Error()})
		return
	}
	s.mu.Lock()
	s.levelTest = test
	s.mu.Unlock()
	s.pushMessages(nil, nil)
	go s.pumpLevels(test)
}

// pumpLevels forwards Input levels until the test stops.
func (s *Shell) pumpLevels(test dictation.LevelStream) {
	for level := range test.Levels() {
		s.mu.Lock()
		window := s.window
		s.mu.Unlock()
		if window != nil {
			window.UpdateLevel(level)
		}
	}
	s.mu.Lock()
	window := s.window
	if s.levelTest == test {
		s.levelTest = nil
	}
	s.mu.Unlock()
	if window != nil {
		window.UpdateLevel(0)
	}
}

// PrepareTrigger releases a running microphone level test so the Dictation
// trigger that is about to arrive can open the capture device. The runtime
// calls it before the trigger reaches the cycle, for hotkey, control-socket,
// Tray, and Recording-indicator triggers alike. Releasing on PhaseRecording
// stays as a backstop.
func (s *Shell) PrepareTrigger() {
	s.stopLevelTest()
}

// stopLevelTest releases a running microphone test. It is idempotent.
func (s *Shell) stopLevelTest() {
	s.mu.Lock()
	test := s.levelTest
	s.levelTest = nil
	s.mu.Unlock()
	if test == nil {
		return
	}
	if err := test.Stop(); err != nil {
		s.log.Warning("stopping the microphone test: %v", err)
	}
}
