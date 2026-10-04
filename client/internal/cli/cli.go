// Package cli is the kitsune-client command-line entry point.
//
// It resolves and validates configuration, supports --check-config, and
// dispatches the commands: run (the interactive hotkey-driven client),
// transcribe-file (POST a WAV), and record (capture once through the Dictation
// cycle). The run command drives the cycle from global hotkeys; real injection
// is wired by a later ticket.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/audio"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/hotkey"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/mic"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
)

// Run parses args and executes, returning a process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("kitsune-client", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to kitsune.yaml")
	checkConfig := fs.Bool("check-config", false, "print effective config and exit")
	device := fs.String("device", "", "audio input device override")
	verbose := fs.Bool("verbose", false, "shorthand for log_level=debug")
	seconds := fs.Float64("seconds", 5, "record: capture length in seconds")
	fs.Usage = func() {
		fmt.Fprint(stderr, usage())
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	// Allow the curated global flags on either side of the subcommand, so
	// "kitsune-client transcribe-file --config x f.wav" works like the Server.
	if len(rest) > 0 && (rest[0] == "transcribe-file" || rest[0] == "record" || rest[0] == "run") {
		command := rest[0]
		if err := fs.Parse(rest[1:]); err != nil {
			return 2
		}
		rest = append([]string{command}, fs.Args()...)
	}

	overrides := map[string]any{}
	if *device != "" {
		overrides["audio"] = map[string]any{"device": *device}
	}
	if *verbose {
		overrides["log_level"] = "debug"
	}

	cfg, err := config.Load(config.LoadOptions{CLIConfig: *configPath, CLIOverrides: overrides})
	if err != nil {
		fmt.Fprintf(stderr, "config error: %v\n", err)
		return 1
	}

	if *checkConfig {
		dumped, err := config.Dump(cfg)
		if err != nil {
			fmt.Fprintf(stderr, "config error: %v\n", err)
			return 1
		}
		fmt.Fprint(stdout, dumped)
		return 0
	}

	if len(rest) == 0 {
		return runClient(cfg, stdout, stderr)
	}
	switch rest[0] {
	case "run":
		return runClient(cfg, stdout, stderr)
	case "transcribe-file":
		return runTranscribeFile(rest[1:], cfg, stdout, stderr)
	case "record":
		return runRecord(rest[1:], *seconds, cfg, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", rest[0])
		fmt.Fprint(stderr, usage())
		return 2
	}
}

func runTranscribeFile(args []string, cfg config.ClientConfig, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: kitsune-client transcribe-file <path.wav>")
		return 2
	}
	data, err := os.ReadFile(args[0])
	if err != nil {
		fmt.Fprintf(stderr, "cannot read %s: %v\n", args[0], err)
		return 1
	}
	utteranceDuration, err := audio.WAVDuration(data)
	if err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", args[0], err)
		return 1
	}

	configured := time.Duration(cfg.TimeoutSeconds * float64(time.Second))
	ctx, cancel := context.WithTimeout(
		context.Background(),
		cycle.SelectTimeout(configured, utteranceDuration),
	)
	defer cancel()

	client := transcribe.NewHTTP(cfg.ServerURL, cfg.Language, cfg.InitialPrompt)
	transcription, err := client.Transcribe(ctx, data)
	if err != nil {
		fmt.Fprintf(stderr, "transcribe failed: %v\n", err)
		return 1
	}
	fmt.Fprintln(stdout, transcription.Text)
	return 0
}

// runRecord captures one Utterance from the configured microphone through the
// Dictation cycle and prints the Transcription. It is the manual counterpart to
// transcribe-file: a way to exercise real capture without the hotkeys.
func runRecord(args []string, seconds float64, cfg config.ClientConfig, stdout, stderr io.Writer) int {
	if len(args) != 0 || seconds <= 0 {
		fmt.Fprintln(stderr, "usage: kitsune-client record [--seconds N]")
		return 2
	}
	recorder, err := newRecorder(cfg, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "capture init failed: %v\n", err)
		return 1
	}
	defer func() { _ = recorder.Close() }()

	out := &stdoutInjector{out: stdout}
	feedback := &cycleFeedback{done: make(chan struct{})}
	var runErr error
	core := cycle.New(cycle.Options{
		Recorder:    recorder,
		Transcriber: newTranscriber(cfg),
		Injector:    out,
		Feedback:    feedback,
		OnError:     func(err error) { runErr = err },
		Limits:      cycleLimits(cfg),
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	triggers := make(chan cycle.Trigger)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = core.Run(ctx, triggers)
	}()

	fmt.Fprintf(stderr, "recording for %.1fs...\n", seconds)
	triggers <- cycle.Toggle
	timer := time.NewTimer(asDuration(seconds))
	defer timer.Stop()
	select {
	case <-timer.C:
		// Only stop if the max-recording bound has not already ended the cycle.
		select {
		case <-feedback.done:
		default:
			triggers <- cycle.Toggle
		}
	case <-feedback.done:
	}

	select {
	case <-feedback.done:
	case <-time.After(cycle.SelectTimeout(asDuration(cfg.TimeoutSeconds), asDuration(seconds)) + 5*time.Second):
		fmt.Fprintln(stderr, "timed out waiting for the transcription")
	}

	close(triggers)
	<-runDone
	if runErr != nil {
		fmt.Fprintf(stderr, "dictation failed: %v\n", runErr)
		return 1
	}
	return 0
}

