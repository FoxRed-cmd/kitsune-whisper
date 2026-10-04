package mic

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/audio"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
)

// Stream is an open capture stream. It delivers PCM at its own native sample
// rate and channel count; conversion to canonical form happens in Stop.
type Stream interface {
	SampleRate() int
	Channels() int
	Start() error
	Close() error
}

// Backend enumerates capture devices and opens one. malgoBackend is the real
// implementation; tests supply a fake through Options.Backend.
type Backend interface {
	Devices() ([]Device, error)
	Open(index int, onData func(pcm []byte)) (Stream, error)
	Close() error
}

// Options configures a Recorder.
type Options struct {
	// Device is the configured selector: empty (system default), an index, or
	// a device name.
	Device string
	// Backend overrides the malgo backend; nil uses the real one.
	Backend Backend
	// OnLog receives malgo diagnostics; nil is silent.
	OnLog func(string)
}

// Recorder implements cycle.Recorder: it captures the selected input device at
// its native rate and yields canonical 16 kHz mono PCM WAV utterances.
type Recorder struct {
	backend  Backend
	selector string

	mu       sync.Mutex
	stream   Stream
	active   bool
	rate     int
	channels int

	pcmMu sync.Mutex
	pcm   []byte
}

var _ cycle.Recorder = (*Recorder)(nil)

// New builds a Recorder, defaulting to the malgo backend.
func New(opts Options) (*Recorder, error) {
	backend := opts.Backend
	if backend == nil {
		native, err := newMalgoBackend(opts.OnLog)
		if err != nil {
			return nil, err
		}
		backend = native
	}
	return &Recorder{backend: backend, selector: opts.Device}, nil
}

// Close releases the audio backend, cancelling any in-progress capture.
func (r *Recorder) Close() error {
	r.mu.Lock()
	active := r.active
	r.mu.Unlock()
	if active {
		_ = r.Cancel()
	}
	return r.backend.Close()
}

// Start opens and starts the selected device, discarding any prior buffer.
func (r *Recorder) Start() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active {
		return nil
	}
	devices, err := r.backend.Devices()
	if err != nil {
		return err
	}
	chosen, err := SelectDevice(r.selector, devices)
	if err != nil {
		return err
	}
	stream, err := r.backend.Open(chosen.Index, r.append)
	if err != nil {
		return err
	}
	r.reset()
	if err := stream.Start(); err != nil {
		_ = stream.Close()
		return err
	}
	r.stream = stream
	r.active = true
	r.rate = stream.SampleRate()
	r.channels = stream.Channels()
	return nil
}

// Stop ends capture and returns the utterance resampled to 16 kHz mono.
func (r *Recorder) Stop() (cycle.Utterance, error) {
	r.mu.Lock()
	stream := r.stream
	rate, channels := r.rate, r.channels
	r.stream = nil
	r.active = false
	r.mu.Unlock()
	if stream == nil {
		return cycle.Utterance{}, errors.New("microphone is not recording")
	}

	closeErr := stream.Close()
	pcm := r.take()
	if closeErr != nil {
		return cycle.Utterance{}, fmt.Errorf("close capture stream: %w", closeErr)
	}

	mono := audio.ResampleToMono16k(pcm, rate, channels)
	return cycle.Utterance{
		Audio:    audio.EncodeWAV(mono, audio.TargetSampleRate, audio.TargetChannels),
		Duration: framesDuration(len(mono) / 2),
	}, nil
}

// Cancel ends capture and discards the buffer.
func (r *Recorder) Cancel() error {
	r.mu.Lock()
	stream := r.stream
	r.stream = nil
	r.active = false
	r.mu.Unlock()
	if stream == nil {
		return nil
	}
	err := stream.Close()
	r.reset()
	return err
}

func (r *Recorder) append(pcm []byte) {
	r.pcmMu.Lock()
	r.pcm = append(r.pcm, pcm...)
	r.pcmMu.Unlock()
}

func (r *Recorder) take() []byte {
	r.pcmMu.Lock()
	defer r.pcmMu.Unlock()
	pcm := r.pcm
	r.pcm = nil
	return pcm
}

func (r *Recorder) reset() {
	r.pcmMu.Lock()
	r.pcm = nil
	r.pcmMu.Unlock()
}

func framesDuration(frames int) time.Duration {
	return time.Duration(frames) * time.Second / audio.TargetSampleRate
}
