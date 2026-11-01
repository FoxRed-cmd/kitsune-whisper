package settings

// ApplyMode says whether a change applies to the running Client immediately or
// needs a restart.
type ApplyMode string

const (
	// ApplyHot means the runtime can pick the value up without a restart.
	ApplyHot ApplyMode = "hot"
	// ApplyRestart means the running Client cannot apply the value safely; the
	// Settings window offers a one-click restart.
	ApplyRestart ApplyMode = "restart"
)

// hotFields lists the values the Dictation runtime applies live, per the
// desktop spec: hotkey, cancel hotkey, trigger, microphone, language, initial
// prompt, paste/clipboard, earcons, indicator, log level, timeouts and the
// Spool. refine and summarize are accepted for one release and ignored by the
// runtime, so changing them needs no restart either. The active Server profile
// becomes hot when the Servers service adds it to this schema.
//
// Everything else needs a restart: the hotkey backend and the Wayland
// injection tool bind at startup, the Server URL is compiled into the
// transcriber, the log file is opened at startup, and an unknown field is by
// definition one the runtime cannot apply safely.
var hotFields = map[string]struct{}{
	"client.hotkey":                {},
	"client.cancel_hotkey":         {},
	"client.trigger":               {},
	"client.audio.device":          {},
	"client.language":              {},
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
	"client.refine":                {},
	"client.summarize":             {},
}

// Classify reports whether a change to the given field path is hot-applied or
// needs a restart. Unknown paths are restart-required.
func Classify(path string) ApplyMode {
	if _, ok := hotFields[path]; ok {
		return ApplyHot
	}
	return ApplyRestart
}
