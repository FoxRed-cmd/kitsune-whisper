// Package cli is the kitsune-client command-line entry point.
//
// It resolves and validates configuration, supports --check-config, and
// dispatches the commands: run (the interactive hotkey-driven client),
// transcribe-file (POST a WAV), and record (capture once through the Dictation
// cycle). The run command drives the cycle from global hotkeys and injects the
// Transcription into the focused field; record prints it.
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
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/control"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/earcon"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/hotkey"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/inject"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/logging"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/mic"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/paths"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/spool"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
)

// Run parses args and executes, returning a process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("kitsune-client", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to kitsune.yaml")
	checkConfig := fs.Bool("check-config", false, "print effective config and exit")
	device := fs.String("device", "", "audio input device override")
	verbose := fs.Bool("verbose", false, "shorthand for log_level=debug, also echoed to stderr")
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
	if len(rest) > 0 && (rest[0] == "transcribe-file" || rest[0] == "record" || rest[0] == "run" || rest[0] == "toggle") {
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
		return runClient(cfg, *verbose, stderr)
	}
	switch rest[0] {
	case "run":
		return runClient(cfg, *verbose, stderr)
	case "transcribe-file":
		return runTranscribeFile(rest[1:], cfg, *verbose, stdout, stderr)
	case "record":
		return runRecord(rest[1:], *seconds, cfg, *verbose, stdout, stderr)
	case "toggle":
		return runToggle(rest[1:], cfg, *verbose, stderr)
	default:
		fmt.Fprintf(stderr, "unknown command: %s\n", rest[0])
		fmt.Fprint(stderr, usage())
		return 2
	}
}

func runTranscribeFile(args []string, cfg config.ClientConfig, verbose bool, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: kitsune-client transcribe-file <path.wav>")
		return 2
	}
	logger := newLogger(cfg, verbose, stderr)
	defer func() { _ = logger.Close() }()

	data, err := os.ReadFile(args[0])
	if err != nil {
		logger.Error("cannot read %s: %v", args[0], err)
		return 1
	}
	utteranceDuration, err := audio.WAVDuration(data)
	if err != nil {
		logger.Error("%s: %v", args[0], err)
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
		logger.Error("transcribe failed: %v", err)
		return 1
	}
	logger.Debug("transcription: language=%s duration=%.2fs", transcription.Language, transcription.Duration)
	fmt.Fprintln(stdout, transcription.Text)
	return 0
}

// runRecord captures one Utterance from the configured microphone through the
// Dictation cycle and prints the Transcription. It is the manual counterpart to
// transcribe-file: a way to exercise real capture without the hotkeys.
func runRecord(args []string, seconds float64, cfg config.ClientConfig, verbose bool, stdout, stderr io.Writer) int {
	if len(args) != 0 || seconds <= 0 {
		fmt.Fprintln(stderr, "usage: kitsune-client record [--seconds N]")
		return 2
	}
	logger := newLogger(cfg, verbose, stderr)
	defer func() { _ = logger.Close() }()

	recorder, err := newRecorder(cfg, logger)
	if err != nil {
		logger.Error("capture init failed: %v", err)
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
		Feedback:    newFeedback(cfg, logger, feedback),
		Spool:       newSpool(cfg, logger),
		OnError: func(err error) {
			runErr = err
			logger.Error("dictation failed: %v", err)
		},
		Limits: cycleLimits(cfg),
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
		return 1
	}
	return 0
}

