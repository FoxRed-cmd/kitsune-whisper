package mic

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"sync/atomic"
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
	onLog    func(string)

	mu     sync.Mutex
	stream Stream
	active bool
	rate   int
	// channels is read by the capture callback, which runs on the audio
	// backend's thread. It is published before the stream starts so early
	// audio is measured with the right channel count.
	channels atomic.Int32
	// level is the latest Input level in [0,1], stored as float64 bits.
	level atomic.Uint64

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
	return &Recorder{backend: backend, selector: opts.Device, onLog: opts.OnLog}, nil
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
	if r.onLog != nil {
		r.onLog(fmt.Sprintf("capture: chose %q (index %d) from %s", chosen.Name, chosen.Index, deviceNames(devices)))
	}
	stream, err := r.backend.Open(chosen.Index, r.append)
	if err != nil {
		return err
	}
	r.reset()
	// Publish the native format before starting: the backend may call the
	// capture callback before Start returns.
	r.rate = stream.SampleRate()
	r.channels.Store(int32(stream.Channels()))
	if err := stream.Start(); err != nil {
		_ = stream.Close()
		return err
	}
	r.stream = stream
	r.active = true
	return nil
}

// InputLevel returns the latest Input level in [0,1], measured as the RMS of
// the captured S16 samples across all channels. It is 0 while idle.
func (r *Recorder) InputLevel() float64 {
	return math.Float64frombits(r.level.Load())
}

// Stop ends capture and returns the utterance resampled to 16 kHz mono.
func (r *Recorder) Stop() (cycle.Utterance, error) {
	r.mu.Lock()
	stream := r.stream
	rate, channels := r.rate, int(r.channels.Load())
	r.stream = nil
	r.active = false
	r.mu.Unlock()
	if stream == nil {
		return cycle.Utterance{}, errors.New("microphone is not recording")
	}

	closeErr := stream.Close()
	r.level.Store(0)
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
	r.level.Store(0)
	return err
}

func (r *Recorder) append(pcm []byte) {
	r.level.Store(math.Float64bits(rmsLevel(pcm, int(r.channels.Load()))))
	r.pcmMu.Lock()
	r.pcm = append(r.pcm, pcm...)
	r.pcmMu.Unlock()
}

// rmsLevel maps S16 RMS to [0,1] full scale. Channels are combined by summing
// squares, so out-of-phase stereo does not cancel to silence.
func rmsLevel(pcm []byte, channels int) float64 {
	if channels < 1 {
		channels = 1
	}
	samples := len(pcm) / 2
	samples -= samples % channels
	if samples <= 0 {
		return 0
	}
	var sumSquares float64
	for i := 0; i < samples; i++ {
		sample := float64(int16(binary.LittleEndian.Uint16(pcm[i*2:])))
		sumSquares += sample * sample
	}
	level := math.Sqrt(sumSquares/float64(samples)) / 32768
	if level > 1 {
		return 1
	}
	return level
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
