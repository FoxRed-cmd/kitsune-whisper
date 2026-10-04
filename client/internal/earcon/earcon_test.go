package earcon_test

import (
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/audio"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/earcon"
)

type recorder struct {
	mu    sync.Mutex
	wavs  [][]byte
	err   error
	calls int
}

func (r *recorder) play(wav []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.wavs = append(r.wavs, wav)
	return r.err
}

func (r *recorder) snapshot() (int, [][]byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls, append([][]byte(nil), r.wavs...)
}

func TestTonesAreDistinctPerCue(t *testing.T) {
	start := earcon.ToneFor(cycle.CueStart)
	stop := earcon.ToneFor(cycle.CueStop)
	failed := earcon.ToneFor(cycle.CueError)
	if start == stop || stop == failed || start == failed {
		t.Fatalf("tones should differ: start=%+v stop=%+v error=%+v", start, stop, failed)
	}
	if start.DurationMs <= 0 || start.FrequencyHz <= 0 {
		t.Fatalf("start tone = %+v", start)
	}
}

func TestRenderProducesAudibleWAV(t *testing.T) {
	tone := earcon.ToneFor(cycle.CueStart)
	wav := earcon.Render(tone)

	duration, err := audio.WAVDuration(wav)
	if err != nil {
		t.Fatalf("render did not produce a WAV: %v", err)
	}
	want := time.Duration(tone.DurationMs) * time.Millisecond
	if diff := duration - want; diff > 10*time.Millisecond || diff < -10*time.Millisecond {
		t.Fatalf("rendered duration = %v, want ~%v", duration, want)
	}
}

func TestCuePlaysTheMatchingTone(t *testing.T) {
	cases := []cycle.Cue{cycle.CueStart, cycle.CueStop, cycle.CueError}
	for _, cue := range cases {
		rec := &recorder{}
		e := earcon.New(earcon.Options{Enabled: true, Play: rec.play})
		e.Cue(cue)

		calls, wavs := rec.snapshot()
		if calls != 1 {
			t.Fatalf("cue %v: played %d times, want 1", cue, calls)
		}
		duration, err := audio.WAVDuration(wavs[0])
		if err != nil {
			t.Fatalf("cue %v: played invalid WAV: %v", cue, err)
		}
		want := time.Duration(earcon.ToneFor(cue).DurationMs) * time.Millisecond
		if diff := duration - want; diff > 10*time.Millisecond || diff < -10*time.Millisecond {
			t.Fatalf("cue %v: played %v, want ~%v", cue, duration, want)
		}
	}
}

func TestDisabledEarconsAreSilent(t *testing.T) {
	rec := &recorder{}
	e := earcon.New(earcon.Options{Enabled: false, Play: rec.play})
	e.Cue(cycle.CueStart)

	if calls, _ := rec.snapshot(); calls != 0 {
		t.Fatalf("disabled earcons played %d times", calls)
	}
}

func TestPlaybackFailureIsLogged(t *testing.T) {
	rec := &recorder{err: errors.New("audio device busy")}
	var logged []string
	e := earcon.New(earcon.Options{Enabled: true, Play: rec.play, OnLog: func(msg string) {
		logged = append(logged, msg)
	}})
	e.Cue(cycle.CueError)

	if len(logged) != 1 || !strings.Contains(logged[0], "audio device busy") {
		t.Fatalf("logged = %v", logged)
	}
}
