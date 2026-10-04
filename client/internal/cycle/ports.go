// Package cycle is the Dictation-cycle core: a state machine driven by toggle,
// start/stop, and cancel triggers, wired to four ports (microphone source,
// transcriber, injector, feedback) plus a spool for failed utterances.
//
// Desktop adapters (malgo, hotkeys, SendInput, wtype/ydotool) are thin and
// live outside this package; tests drive the core with fake ports.
package cycle

import (
	"time"
)

// Utterance is one captured stretch of speech: canonical 16 kHz mono PCM WAV.
type Utterance struct {
	Audio    []byte
	Duration time.Duration
}

// Recorder is the microphone source port.
type Recorder interface {
	// Start begins capturing an utterance.
	Start() error
	// Stop ends capture and returns the captured utterance.
	Stop() (Utterance, error)
	// Cancel ends capture and discards it.
	Cancel() error
}

// Injector places a transcription into the focused field.
type Injector interface {
	Inject(text string) error
}

// Cue is a feedback event.
type Cue int

const (
	// CueStart signals that recording has begun.
	CueStart Cue = iota
	// CueStop signals that a cycle ended without error.
	CueStop
	// CueError signals a failed cycle.
	CueError
)

// Feedback signals cycle state (earcons, logs).
type Feedback interface {
	Cue(cue Cue)
}

// Spool saves an utterance whose transcription failed, so speech is not lost.
type Spool interface {
	Save(utterance Utterance) error
}

// Trigger is an event that drives the cycle.
type Trigger int

const (
	// Toggle starts recording when idle, or stops and processes when recording.
	Toggle Trigger = iota
	// Cancel discards the in-progress recording without sending it.
	Cancel
	// Start begins recording and is a no-op when already recording. Hold-to-talk
	// pairs it with Stop so an auto-stopped cycle does not restart.
	Start
	// Stop ends recording and processes it, and is a no-op when idle.
	Stop
)

// Clock supplies the max-recording timer. Injected so tests run without real
// delays.
type Clock interface {
	After(d time.Duration) <-chan time.Time
}

// Limits are the cycle's timing bounds.
type Limits struct {
	// MinRecording discards shorter recordings silently.
	MinRecording time.Duration
	// MaxRecording auto-stops a recording that runs too long.
	MaxRecording time.Duration
	// Timeout caps the transcribe request; 0 selects max(30s, 2x audio).
	Timeout time.Duration
}

// SelectTimeout resolves the request timeout: an explicit value wins, else
// max(30s, 2x audio length).
func SelectTimeout(configured, audio time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	auto := 2 * audio
	if auto < 30*time.Second {
		return 30 * time.Second
	}
	return auto
}