// runClient is the interactive Client: it grabs the global hotkeys and drives
// the Dictation cycle, injecting each Transcription into the focused field via
// the clipboard plus a synthetic paste.
func runClient(cfg config.ClientConfig, verbose bool, stderr io.Writer) int {
	logger := newLogger(cfg, verbose, stderr)
	defer func() { _ = logger.Close() }()

	mode, err := hotkey.ParseMode(cfg.Trigger)
	if err != nil {
		logger.Error("hotkey: %v", err)
		return 1
	}
	source, err := hotkey.New(hotkey.Options{
		Hotkey:       cfg.Hotkey,
		CancelHotkey: cfg.CancelHotkey,
		Mode:         mode,
		Backend:      cfg.HotkeyBackend,
		AppID:        hotkey.DefaultAppID,
		Retry:        hotkey.RetryPolicy{Initial: 250 * time.Millisecond, Max: 5 * time.Second},
		OnRetry: func(attempt int, err error) {
			logger.Warning("hotkey: registration attempt %d failed: %v", attempt, err)
		},
		OnLog: func(message string) { logger.Info("%s", message) },
	})
	if err != nil {
		logger.Error("hotkey: %v", err)
		return 1
	}

	recorder, err := newRecorder(cfg, logger)
	if err != nil {
		logger.Error("capture init failed: %v", err)
		return 1
	}
	defer func() { _ = recorder.Close() }()

	injector, err := inject.New(inject.Options{
		Paste:            cfg.Paste,
		PasteShortcut:    cfg.PasteShortcut,
		ClipboardRestore: cfg.ClipboardRestore,
		WaylandTool:      cfg.WaylandTool,
		OnLog:            func(message string) { logger.Debug("inject: %s", message) },
	})
	if err != nil {
		logger.Error("injection init failed: %v", err)
		return 1
	}

	core := cycle.New(cycle.Options{
		Recorder:    recorder,
		Transcriber: newTranscriber(cfg),
		Injector:    injector,
		Feedback:    newFeedback(cfg, logger),
		Spool:       newSpool(cfg, logger),
		OnError:     func(err error) { logger.Error("dictation failed: %v", err) },
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

	controlDone := startControl(ctx, logger, triggers)

	fmt.Fprintf(stderr, "kitsune-client: %s trigger on %s, cancel on %s\n", cfg.Trigger, cfg.Hotkey, cfg.CancelHotkey)
	err = source.Run(ctx, triggers)
	stop()
	if controlDone != nil {
		<-controlDone
	}
	close(triggers)
	<-runDone
	if err != nil {
		logger.Error("hotkey: %v", err)
		return 1
	}
	return 0
}

// startControl runs the External trigger's local control socket, feeding
// commands into triggers until ctx ends. It is best-effort: a failure to bind
// only disables the external trigger. The returned channel closes once the
// server has stopped, so callers can close triggers without a send race.
func startControl(ctx context.Context, logger *logging.Logger, triggers chan<- cycle.Trigger) <-chan struct{} {
	path := paths.ControlSocket()
	listener, err := control.Listen(path)
	if err != nil {
		logger.Warning("external trigger disabled: %v", err)
		return nil
	}
	logger.Info("external trigger: listening on %s", path)

	deliver := func(trigger cycle.Trigger) {
		select {
		case triggers <- trigger:
		case <-ctx.Done():
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := control.NewServer(listener).Serve(ctx, deliver); err != nil {
			logger.Warning("external trigger: %v", err)
		}
	}()
	return done
}

// runToggle sends one toggle command to a running Client over the control
// socket, letting a compositor keybind or script drive the Dictation cycle.
func runToggle(args []string, cfg config.ClientConfig, verbose bool, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: kitsune-client toggle")
		return 2
	}
	logger := newLogger(cfg, verbose, stderr)
	defer func() { _ = logger.Close() }()

	if err := control.Send(paths.ControlSocket(), string(control.Toggle)); err != nil {
		logger.Error("toggle: %v", err)
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

// newLogger builds the Client logger from the configured log_level/log_file,
// echoing to stderr on --verbose. The level is validated during config load, so
// an unknown value is defensive only.
func newLogger(cfg config.ClientConfig, verbose bool, stderr io.Writer) *logging.Logger {
	level, err := logging.ParseLevel(cfg.LogLevel)
	if err != nil {
		level = logging.Info
	}
	return logging.New(logging.Options{
		Level:   level,
		File:    cfg.LogFile,
		Stderr:  stderr,
		Verbose: verbose,
	})
}

// newSpool builds the Spool that catches failed utterances. An unset spool_dir
// resolves to the default cache path inside the Spool.
func newSpool(cfg config.ClientConfig, logger *logging.Logger) *spool.Spool {
	return spool.New(spool.Options{
		Dir:   cfg.SpoolDir,
		OnLog: func(path string) { logger.Info("spool: saved failed utterance to %s", path) },
	})
}

// newFeedback composes the Client's feedback: cue logging, optional earcons,
// and any extra feedback (the record command's completion signal).
func newFeedback(cfg config.ClientConfig, logger *logging.Logger, extra ...cycle.Feedback) cycle.Feedback {
	feedbacks := []cycle.Feedback{logFeedback{log: logger}}
	if cfg.Feedback.Earcons {
		feedbacks = append(feedbacks, earcon.New(earcon.Options{
			Enabled: true,
			OnLog:   func(message string) { logger.Warning("%s", message) },
		}))
	}
	return feedbackGroup(append(feedbacks, extra...))
}

// feedbackGroup fans a cue out to several Feedback ports.
type feedbackGroup []cycle.Feedback

func (g feedbackGroup) Cue(cue cycle.Cue) {
	for _, feedback := range g {
		feedback.Cue(cue)
	}
}

// logFeedback records cycle state at debug level.
type logFeedback struct {
	log *logging.Logger
}

func (f logFeedback) Cue(cue cycle.Cue) {
	switch cue {
	case cycle.CueStart:
		f.log.Debug("dictation cycle: started")
	case cycle.CueStop:
		f.log.Debug("dictation cycle: finished")
	case cycle.CueError:
		f.log.Debug("dictation cycle: failed")
	}
}

// newRecorder opens the configured microphone, routing device logs to the
// logger.
func newRecorder(cfg config.ClientConfig, logger *logging.Logger) (*mic.Recorder, error) {
	return mic.New(mic.Options{
		Device: cfg.Audio.Device,
		OnLog:  func(message string) { logger.Debug("audio: %s", message) },
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
  toggle                       trigger the running client via its control socket
  transcribe-file <path.wav>   transcribe a WAV file and print the text
  record                       capture from the microphone and print the text

Flags:
  --config PATH     path to kitsune.yaml
  --check-config    print the effective config and exit
  --device NAME     audio input device override
  --seconds N       record: capture length in seconds (default 5)
  --verbose         shorthand for log_level=debug, also echoed to stderr
`
}
