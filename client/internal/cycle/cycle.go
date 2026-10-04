package cycle

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
)

// Options wires the core to its ports and limits.
type Options struct {
	Recorder    Recorder
	Transcriber transcribe.Transcriber
	Injector    Injector
	// Feedback is optional; nil means silent.
	Feedback Feedback
	// Spool is optional; nil means failed utterances are dropped.
	Spool Spool
	// Clock is optional; nil uses the real clock.
	Clock Clock
	// OnError is an optional hook invoked for every handled failure.
	OnError func(error)
	Limits  Limits
}

// Core orchestrates the Dictation cycle.
type Core struct {
	recorder    Recorder
	transcriber transcribe.Transcriber
	injector    Injector
	feedback    Feedback
	spooler     Spool
	clock       Clock
	onError     func(error)
	limits      Limits
}

// New builds a Core from its ports. Recorder, Transcriber, and Injector are
// required.
func New(opts Options) *Core {
	c := &Core{
		recorder:    opts.Recorder,
		transcriber: opts.Transcriber,
		injector:    opts.Injector,
		feedback:    opts.Feedback,
		spooler:     opts.Spool,
		clock:       opts.Clock,
		onError:     opts.OnError,
		limits:      opts.Limits,
	}
	if c.clock == nil {
		c.clock = realClock{}
	}
	return c
}

type realClock struct{}

func (realClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// Run consumes triggers until ctx is cancelled, driving the cycle. Errors are
// handled internally (feedback, spool, OnError); Run returns nil on a closed
// trigger channel and ctx.Err() on cancellation.
func (c *Core) Run(ctx context.Context, triggers <-chan Trigger) error {
	recording := false
	var maxC <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			if recording {
				_ = c.recorder.Cancel()
			}
			return ctx.Err()
		case trigger, ok := <-triggers:
			if !ok {
				if recording {
					_ = c.recorder.Cancel()
				}
				return nil
			}
			switch trigger {
			case Toggle:
				if recording {
					c.finish(ctx)
					recording = false
					maxC = nil
				} else if c.start() {
					recording = true
					maxC = c.clock.After(c.limits.MaxRecording)
				}
			case Start:
				if !recording && c.start() {
					recording = true
					maxC = c.clock.After(c.limits.MaxRecording)
				}
			case Stop:
				if recording {
					c.finish(ctx)
					recording = false
					maxC = nil
				}
			case Cancel:
				if recording {
					if err := c.recorder.Cancel(); err != nil {
						c.fail(err)
					}
					recording = false
					maxC = nil
					c.cue(CueStop)
				}
			}
		case <-maxC:
			c.finish(ctx)
			recording = false
			maxC = nil
		}
	}
}

// start begins capture, reporting any failure through fail. It reports whether
// capture is now running.
func (c *Core) start() bool {
	if err := c.recorder.Start(); err != nil {
		c.fail(err)
		return false
	}
	c.cue(CueStart)
	return true
}

// finish stops capture and runs the transcribe -> inject pipeline.
func (c *Core) finish(ctx context.Context) {
	utterance, err := c.recorder.Stop()
	if err != nil {
		c.fail(err)
		return
	}
	if utterance.Duration < c.limits.MinRecording {
		// Accidental taps are discarded silently: no cue, no request.
		return
	}

	timeout := SelectTimeout(c.limits.Timeout, utterance.Duration)
	tctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	transcription, err := c.transcriber.Transcribe(tctx, utterance.Audio)
	if err != nil {
		c.fail(errors.Join(err, c.spool(utterance)))
		return
	}
	if strings.TrimSpace(transcription.Text) == "" {
		c.cue(CueStop)
		return
	}
	if err := c.injector.Inject(transcription.Text); err != nil {
		c.fail(err)
		return
	}
	c.cue(CueStop)
}

func (c *Core) spool(utterance Utterance) error {
	if c.spooler == nil {
		return nil
	}
	return c.spooler.Save(utterance)
}

func (c *Core) fail(err error) {
	if c.onError != nil {
		c.onError(err)
	}
	c.cue(CueError)
}

func (c *Core) cue(cue Cue) {
	if c.feedback != nil {
		c.feedback.Cue(cue)
	}
}
