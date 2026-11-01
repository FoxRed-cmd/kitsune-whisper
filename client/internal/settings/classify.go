package settings

// ApplyMode says whether a change applies to the running Client immediately or
// needs a restart.
type ApplyMode string

const (
	// ApplyHot means the runtime can pick the value up without a restart. The
	// runtime rebuilds its adapters, or the shell re-renders the value it owns.
	ApplyHot ApplyMode = "hot"
	// ApplyRestart means the running Client cannot apply the value safely; the
	// Settings window offers a one-click restart.
	ApplyRestart ApplyMode = "restart"
)

// Classification is the contract the Settings window and the runtime share:
// the window shows a restart badge for every restart field, and the runtime
// rebuilds its adapters for hot changes without restarting the process. A hot
// field the shell itself renders (the interface language) is applied by the
// shell without a runtime rebuild.
//
// hotFields lists the values the Dictation runtime applies live per the
// desktop spec: hotkey, cancel hotkey, trigger, the hotkey backend, microphone,
// language, initial prompt, paste/clipboard, earcons, indicator, log level,
// timeouts, the Spool, and the active Server profile. The runtime rebuilds its
// transcriber from the active profile, so a Server switch needs no process
// restart.
//
// Everything else needs a restart. The Wayland injection tool binds at startup,
// the profile list is read when the transcriber is built, the log file is
// opened at startup, and an unknown field is by definition one the runtime
// cannot apply safely.
var hotFields = map[string]struct{}{
	"client.hotkey":                {},
	"client.cancel_hotkey":         {},
	"client.trigger":               {},
	"client.hotkey_backend":        {},
	"client.audio.device":          {},
	"client.language":              {},
	"client.ui_language":           {},
	"client.initial_prompt":        {},
	"client.paste":                 {},
	"client.paste_shortcut":        {},
	"client.clipboard_restore":     {},
	"client.feedback.earcons":      {},
	"client.feedback.indicator":    {},
	"client.log_level":             {},
	"client.max_recording_seconds": {},
	"client.min_recording_seconds": {},
	"client.timeout_seconds":       {},
	"client.spool_dir":             {},
	"client.active_server":         {},
}

// Classify reports whether a change to the given field path is hot-applied or
// needs a restart. Unknown paths are restart-required.
func Classify(path string) ApplyMode {
	if _, ok := hotFields[path]; ok {
		return ApplyHot
	}
	return ApplyRestart
}
