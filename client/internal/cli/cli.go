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
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/audio"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/autostart"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/control"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/earcon"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/hotkey"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/indicator"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/inject"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/instance"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/logging"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/mic"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/paths"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/restart"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/runtime"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/session"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/settings"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/shell/wails"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/spool"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
)

// Version is the Client build version. Release builds override it with
// -ldflags "-X .../internal/cli.Version=vX.Y.Z"; the default marks a local
// build.
var Version = "dev"

// commands that take the curated global flags on either side of the name.
var subcommands = map[string]bool{
	"run":                 true,
	"gui":                 true,
	"settings":            true,
	"toggle":              true,
	"transcribe-file":     true,
	"record":              true,
	"install-autostart":   true,
	"uninstall-autostart": true,
}

// Run parses args and executes, returning a process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("kitsune-client", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to kitsune.yaml")
	checkConfig := fs.Bool("check-config", false, "print effective config and exit")
	device := fs.String("device", "", "audio input device override")
	verbose := fs.Bool("verbose", false, "shorthand for log_level=debug, also echoed to stderr")
	seconds := fs.Float64("seconds", 5, "record: capture length in seconds")
	hidden := fs.Bool("hidden", false, "gui: start in the Tray without a window")
	showVersion := fs.Bool("version", false, "print the version and exit")
	fs.Usage = func() {
		fmt.Fprint(stderr, usage())
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	// Allow the curated global flags on either side of the subcommand, so
	// "kitsune-client transcribe-file --config x f.wav" works like the Server.
	if len(rest) > 0 && subcommands[rest[0]] {
		command := rest[0]
		if err := fs.Parse(rest[1:]); err != nil {
			return 2
		}
		rest = append([]string{command}, fs.Args()...)
	}

	if *showVersion {
		fmt.Fprintf(stdout, "kitsune-client %s\n", Version)
		return 0
	}
	if *hidden && (len(rest) == 0 || rest[0] != "gui") {
		fmt.Fprintln(stderr, "--hidden is only valid with the gui command")
		return 2
	}
	// Autostart registration does not need the rest of the config; pinning an
	// explicit --config only affects the command line written into the unit or
	// task.
	if len(rest) > 0 && rest[0] == "install-autostart" {
		return runAutostart(rest[1:], *configPath, true, stdout, stderr)
	}
	if len(rest) > 0 && rest[0] == "uninstall-autostart" {
		return runAutostart(rest[1:], *configPath, false, stdout, stderr)
	}

	overrides := map[string]any{}
	if *device != "" {
		overrides["audio"] = map[string]any{"device": *device}
	}
	if *verbose {
		overrides["log_level"] = "debug"
	}

	cfg, err := config.Load(config.LoadOptions{
		CLIConfig:    *configPath,
		CLIOverrides: overrides,
		OnWarning: func(message string) {
			fmt.Fprintf(stderr, "config warning: %s\n", message)
		},
	})
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
	case "gui":
		return runGUI(rest[1:], *hidden, cfg, *verbose, *configPath, overrides, stderr)
	case "settings":
		return runSettings(rest[1:], cfg, *verbose, stderr)
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

	client := newTranscriber(cfg, logger)
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
		Transcriber: newTranscriber(cfg, logger),
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

// runClient starts the headless Client. It takes the single-instance lock so a
// second Client cannot grab the same hotkeys and microphone; a duplicate `run`
// exits quietly because the user already has a Client.
func runClient(cfg config.ClientConfig, verbose bool, stderr io.Writer) int {
	lock, code := acquireLock(false, false, stderr)
	if lock == nil {
		return code
	}
	defer func() { _ = lock.Release() }()
	return serve(cfg, serveOptions{verbose: verbose}, lock, stderr)
}

// runGUI starts the desktop shell: the Settings window plus the Tray. A second
// visible launch forwards the settings command to the running Client, while a
// second hidden launch (autostart) exits quietly. When the build has no
// desktop adapter or the window/Tray cannot be created, the runtime keeps
// dictating headlessly.
func runGUI(args []string, hidden bool, cfg config.ClientConfig, verbose bool, configPath string, overrides map[string]any, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: kitsune-client gui [--hidden]")
		return 2
	}
	lock, code := acquireLock(true, hidden, stderr)
	if lock == nil {
		return code
	}
	defer func() { _ = lock.Release() }()
	return serve(cfg, serveOptions{
		verbose:    verbose,
		gui:        true,
		hidden:     hidden,
		configPath: configPath,
		overrides:  overrides,
	}, lock, stderr)
}

