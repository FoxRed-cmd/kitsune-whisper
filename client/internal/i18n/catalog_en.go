package i18n

// english is the reference catalog: every other catalog must define the same
// keys (TestEnglishAndRussianCatalogsCoverTheSameKeys enforces it), and it is
// the fallback for any key a translation misses.
var english = map[string]string{
	"app.title":    "kitsune-whisper",
	"app.subtitle": "Private, LAN-local dictation",

	"status.section": "Status",
	"status.phase":   "Phase",
	"status.client":  "Client",
	"status.profile": "Server profile",
	"status.version": "Version",
	"status.running": "running",
	"status.stopped": "stopped",

	"state.idle":       "Idle",
	"state.recording":  "Recording",
	"state.processing": "Processing",
	"state.error":      "Error",

	"phase.idle":       "idle",
	"phase.recording":  "recording",
	"phase.processing": "processing",
	"phase.delivering": "delivering",

	"section.general":   "General",
	"section.dictation": "Dictation",

	"field.ui_language":        "Interface language",
	"help.ui_language":         "The language of this window and the Tray. The system language is used unless you pick one.",
	"hotkey.capture":           "Press the combination",
	"field.hotkey":             "Dictation hotkey",
	"help.hotkey":              "Press the combination in the field. Toggle or push-to-talk is set below.",
	"field.cancel_hotkey":      "Cancel hotkey",
	"help.cancel_hotkey":       "Discards the current recording without sending it to the Server.",
	"field.trigger":            "Trigger mode",
	"help.trigger":             "Toggle starts and stops with one press. Push-to-talk records while the key is held.",
	"field.hotkey_backend":     "Hotkey backend",
	"help.hotkey_backend":      "Automatic picks the session's native mechanism. Changing it requires a Client restart.",
	"field.audio.device":       "Microphone",
	"help.audio.device":        "Pick the input device and check its level before dictating.",
	"field.language":           "Spoken language",
	"help.language":            "A language code such as en or ru, or auto to detect it.",
	"field.paste":              "Paste automatically",
	"help.paste":               "Use the paste shortcut instead of typing the text character by character.",
	"field.paste_shortcut":     "Paste shortcut",
	"field.clipboard_restore":  "Clipboard restore",
	"field.feedback.earcons":   "Earcons",
	"help.feedback.earcons":    "Short tones when recording starts and stops and when a cycle fails.",
	"field.feedback.indicator": "Recording indicator",
	"field.spool_dir":          "Spool folder",
	"help.spool_dir":           "Failed recordings are kept here. Leave empty to use the default folder.",

	"language.system": "System language",
	"language.en":     "English",
	"language.ru":     "Russian",

	"trigger.toggle": "Toggle",
	"trigger.hold":   "Push-to-talk",

	"backend.auto":   "Automatic",
	"backend.portal": "GlobalShortcuts portal",
	"backend.x11":    "X11",

	"paste_shortcut.auto":         "Automatic",
	"paste_shortcut.ctrl_v":       "Ctrl+V",
	"paste_shortcut.ctrl_shift_v": "Ctrl+Shift+V",
	"paste_shortcut.shift_insert": "Shift+Insert",

	"clipboard.auto":   "Automatic",
	"clipboard.always": "Always restore",
	"clipboard.never":  "Never restore",

	"indicator.auto":        "Automatic",
	"indicator.layer_shell": "Layer shell (Wayland)",
	"indicator.x11":         "X11 window",
	"indicator.off":         "Off",

	"source.default":      "Default",
	"source.file":         "Saved",
	"source.environment":  "Environment",
	"source.cli":          "Command line",
	"lock.env":            "Locked by the environment variable %s",
	"lock.cli":            "Locked by a command-line flag",
	"lock.other":          "Locked: %s",
	"settings.configPath": "Configuration file",
	"settings.locked":     "Locked",

	"action.save":    "Save",
	"action.restart": "Restart Client",
	"action.test":    "Test",
	"action.stop":    "Stop",

	"badge.restart": "Restart required",

	"notice.saved":           "Saved",
	"notice.noChanges":       "No changes",
	"notice.restartRequired": "Restart the Client to apply: %s",
	"error.apply":            "Saved, but the running Client could not apply the change: %s. Restart the Client.",
	"error.generic":          "Could not complete the action: %s",

	"error.hotkey.empty":           "Enter a hotkey combination, for example Ctrl+Shift+Space.",
	"error.hotkey.invalid":         "“%s” is not a valid hotkey. Use modifiers and a key, for example Ctrl+Shift+Space.",
	"error.hotkey.cancel.invalid":  "“%s” is not a valid cancel hotkey. Use modifiers and a key, for example Ctrl+Shift+Q.",
	"error.hotkey.conflict.cancel": "The dictation hotkey must differ from the cancel hotkey (%s).",
	"error.hotkey.conflict.main":   "The cancel hotkey must differ from the dictation hotkey (%s).",

	"mic.default":       "System default",
	"mic.level":         "Input level",
	"mic.noDevices":     "No microphones found.",
	"error.mic.list":    "Could not list microphones: %s.",
	"error.mic.unknown": "Microphone “%s” is not available. Choose one of: %s.",
	"error.mic.start":   "Could not open microphone “%s”: %s. Choose another device or check your audio settings.",
	"error.mic.none":    "No microphone was found. Connect one and reload the devices.",

	"error.field.text":     "Enter text.",
	"error.field.boolean":  "Choose on or off.",
	"error.field.range":    "The value is out of range.",
	"error.field.unknown":  "This setting is not recognised.",
	"error.field.mapping":  "This setting must be a group of values.",
	"error.field.required": "This setting cannot be empty.",
	"error.field.choice":   "Choose one of: %s.",
	"error.field.number":   "Enter a number.",
	"error.field.locked":   "This setting is locked: %s.",
	"error.field.invalid":  "Invalid value: %s.",

	"tray.status":   "kitsune-whisper — %s",
	"tray.start":    "Start dictation",
	"tray.stop":     "Stop dictation",
	"tray.settings": "Settings…",
	"tray.restart":  "Restart Client",
	"tray.quit":     "Quit",

	"page.restartHint": "Restart the Client to apply changes marked “Restart required”.",
}
