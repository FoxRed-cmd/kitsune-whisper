package audio_test

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/audio"
)

// s16le builds an interleaved S16LE sample buffer from int16 frames.
func s16le(frames ...[]int16) []byte {
	out := make([]byte, 0, len(frames)*2)
	for _, frame := range frames {
		for _, sample := range frame {
			out = binary.LittleEndian.AppendUint16(out, uint16(sample))
		}
	}
	return out
}

func samples(pcm []byte) []int16 {
	out := make([]int16, len(pcm)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(pcm[i*2:]))
	}
	return out
}

func TestResamplePassthroughWhenAlready16kMono(t *testing.T) {
	in := s16le([]int16{10}, []int16{-20}, []int16{30})
	got := audio.ResampleToMono16k(in, audio.TargetSampleRate, 1)
	if len(got) == 0 || &got[0] != &in[0] {
		t.Fatalf("expected the input slice back unchanged, got a new slice")
	}
}

func TestResampleDownmixesStereoToMono(t *testing.T) {
	// Both channels already at 16 kHz: only the downmix runs.
	in := s16le([]int16{100, 200}, []int16{300, 400})
	got := audio.ResampleToMono16k(in, audio.TargetSampleRate, 2)
	want := []int16{150, 350}
	if gotSamples := samples(got); !equalSamples(gotSamples, want) {
		t.Fatalf("downmix = %v, want %v", gotSamples, want)
	}
}

func TestResampleScalesFrameCount(t *testing.T) {
	const inFrames = 48000
	in := make([]byte, inFrames*2)
	for i := 0; i < inFrames; i++ {
		binary.LittleEndian.PutUint16(in[i*2:], 1000)
	}
	got := audio.ResampleToMono16k(in, 48000, 1)
	if len(got) != (inFrames/3)*2 {
		t.Fatalf("got %d bytes, want %d", len(got), (inFrames/3)*2)
	}
	for i, s := range samples(got) {
		if s != 1000 {
			t.Fatalf("sample %d = %d, want 1000 (constant signal must survive)", i, s)
		}
	}
}

func TestResample44100To16000(t *testing.T) {
	const inFrames = 44100
	in := make([]byte, inFrames*2)
	got := audio.ResampleToMono16k(in, 44100, 1)
	if len(got) != 16000*2 {
		t.Fatalf("got %d bytes, want %d", len(got), 16000*2)
	}
}

func TestResampleEmptyIsEmpty(t *testing.T) {
	if got := audio.ResampleToMono16k(nil, 48000, 2); len(got) != 0 {
		t.Fatalf("got %d bytes from empty input", len(got))
	}
}

func TestResampleAttenuatesContentAboveTargetNyquist(t *testing.T) {
	const amp = 20000
	inBand := audio.ResampleToMono16k(sine(48000, 1000, amp, 48000), 48000, 1)
	if got := peak(inBand); got < amp/2 {
		t.Fatalf("1 kHz tone attenuated to peak %d, want ~%d", got, amp)
	}
	outOfBand := audio.ResampleToMono16k(sine(48000, 20000, amp, 48000), 48000, 1)
	if got := peak(outOfBand); got > amp/5 {
		t.Fatalf("20 kHz tone not attenuated: peak %d", got)
	}
}

func sine(rate, freq, amp, frames int) []byte {
	out := make([]byte, frames*2)
	for i := 0; i < frames; i++ {
		v := int16(math.Round(float64(amp) * math.Sin(2*math.Pi*float64(freq)*float64(i)/float64(rate))))
		binary.LittleEndian.PutUint16(out[i*2:], uint16(v))
	}
	return out
}

func peak(pcm []byte) int {
	maximum := 0
	for _, s := range samples(pcm) {
		if s < 0 {
			s = -s
		}
		if int(s) > maximum {
			maximum = int(s)
		}
	}
	return maximum
}

func TestEncodeWAVRoundTripsThroughDuration(t *testing.T) {
	pcm := make([]byte, audio.TargetSampleRate*2) // one second of mono
	wav := audio.EncodeWAV(pcm, audio.TargetSampleRate, audio.TargetChannels)
	if len(wav) != 44+len(pcm) {
		t.Fatalf("wav length = %d, want %d", len(wav), 44+len(pcm))
	}
	d, err := audio.WAVDuration(wav)
	if err != nil {
		t.Fatalf("duration: %v", err)
	}
	if d.Seconds() != 1 {
		t.Fatalf("duration = %v, want 1s", d)
	}
}

func equalSamples(a, b []int16) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