// runSettings asks the running desktop shell to show the Settings window.
func runSettings(args []string, cfg config.ClientConfig, verbose bool, stderr io.Writer) int {
	if len(args) != 0 {
		fmt.Fprintln(stderr, "usage: kitsune-client settings")
		return 2
	}
	logger := newLogger(cfg, verbose, stderr)
	defer func() { _ = logger.Close() }()

	if err := requestSettings(); err != nil {
		logger.Error("settings: %v", err)
		return 1
	}
	return 0
}

// requestSettings asks the running Client to show its Settings window.
func requestSettings() error {
	return control.Send(paths.ControlSocket(), string(control.Settings))
}

// acquireLock takes the single-instance lock or forwards a duplicate launch.
// It returns a nil lock with the process exit code when another Client already
// owns the session.
func acquireLock(gui, hidden bool, stderr io.Writer) (*instance.Lock, int) {
	lock, err := instance.Acquire(paths.InstanceLock())
	if err == nil {
		return lock, 0
	}
	if !errors.Is(err, instance.ErrAlreadyRunning) {
		fmt.Fprintf(stderr, "kitsune-client: %v\n", err)
		return nil, 1
	}
	if gui && !hidden {
		if err := requestSettings(); err != nil {
			fmt.Fprintf(stderr, "kitsune-client: already running; Settings window unavailable: %v\n", err)
			return nil, 1
		}
		return nil, 0
	}
	fmt.Fprintln(stderr, "kitsune-client: already running")
	return nil, 0
}

// serveOptions are the launch parameters that stay out of the configuration.
type serveOptions struct {
	verbose bool
	gui     bool
	hidden  bool
	// configPath is the explicit --config path, so the settings store reads and
	// writes the same file the Client loaded.
	configPath string
	// overrides are the curated CLI flags, so the settings store reports them
	// as locked fields.
	overrides map[string]any
}

