// Package cli is the kitsune-client command-line entry point.
//
// It resolves and validates configuration, supports --check-config, and
// dispatches the one-shot commands: transcribe-file (POST a WAV) and record
// (capture from the microphone through the Dictation cycle). The interactive
// daemon (global hotkey, injection) is wired by later tickets.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/audio"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
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
	if len(rest) > 0 && (rest[0] == "transcribe-file" || rest[0] == "record") {
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
		fmt.Fprintln(stderr, "the interactive hotkey client is not implemented yet; use 'transcribe-file' or 'record'")
		return 1
	}
	switch rest[0] {
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
// transcribe-file: a way to exercise real capture before the hotkey daemon
// lands.
func runRecord(args []string, seconds float64, cfg config.ClientConfig, stdout, stderr io.Writer) int {
	if len(args) != 0 || seconds <= 0 {
		fmt.Fprintln(stderr, "usage: kitsune-client record [--seconds N]")
		return 2
	}
	recorder, err := mic.New(mic.Options{
		Device: cfg.Audio.Device,
		OnLog:  func(message string) { fmt.Fprintf(stderr, "audio: %s\n", message) },
	})
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
		Transcriber: transcribe.NewHTTP(cfg.ServerURL, cfg.Language, cfg.InitialPrompt),
		Injector:    out,
		Feedback:    feedback,
		OnError:     func(err error) { runErr = err },
		Limits: cycle.Limits{
			MinRecording: asDuration(cfg.MinRecordingSeconds),
			MaxRecording: asDuration(cfg.MaxRecordingSeconds),
			Timeout:      asDuration(cfg.TimeoutSeconds),
		},
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

func asDuration(seconds float64) time.Duration {
	return time.Duration(seconds * float64(time.Second))
}

func usage() string {
	return `Usage: kitsune-client [flags] [command]

Commands:
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
