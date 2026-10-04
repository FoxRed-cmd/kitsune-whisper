// Package cli is the kitsune-client command-line entry point.
//
// It resolves and validates configuration, supports --check-config, and
// dispatches the one-shot transcribe-file command. The interactive daemon
// (hotkey, capture, injection) is wired by later tickets.
package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/audio"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
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
	fs.Usage = func() {
		fmt.Fprint(stderr, usage())
	}
	if err := fs.Parse(args); err != nil {
		return 2
	}
	rest := fs.Args()
	// Allow the curated global flags on either side of the subcommand, so
	// "kitsune-client transcribe-file --config x f.wav" works like the Server.
	if len(rest) > 0 && rest[0] == "transcribe-file" {
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
		fmt.Fprintln(stderr, "the interactive client is not implemented yet; use 'transcribe-file'")
		return 1
	}
	switch rest[0] {
	case "transcribe-file":
		return runTranscribeFile(rest[1:], cfg, stdout, stderr)
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

func usage() string {
	return `Usage: kitsune-client [flags] [command]

Commands:
  transcribe-file <path.wav>   transcribe a WAV file and print the text

Flags:
  --config PATH     path to kitsune.yaml
  --check-config    print the effective config and exit
  --device NAME     audio input device override
  --verbose         shorthand for log_level=debug
`
}