// serve builds the real adapters, starts the runtime, and either waits
// headlessly or hands control to the desktop shell.
func serve(cfg config.ClientConfig, so serveOptions, lock *instance.Lock, stderr io.Writer) int {
	logger := newLogger(cfg, so.verbose, stderr)
	defer func() { _ = logger.Close() }()

	adapters, err := buildAdapters(cfg, logger)
	if err != nil {
		logger.Error("%v", err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var sh *shell.Shell
	opts := runtime.Options{
		Config:      cfg,
		Hotkey:      adapters.Hotkey,
		Recorder:    adapters.Recorder,
		Transcriber: adapters.Transcriber,
		Injector:    adapters.Injector,
		Spool:       adapters.Spool,
		Feedback:    adapters.Feedback,
		Indicator:   adapters.Indicator,
		// A settings change rebuilds the adapters through the same factory the
		// initial launch used, so hot changes never need a process restart.
		Rebuild: func(next config.ClientConfig) (runtime.Adapters, error) {
			return buildAdapters(next, logger)
		},
		ControlPath: paths.ControlSocket(),
		Logger:      logger,
	}
	if so.gui {
		opts.OnSettings = func() error {
			if sh == nil {
				return errors.New("the Settings window is still starting")
			}
			return sh.ShowSettings()
		}
	}
	rt := runtime.New(opts)

	if so.gui {
		var settingsService shell.Settings
		if service := newSettingsService(rt, so, logger); service != nil {
			settingsService = service
		}
		server, _ := cfg.ActiveProfile()
		sh = shell.New(shell.Options{
			Runtime:  rt,
			Desktop:  wails.New(wails.Options{Version: Version, Logger: logger}),
			Settings: settingsService,
			Send:     rt.Trigger,
			Version:  Version,
			Profile:  server.Name,
			Logger:   logger,
		})
	}

	if err := rt.Start(ctx); err != nil {
		if closer, ok := adapters.Recorder.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		logger.Error("%v", err)
		return 1
	}

	if sh != nil {
		err := sh.Run(ctx, so.hidden)
		// Restart must release the single-instance lock before the new process
		// tries to acquire it.
		if releaseErr := lock.Release(); releaseErr != nil {
			logger.Warning("%v", releaseErr)
		}
		if errors.Is(err, shell.ErrRestart) {
			if restartErr := restart.Exec(); restartErr != nil {
				logger.Error("%v", restartErr)
				return 1
			}
			return 0
		}
		if err != nil {
			logger.Error("%v", err)
			return 1
		}
		return 0
	}

	fmt.Fprintf(stderr, "kitsune-client: %s trigger on %s, cancel on %s\n", cfg.Trigger, cfg.Hotkey, cfg.CancelHotkey)
	if err := rt.Wait(); err != nil {
		logger.Error("hotkey: %v", err)
		return 1
	}
	return 0
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

// runAutostart registers or removes the Client's login autostart entry. It is
// what the install scripts call: the binary knows its own absolute path and the
// platform's registration mechanism, so the scripts stay thin.
func runAutostart(args []string, configPath string, install bool, stdout, stderr io.Writer) int {
	action := "uninstall"
	if install {
		action = "install"
	}
	if len(args) != 0 {
		fmt.Fprintf(stderr, "usage: kitsune-client %s-autostart [--config PATH]\n", action)
		return 2
	}

	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "autostart: locate executable: %v\n", err)
		return 1
	}
	pinned := ""
	if configPath != "" {
		pinned, err = filepath.Abs(configPath)
		if err != nil {
			fmt.Fprintf(stderr, "autostart: resolve config path: %v\n", err)
			return 1
		}
	}

	env := autostart.Default(func(format string, a ...any) {
		fmt.Fprintf(stderr, format+"\n", a...)
	})
	opts := autostart.Options{Binary: exe, Config: pinned}
	if install {
		err = autostart.Install(env, opts)
	} else {
		err = autostart.Uninstall(env)
	}
	if err != nil {
		fmt.Fprintf(stderr, "autostart: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "autostart %sed\n", action)
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
	if cue == cycle.CueStop || cue == cycle.CueError || cue == cycle.CueCancelled {
		f.once.Do(func() { close(f.done) })
	}
}

// buildAdapters builds every adapter the runtime owns from a configuration.
// The initial launch and every live Apply use the same factory, so a hot
// change rebuilds exactly what a restart would.
func buildAdapters(cfg config.ClientConfig, logger *logging.Logger) (runtime.Adapters, error) {
	if level, err := logging.ParseLevel(cfg.LogLevel); err == nil {
		logger.SetLevel(level)
	}
	mode, err := hotkey.ParseMode(cfg.Trigger)
	if err != nil {
		return runtime.Adapters{}, fmt.Errorf("hotkey: %w", err)
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
		return runtime.Adapters{}, fmt.Errorf("hotkey: %w", err)
	}

	recorder, err := newRecorder(cfg, logger)
	if err != nil {
		return runtime.Adapters{}, fmt.Errorf("capture init failed: %w", err)
	}

	injector, err := inject.New(inject.Options{
		Paste:            cfg.Paste,
		PasteShortcut:    cfg.PasteShortcut,
		ClipboardRestore: cfg.ClipboardRestore,
		WaylandTool:      cfg.WaylandTool,
		OnLog:            func(message string) { logger.Debug("inject: %s", message) },
	})
	if err != nil {
		_ = recorder.Close()
		return runtime.Adapters{}, fmt.Errorf("injection init failed: %w", err)
	}

	return runtime.Adapters{
		Hotkey:      source,
		Recorder:    recorder,
		Transcriber: newTranscriber(cfg, logger),
		Injector:    injector,
		Spool:       newSpool(cfg, logger),
		Feedback:    newFeedback(cfg, logger),
		// The Recording indicator exists only for the interactive client;
		// record and transcribe-file stay quiet. The runtime owns its lifetime.
		Indicator: func(send func(cycle.Trigger)) cycle.Feedback {
			if ind := newIndicator(cfg, logger, recorder, send); ind != nil {
				return ind
			}
			return nil
		},
	}, nil
}

// newSettingsService builds the Dictation settings service the Settings window
// edits. A settings store that cannot be opened disables the screens but keeps
// dictation running.
func newSettingsService(rt *runtime.Runtime, so serveOptions, logger *logging.Logger) *dictation.Service {
	store, err := settings.Open(settings.Options{
		Path:         so.configPath,
		CLIOverrides: so.overrides,
	})
	if err != nil {
		logger.Warning("settings unavailable: %v", err)
		return nil
	}
	return dictation.New(dictation.Options{
		Store:   store,
		Runtime: rt,
		Devices: func() ([]mic.Device, error) {
			return mic.ListDevices(func(message string) { logger.Debug("audio: %s", message) })
		},
		Recorders: func(device string) (dictation.Recorder, error) {
			return mic.New(mic.Options{
				Device: device,
				OnLog:  func(message string) { logger.Debug("audio: %s", message) },
			})
		},
		OnLog: func(message string) { logger.Warning("settings: %s", message) },
	})
}

// newIndicator builds the Recording indicator for the interactive run command.
// It returns nil when the indicator is off, unavailable, or failed to start;
// the Client then keeps dictating silently. send feeds the indicator's stop
// button into the Dictation cycle.
func newIndicator(cfg config.ClientConfig, logger *logging.Logger, recorder *mic.Recorder, send func(cycle.Trigger)) *indicator.Indicator {
	if cfg.Feedback.Indicator == "off" {
		return nil
	}
	current := session.Current()
	presenter, err := indicator.Open(
		cfg.Feedback.Indicator,
		indicator.Environment{LayerShell: current.Wayland, X11: current.X11},
		indicator.PlatformFactories(),
	)
	if err != nil {
		logger.Warning("%v", err)
		return nil
	}
	if presenter == nil {
		return nil
	}
	logger.Info("recording indicator: %s backend", presenter.Name())
	return indicator.New(indicator.Options{
		Presenter: presenter,
		Level:     recorder,
		Clock:     nil,
		OnClick:   func() { send(cycle.Stop) },
		OnLog:     func(message string) { logger.Debug("%s", message) },
		OnWarning: func(message string) { logger.Warning("%s", message) },
	})
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
	case cycle.CueProcessing:
		f.log.Debug("dictation cycle: processing")
	case cycle.CueDelivering:
		f.log.Debug("dictation cycle: delivering")
	case cycle.CueStop:
		f.log.Debug("dictation cycle: finished")
	case cycle.CueError:
		f.log.Debug("dictation cycle: failed")
	case cycle.CueCancelled:
		f.log.Debug("dictation cycle: cancelled")
	case cycle.CueTooShort:
		f.log.Debug("dictation cycle: recording too short")
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

// newTranscriber builds the HTTP adapter for the active Server profile.
// config.Load always resolves exactly one active profile, so a missing match
// here is impossible and the zero profile would fail loudly in the request.
// Warnings from a successful response are routed to the Client log.
func newTranscriber(cfg config.ClientConfig, logger *logging.Logger) transcribe.Transcriber {
	profile, _ := cfg.ActiveProfile()
	client := transcribe.NewHTTP(profile.URL, cfg.Language, cfg.InitialPrompt)
	client.OnWarning = func(warning string) { logger.Warning("transcribe: %s", warning) }
	return client
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
  gui [--hidden]               run the desktop shell with the Settings window and Tray
  settings                     ask the running Client to open the Settings window
  toggle                       trigger the running client via its control socket
  transcribe-file <path.wav>   transcribe a WAV file and print the text
  record                       capture from the microphone and print the text
  install-autostart            register the client to launch at login
  uninstall-autostart          remove the login registration

Flags:
  --config PATH     path to kitsune.yaml
  --check-config    print the effective config and exit
  --device NAME     audio input device override
  --seconds N       record: capture length in seconds (default 5)
  --hidden          gui: start in the Tray without a window
  --verbose         shorthand for log_level=debug, also echoed to stderr
  --version         print the version and exit
`
}
