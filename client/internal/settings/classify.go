package settings

// ApplyMode says whether a change applies to the running Client immediately or
// needs a restart.
type ApplyMode string

const (
	// ApplyHot means the runtime can pick the value up without a restart.
	// Classification is advisory until #71 wires live apply.
	ApplyHot ApplyMode = "hot"
	// ApplyRestart means the running Client cannot apply the value safely; the
	// Settings window offers a one-click restart.
	ApplyRestart ApplyMode = "restart"
)

// Classification is advisory until #71 wires live apply into the runtime. The
// Settings window can already show which changes will be hot and which need a
// restart; for now every persisted change still takes effect on the next start.
//
// hotFields lists the values the Dictation runtime will apply live per the
// desktop spec: hotkey, cancel hotkey, trigger, microphone, language, initial
// prompt, paste/clipboard, earcons, indicator, log level, timeouts and the
// Spool. The active Server profile becomes hot when live apply lands with the
// Settings window (#75); the current runtime builds its transcriber at startup,
// so switching is reported as restart-required.
//
// Everything else needs a restart. The hotkey backend and the Wayland
// injection tool bind at startup, the profile list and active selection are
// read when the transcriber is built, the log file is opened at startup, and
// an unknown field is by definition one the runtime cannot apply safely.
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
}

// Classify reports whether a change to the given field path is hot-applied or
// needs a restart. Unknown paths are restart-required.
func Classify(path string) ApplyMode {
	if _, ok := hotFields[path]; ok {
		return ApplyHot
	}
	return ApplyRestart
}
