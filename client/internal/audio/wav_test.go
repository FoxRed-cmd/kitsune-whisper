package audio_test

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/audio"
)

func wav(sampleRate, channels, bits, frames int) []byte {
	var b bytes.Buffer
	dataSize := frames * channels * (bits / 8)
	byteRate := sampleRate * channels * (bits / 8)
	blockAlign := channels * (bits / 8)

	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+dataSize))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&b, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&b, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(&b, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(&b, binary.LittleEndian, uint16(bits))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(dataSize))
	b.Write(make([]byte, dataSize))
	return b.Bytes()
}

func TestWAVDuration16kMono(t *testing.T) {
	// 1.5 seconds at 16 kHz = 24000 frames.
	d, err := audio.WAVDuration(wav(16000, 1, 16, 24000))
	if err != nil {
		t.Fatalf("duration: %v", err)
	}
	if d != 1500*time.Millisecond {
		t.Fatalf("duration = %v, want 1.5s", d)
	}
}

func TestWAVDurationStereo(t *testing.T) {
	d, err := audio.WAVDuration(wav(44100, 2, 16, 44100))
	if err != nil {
		t.Fatalf("duration: %v", err)
	}
	if d != time.Second {
		t.Fatalf("duration = %v, want 1s", d)
	}
}

func TestNotWAV(t *testing.T) {
	if _, err := audio.WAVDuration([]byte("this is not a wav file")); err == nil {
		t.Fatal("expected error for non-WAV data")
	}
}
