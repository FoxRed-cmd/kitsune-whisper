// Package earcon plays short synthesized tones that signal Dictation-cycle
// state: a rising tone on start, a mid tone on stop, and a low tone on error.
//
// Earcons are optional and off by default. The tone rendering is pure; playing
// it is a thin platform adapter (winmm on Windows, a system player on Linux,
// and silent elsewhere).
package earcon

import (
	"encoding/binary"
	"math"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/audio"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
)

// sampleRate is the canonical audio rate shared with capture and the Server.
const sampleRate = 16000

// amplitude keeps the tones gentle rather than full-scale.
const amplitude = 0.4

// Tone is one synthesized earcon.
type Tone struct {
	FrequencyHz int
	DurationMs  int
}

// ToneFor maps a cycle cue to its earcon.
func ToneFor(cue cycle.Cue) Tone {
	switch cue {
	case cycle.CueStart:
		return Tone{FrequencyHz: 880, DurationMs: 80}
	case cycle.CueStop:
		return Tone{FrequencyHz: 660, DurationMs: 80}
	case cycle.CueError:
		return Tone{FrequencyHz: 220, DurationMs: 200}
	default:
		return Tone{FrequencyHz: 440, DurationMs: 80}
	}
}

// Render synthesizes a tone as canonical 16 kHz mono PCM WAV, with a short fade
// at each end so playback does not click.
func Render(tone Tone) []byte {
	rate := sampleRate
	frames := rate * tone.DurationMs / 1000
	if frames <= 0 {
		frames = 1
	}
	fade := frames / 20
	if fade < 1 {
		fade = 1
	}

	pcm := make([]byte, frames*2)
	frequency := float64(tone.FrequencyHz)
	for i := 0; i < frames; i++ {
		envelope := 1.0
		if i < fade {
			envelope = float64(i) / float64(fade)
		} else if i >= frames-fade {
			envelope = float64(frames-i) / float64(fade)
		}
		sample := amplitude * envelope * math.Sin(2*math.Pi*frequency*float64(i)/float64(rate))
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(int16(sample*32767)))
	}
	return audio.EncodeWAV(pcm, rate, 1)
}

// Options configures Earcons.
type Options struct {
	// Enabled is client.feedback.earcons; when false, Cue is a no-op.
	Enabled bool
	// Play plays a rendered WAV; nil uses the platform player. Tests inject a
	// fake.
	Play func([]byte) error
	// OnLog receives playback failures; nil is silent.
	OnLog func(string)
}

// Earcons is a cycle.Feedback that plays a tone per cue.
type Earcons struct {
	enabled bool
	play    func([]byte) error
	onLog   func(string)
}

// New builds Earcons.
func New(opts Options) *Earcons {
	player := opts.Play
	if player == nil {
		player = play
	}
	return &Earcons{enabled: opts.Enabled, play: player, onLog: opts.OnLog}
}

// Cue plays the tone for cue, unless earcons are disabled.
func (e *Earcons) Cue(cue cycle.Cue) {
	if !e.enabled {
		return
	}
	if err := e.play(Render(ToneFor(cue))); err != nil && e.onLog != nil {
		e.onLog("earcon: " + err.Error())
	}
}