// runClient is the interactive Client: it grabs the global hotkeys and drives
// the Dictation cycle. Real injection lands in #19; until then a Transcription
// is printed to stdout, matching the record command.
func runClient(cfg config.ClientConfig, stdout, stderr io.Writer) int {
	mode, err := hotkey.ParseMode(cfg.Trigger)
	if err != nil {
		fmt.Fprintf(stderr, "hotkey: %v\n", err)
		return 1
	}
	source, err := hotkey.New(hotkey.Options{
		Hotkey:       cfg.Hotkey,
		CancelHotkey: cfg.CancelHotkey,
		Mode:         mode,
		Retry:        hotkey.RetryPolicy{Initial: 250 * time.Millisecond, Max: 5 * time.Second},
		OnRetry: func(attempt int, err error) {
			fmt.Fprintf(stderr, "hotkey: registration attempt %d failed: %v\n", attempt, err)
		},
	})
	if err != nil {
		fmt.Fprintf(stderr, "hotkey: %v\n", err)
		return 1
	}

	recorder, err := newRecorder(cfg, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "capture init failed: %v\n", err)
		return 1
	}
	defer func() { _ = recorder.Close() }()

	core := cycle.New(cycle.Options{
		Recorder:    recorder,
		Transcriber: newTranscriber(cfg),
		Injector:    &stdoutInjector{out: stdout},
		OnError:     func(err error) { fmt.Fprintf(stderr, "dictation failed: %v\n", err) },
		Limits:      cycleLimits(cfg),
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	triggers := make(chan cycle.Trigger)
	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		_ = core.Run(ctx, triggers)
	}()

	fmt.Fprintf(stderr, "kitsune-client: %s trigger on %s, cancel on %s\n", cfg.Trigger, cfg.Hotkey, cfg.CancelHotkey)
	err = source.Run(ctx, triggers)
	close(triggers)
	<-runDone
	if err != nil {
		fmt.Fprintf(stderr, "hotkey: %v\n", err)
		return 1
	}
	return 0
}

// stdoutInjector is the record command's Injector: print the Transcription.
type stdoutInjector struct {
	out io.Writer
}

func (s *stdoutInjector) Inject(text string) error {
	fmt.Fprintln(s.out, text)
	return nil
}

// cycleFeedback closes done once the cycle finishes (success or failure),
// so the record command knows when it can stop waiting.
type cycleFeedback struct {
	once sync.Once
	done chan struct{}
}

func (f *cycleFeedback) Cue(cue cycle.Cue) {
	if cue == cycle.CueStop || cue == cycle.CueError {
		f.once.Do(func() { close(f.done) })
	}
}

// newRecorder opens the configured microphone, routing device logs to stderr.
func newRecorder(cfg config.ClientConfig, stderr io.Writer) (*mic.Recorder, error) {
	return mic.New(mic.Options{
		Device: cfg.Audio.Device,
		OnLog:  func(message string) { fmt.Fprintf(stderr, "audio: %s\n", message) },
	})
}

// newTranscriber builds the HTTP adapter for the configured Server.
func newTranscriber(cfg config.ClientConfig) transcribe.Transcriber {
	return transcribe.NewHTTP(cfg.ServerURL, cfg.Language, cfg.InitialPrompt)
}

// cycleLimits maps the configured seconds bounds into cycle limits.
func cycleLimits(cfg config.ClientConfig) cycle.Limits {
	return cycle.Limits{
		MinRecording: asDuration(cfg.MinRecordingSeconds),
		MaxRecording: asDuration(cfg.MaxRecordingSeconds),
		Timeout:      asDuration(cfg.TimeoutSeconds),
	}
}

func asDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

func usage() string {
	return `Usage: kitsune-client [flags] [command]

Commands:
  run                          run the hotkey-driven dictation client (default)
  transcribe-file <path.wav>   transcribe a WAV file and print the text
  record                       capture from the microphone and print the text

Flags:
  --config PATH     path to kitsune.yaml
  --check-config    print the effective config and exit
  --device NAME     audio input device override
  --seconds N       record: capture length in seconds (default 5)
  --verbose         shorthand for log_level=debug
`
}
