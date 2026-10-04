package mic_test

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/audio"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/mic"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
)

// --- fakes ---

type fakeStream struct {
	rate     int
	channels int
	startErr error

	mu     sync.Mutex
	closed bool
}

func (s *fakeStream) SampleRate() int { return s.rate }
func (s *fakeStream) Channels() int   { return s.channels }
func (s *fakeStream) Start() error    { return s.startErr }
func (s *fakeStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func (s *fakeStream) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

type fakeBackend struct {
	devices []mic.Device
	stream  *fakeStream
	openErr error

	mu          sync.Mutex
	openedIndex int
	onData      func([]byte)
}

func (b *fakeBackend) Devices() ([]mic.Device, error) { return b.devices, nil }

func (b *fakeBackend) Open(index int, onData func([]byte)) (mic.Stream, error) {
	if b.openErr != nil {
		return nil, b.openErr
	}
	b.mu.Lock()
	b.openedIndex = index
	b.onData = onData
	b.mu.Unlock()
	return b.stream, nil
}

func (b *fakeBackend) Close() error { return nil }

// push delivers captured PCM the way the real device callback would.
func (b *fakeBackend) push(pcm []byte) {
	b.mu.Lock()
	callback := b.onData
	b.mu.Unlock()
	callback(pcm)
}

func (b *fakeBackend) hasStream() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.onData != nil
}

func (b *fakeBackend) selectedIndex() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.openedIndex
}

// --- helpers ---

func singleDeviceBackend(rate, channels int) *fakeBackend {
	return &fakeBackend{
		devices: []mic.Device{{Index: 0, Name: "Test Mic", IsDefault: true}},
		stream:  &fakeStream{rate: rate, channels: channels},
	}
}

// wavData strips the 44-byte canonical header EncodeWAV always writes.
func wavData(wav []byte) []byte {
	if len(wav) < 44 {
		return nil
	}
	return wav[44:]
}

func s16samples(pcm []byte) []int16 {
	out := make([]int16, len(pcm)/2)
	for i := range out {
		out[i] = int16(binary.LittleEndian.Uint16(pcm[i*2:]))
	}
	return out
}

func constantSamples(frames, channels int, value int16) []byte {
	out := make([]byte, frames*channels*2)
	for i := 0; i < frames*channels; i++ {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(value))
	}
	return out
}

// --- tests ---

func TestRecorderResamplesDeviceRateToCanonicalWAV(t *testing.T) {
	backend := singleDeviceBackend(48000, 2)
	rec, err := mic.New(mic.Options{Backend: backend})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	backend.push(constantSamples(48000, 2, 1000)) // one second at 48 kHz stereo
	utterance, err := rec.Stop()
	if err != nil {
		t.Fatalf("stop: %v", err)
	}

	if !backend.stream.isClosed() {
		t.Fatal("stream was not closed on stop")
	}
	d, err := audio.WAVDuration(utterance.Audio)
	if err != nil {
		t.Fatalf("utterance is not a WAV: %v", err)
	}
	if d != time.Second {
		t.Fatalf("duration = %v, want 1s", d)
	}
	got := s16samples(wavData(utterance.Audio))
	if len(got) != audio.TargetSampleRate {
		t.Fatalf("got %d frames, want %d", len(got), audio.TargetSampleRate)
	}
	for i, sample := range got {
		if sample != 1000 {
			t.Fatalf("sample %d = %d, want 1000", i, sample)
		}
	}
}

func TestRecorderPassesThrough16kMonoUntouched(t *testing.T) {
	backend := singleDeviceBackend(audio.TargetSampleRate, 1)
	rec, err := mic.New(mic.Options{Backend: backend})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	in := make([]byte, 16000*2)
	for i := 0; i < 16000; i++ {
		binary.LittleEndian.PutUint16(in[i*2:], uint16(i))
	}
	if err := rec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	backend.push(in)
	utterance, err := rec.Stop()
	if err != nil {
		t.Fatalf("stop: %v", err)
	}

	got := wavData(utterance.Audio)
	if len(got) != len(in) {
		t.Fatalf("data length = %d, want %d", len(got), len(in))
	}
	for i := range in {
		if got[i] != in[i] {
			t.Fatalf("byte %d changed: %d != %d (resampling must not run)", i, got[i], in[i])
		}
	}
}

