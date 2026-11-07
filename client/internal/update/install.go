package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/components"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/paths"
)

// Result statuses persisted by the helper.
const (
	StatusPending = "pending"
	StatusSuccess = "success"
	StatusPartial = "partial"
	StatusError   = "error"
	StatusTimeout = "timeout"
)

// helperClientExitWait is how long the generated helper waits for the old
// Client to exit and release the single-instance lock before running the
// installer anyway.
const helperClientExitWait = 5 * time.Minute

// Result is the persisted outcome of one install. It is written as pending
// before the helper starts, overwritten with the final status by the helper,
// and kept until the user dismisses it.
type Result struct {
	Status           string `json:"status"`
	Tag              string `json:"tag"`
	ClientUpdated    bool   `json:"client_updated"`
	ServerUpdated    bool   `json:"server_updated"`
	ServerWasRunning bool   `json:"server_was_running"`
	TimedOut         bool   `json:"timed_out"`
	InstallerExit    int    `json:"installer_exit"`
	FinishedAt       string `json:"finished_at,omitempty"`
	LogPath          string `json:"log_path,omitempty"`
	Consumed         bool   `json:"consumed,omitempty"`
}

// InstallRequest is one install started from a staged set.
type InstallRequest struct {
	Stage            Stage
	Components       components.Set
	ServerWasRunning bool
	ClientPID        int
}

// Runner starts the generated helper. Production starts it without a terminal
// and appends its output to the log; tests record the call.
type Runner func(scriptPath, logPath string) error

// ResultPath is where the update result for configPath lives.
func ResultPath(configPath string) string { return paths.UpdateResult(configPath) }

// UpdateLogPath is the helper log for configPath.
//
//nolint:revive // The update namespace makes this name self-explanatory at call sites.
func UpdateLogPath(configPath string) string { return paths.UpdateLog(configPath) }

// Install writes the pending marker, generates a platform helper inside the
// stage, and starts it detached. The Client must exit after this succeeds; the
// helper waits for it, runs the installer from the stage, writes the result,
// and relaunches the installed Client.
func (s *Service) Install(ctx context.Context, req InstallRequest) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stage := req.Stage
	if !validTag(stage.Tag) {
		return errors.New("update: the staged release tag is invalid")
	}
	if stage.Dir == "" {
		return errors.New("update: the staged set has no directory")
	}
	installer := stage.InstallerPath()
	if installer == "" {
		return errors.New("update: the staged set has no installer")
	}
	if info, err := os.Stat(installer); err != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("update: the staged installer %s is missing", installer)
	}
	if s.opts.ClientPath == "" {
		return errors.New("update: the installed Client path is unknown")
	}

	set := req.Components
	if !set.Any() {
		set = stage.Components
	}
	set = effectiveComponents(set)

	pending := Result{
		Status:           StatusPending,
		Tag:              stage.Tag,
		ServerWasRunning: req.ServerWasRunning,
		LogPath:          s.opts.LogPath,
	}
	if err := writeResult(s.opts.ResultPath, pending); err != nil {
		return err
	}

	script := s.helperScript(stage, installer, set, req.ServerWasRunning, req.ClientPID)
	name := "kitsune-update-helper.sh"
	if s.opts.Platform == "windows" {
		name = "kitsune-update-helper.ps1"
	}
	helperPath := filepath.Join(stage.Dir, name)
	if err := os.WriteFile(helperPath, []byte(script), 0o700); err != nil {
		s.failResult(stage.Tag, req.ServerWasRunning)
		return fmt.Errorf("update: write the helper: %w", err)
	}
	if s.opts.Platform != "windows" {
		if err := os.Chmod(helperPath, 0o700); err != nil {
			s.failResult(stage.Tag, req.ServerWasRunning)
			return fmt.Errorf("update: make the helper executable: %w", err)
		}
	}

	start := s.opts.StartHelper
	if start == nil {
		start = startHelper
	}
	if err := start(helperPath, s.opts.LogPath); err != nil {
		s.failResult(stage.Tag, req.ServerWasRunning)
		return fmt.Errorf("update: start the helper: %w", err)
	}
	return nil
}

