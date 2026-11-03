package dictation

import (
	"errors"
	"fmt"
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/i18n"
)

// Hotkey paths the service validates.
const (
	// HotkeyPath is the dictation (toggle or push-to-talk) hotkey.
	HotkeyPath = "client.hotkey"
	// CancelHotkeyPath is the cancel hotkey.
	CancelHotkeyPath = "client.cancel_hotkey"
)

// ValidateHotkey checks one hotkey field against the other effective hotkey
// and the platform parser. It is what the recorder calls after a key capture,
// so a conflict is reported before the user presses Save.
func (s *Service) ValidateHotkey(path, value string) error {
	snapshot := s.opts.Store.Snapshot()
	catalog := s.catalogFor(snapshot)
	switch path {
	case HotkeyPath:
		return s.checkHotkey(catalog, path, value, snapshot.Config.CancelHotkey)
	case CancelHotkeyPath:
		return s.checkHotkey(catalog, path, value, snapshot.Config.Hotkey)
	default:
		return fmt.Errorf("dictation: unknown hotkey field %q", path)
	}
}

// validateHotkeyPatch checks the hotkeys a save changes. A field absent from
// the patch is not re-validated: a hand-edited config may carry a spec this
// build cannot parse, and that must not block every other setting.
func (s *Service) validateHotkeyPatch(catalog i18n.Catalog, patch map[string]any) error {
	main, mainChanged := stringValue(patch, "hotkey")
	cancel, cancelChanged := stringValue(patch, "cancel_hotkey")
	if !mainChanged && !cancelChanged {
		return nil
	}
	snapshot := s.opts.Store.Snapshot()
	if !mainChanged {
		main = snapshot.Config.Hotkey
	}
	if !cancelChanged {
		cancel = snapshot.Config.CancelHotkey
	}
	if mainChanged {
		if err := s.checkHotkey(catalog, HotkeyPath, main, cancel); err != nil {
			return err
		}
	}
	if cancelChanged {
		if err := s.checkHotkey(catalog, CancelHotkeyPath, cancel, main); err != nil {
			return err
		}
	}
	return nil
}

// checkHotkey applies the three rules the recorder enforces: the spec cannot
// be empty, it cannot equal the other hotkey, and it must parse.
func (s *Service) checkHotkey(catalog i18n.Catalog, path, value, other string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New(catalog.T("error.hotkey.empty"))
	}
	if other != "" && normalizeHotkey(value) == normalizeHotkey(other) {
		if path == CancelHotkeyPath {
			return fmt.Errorf(catalog.T("error.hotkey.conflict.main"), other)
		}
		return fmt.Errorf(catalog.T("error.hotkey.conflict.cancel"), other)
	}
	if err := s.parser(value); err != nil {
		s.logf("hotkey %q rejected: %v", value, err)
		if path == CancelHotkeyPath {
			return fmt.Errorf(catalog.T("error.hotkey.cancel.invalid"), value)
		}
		return fmt.Errorf(catalog.T("error.hotkey.invalid"), value)
	}
	return nil
}

// normalizeHotkey compares combinations independently of case and spacing.
func normalizeHotkey(spec string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(spec), " ", ""))
}