func TestRecorderSelectsConfiguredDevice(t *testing.T) {
	backend := &fakeBackend{
		devices: []mic.Device{
			{Index: 0, Name: "Built-in", IsDefault: true},
			{Index: 1, Name: "USB Mic"},
		},
		stream: &fakeStream{rate: audio.TargetSampleRate, channels: 1},
	}
	rec, err := mic.New(mic.Options{Backend: backend, Device: "usb mic"})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if backend.selectedIndex() != 1 {
		t.Fatalf("opened index %d, want 1", backend.selectedIndex())
	}
}

func TestRecorderEmptySelectorDefersToSystemDefault(t *testing.T) {
	backend := &fakeBackend{
		devices: []mic.Device{{Index: 0, Name: "Only One"}},
		stream:  &fakeStream{rate: audio.TargetSampleRate, channels: 1},
	}
	rec, err := mic.New(mic.Options{Backend: backend})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	if backend.selectedIndex() != -1 {
		t.Fatalf("opened index %d, want -1 (system default)", backend.selectedIndex())
	}
}

func TestRecorderCancelClosesAndDiscards(t *testing.T) {
	backend := singleDeviceBackend(audio.TargetSampleRate, 1)
	rec, err := mic.New(mic.Options{Backend: backend})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	backend.push(make([]byte, 1000))
	if err := rec.Cancel(); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if !backend.stream.isClosed() {
		t.Fatal("stream was not closed on cancel")
	}
	if _, err := rec.Stop(); err == nil {
		t.Fatal("stop after cancel should fail")
	}
}

func TestRecorderStartPropagatesDeviceError(t *testing.T) {
	failure := errors.New("device busy")
	backend := singleDeviceBackend(audio.TargetSampleRate, 1)
	backend.stream.startErr = failure
	rec, err := mic.New(mic.Options{Backend: backend})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	if err := rec.Start(); !errors.Is(err, failure) {
		t.Fatalf("start err = %v, want %v", err, failure)
	}
	if !backend.stream.isClosed() {
		t.Fatal("failed start should close the stream")
	}
}

// --- wired through the Dictation cycle ---

type captureTranscriber struct {
	mu    sync.Mutex
	audio []byte
	once  sync.Once
	ch    chan struct{}
}

func newCaptureTranscriber() *captureTranscriber {
	return &captureTranscriber{ch: make(chan struct{})}
}

func (t *captureTranscriber) Transcribe(_ context.Context, pcm []byte) (transcribe.Transcription, error) {
	t.mu.Lock()
	t.audio = pcm
	t.mu.Unlock()
	t.once.Do(func() { close(t.ch) })
	return transcribe.Transcription{Text: "hello"}, nil
}

func (t *captureTranscriber) captured() []byte {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]byte(nil), t.audio...)
}

type injector struct {
	once sync.Once
	ch   chan struct{}
	text string
}

func (i *injector) Inject(text string) error {
	i.text = text
	i.once.Do(func() { close(i.ch) })
	return nil
}

func TestRecorderDrivesDictationCycle(t *testing.T) {
	backend := singleDeviceBackend(48000, 1)
	rec, err := mic.New(mic.Options{Backend: backend})
	if err != nil {
		t.Fatalf("new recorder: %v", err)
	}
	transcriber := newCaptureTranscriber()
	injector := &injector{ch: make(chan struct{})}
	core := cycle.New(cycle.Options{
		Recorder:    rec,
		Transcriber: transcriber,
		Injector:    injector,
		Limits:      cycle.Limits{MaxRecording: time.Hour},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	triggers := make(chan cycle.Trigger)
	go func() { _ = core.Run(ctx, triggers) }()

	triggers <- cycle.Toggle
	waitFor(t, func() bool { return backend.hasStream() }, "capture to open")
	backend.push(constantSamples(48000, 1, 1000))
	triggers <- cycle.Toggle

	select {
	case <-injector.ch:
	case <-time.After(2 * time.Second):
		t.Fatal("cycle did not inject a transcription")
	}
	if injector.text != "hello" {
		t.Fatalf("injected %q, want hello", injector.text)
	}
	if d, err := audio.WAVDuration(transcriber.captured()); err != nil || d != time.Second {
		t.Fatalf("transcriber got duration %v (err %v), want 1s", d, err)
	}
}

func waitFor(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