// failResult replaces the pending marker with a launch error so the next
// launch can report what happened. The marker is deliberately kept until the
// user reads it.
func (s *Service) failResult(tag string, serverWasRunning bool) {
	_ = writeResult(s.opts.ResultPath, Result{
		Status:           StatusError,
		Tag:              tag,
		ServerWasRunning: serverWasRunning,
		FinishedAt:       time.Now().UTC().Format(time.RFC3339),
		LogPath:          s.opts.LogPath,
	})
}

// Result returns the persisted result, if any. A missing or corrupt file is
// reported as absent so the Client starts normally.
func (s *Service) Result() (Result, bool) {
	return ReadResult(s.opts.ResultPath)
}

// ClearResult deletes the persisted result after the user dismisses it.
func (s *Service) ClearResult() error {
	err := os.Remove(s.opts.ResultPath)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

// ReadResult reads and decodes a result file. Missing or corrupt state is not
// an error: the next launch simply has nothing to show.
func ReadResult(path string) (Result, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Result{}, false
	}
	var result Result
	if err := json.Unmarshal(data, &result); err != nil {
		return Result{}, false
	}
	if result.Status == "" {
		return Result{}, false
	}
	return result, true
}

// ConsumeResult marks a result as read while preserving every other field. A
// later ordinary launch then follows the normal Local Server autostart rules
// even if the user dismissed no message yet.
func ConsumeResult(path string) error {
	result, ok := ReadResult(path)
	if !ok {
		return nil
	}
	result.Consumed = true
	return writeResult(path, result)
}

// writeResult persists result atomically with a 0600 file in a 0700 directory.
func writeResult(path string, result Result) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("update: create %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("update: encode the update result: %w", err)
	}
	data = append(data, '\n')
	temp, err := os.CreateTemp(dir, ".update-*.tmp")
	if err != nil {
		return fmt.Errorf("update: create the update result: %w", err)
	}
	name := temp.Name()
	defer func() {
		_ = temp.Close()
		_ = os.Remove(name)
	}()
	if err := temp.Chmod(0o600); err != nil {
		return fmt.Errorf("update: secure the update result: %w", err)
	}
	if _, err := temp.Write(data); err != nil {
		return fmt.Errorf("update: write the update result: %w", err)
	}
	if err := temp.Sync(); err != nil {
		return fmt.Errorf("update: sync the update result: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("update: close the update result: %w", err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("update: publish the update result: %w", err)
	}
	return nil
}

// helperScript renders the platform helper. It is a plain script, never the
// Client binary: the Windows installer force-stops kitsune-client.
func (s *Service) helperScript(
	stage Stage,
	installer string,
	set components.Set,
	serverWasRunning bool,
	clientPID int,
) string {
	timeout := int(s.opts.HelperTimeout / time.Second)
	if timeout <= 0 {
		timeout = 1
	}
	data := helperData{
		Stage:                 stage.Dir,
		Installer:             installer,
		Tag:                   stage.Tag,
		LogPath:               s.opts.LogPath,
		ResultPath:            s.opts.ResultPath,
		ClientPath:            s.opts.ClientPath,
		ConfigPath:            s.opts.ConfigPath,
		ClientOnly:            !set.Server,
		ServerSelected:        set.Server,
		ServerWasRunning:      serverWasRunning,
		ClientPID:             clientPID,
		TimeoutSeconds:        timeout,
		ClientExitWaitSeconds: int(helperClientExitWait / time.Second),
	}
	if s.opts.Platform == "windows" {
		return windowsHelper(data)
	}
	return unixHelper(data)
}

// helperData is every value embedded in the generated script.
type helperData struct {
	Stage                 string
	Installer             string
	Tag                   string
	LogPath               string
	ResultPath            string
	ClientPath            string
	ConfigPath            string
	ClientOnly            bool
	ServerSelected        bool
	ServerWasRunning      bool
	ClientPID             int
	TimeoutSeconds        int
	ClientExitWaitSeconds int
}

// jsonLiteral renders a Go string as a quoted JSON string, so a path with
// quotes or backslashes cannot corrupt the helper's result file.
func jsonLiteral(value string) string {
	data, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(data)
}

// shellQuote single-quotes one value for a POSIX shell.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// powershellQuote single-quotes one value for PowerShell.
func powershellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// openHelperLog opens the helper log for append, creating its directory.
func openHelperLog(path string) (*os.File, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("update: create the log directory: %w", err)
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
}
