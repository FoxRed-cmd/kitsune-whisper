package shell

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/logging"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/servers"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// Internal Server lifecycle actions shared by the Tray and the Settings page.
const (
	actionServerRestart = "restart"
	actionServerStop    = "stop"
	actionServerStart   = "start"
)

// activeServerID returns the profile every Server-management call targets.
func (s *Shell) activeServerID() string {
	if s.opts.Servers == nil {
		return ""
	}
	return s.opts.Servers.View().ActiveID
}

// saveServerSettings validates and writes one Server configuration patch
// through the Admin API. A rejected patch leaves the Server file untouched and
// the Server's message is shown verbatim.
func (s *Shell) saveServerSettings(patch map[string]any) {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	profileID := s.activeServerID()
	if profileID == "" {
		s.pushMessages(nil, []string{s.catalog().T("error.server.noProfile")})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	result, err := s.opts.Servers.PatchConfig(ctx, profileID, patch)
	if err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T("error.server.config", err.Error())})
		return
	}
	if result.Written {
		// The patch response already refreshed the configuration cache. Pull
		// the log tail and the live status as well, so Diagnostics shows the
		// effect of the save without the user pressing Refresh.
		probeCtx, probeCancel := context.WithTimeout(context.Background(), 10*time.Second)
		s.updateServer(s.probeActiveServer(probeCtx))
		probeCancel()
		logCtx, logCancel := context.WithTimeout(context.Background(), 15*time.Second)
		if _, err := s.opts.Servers.LoadLogs(logCtx, profileID, 200); err != nil {
			s.log.Debug("diagnostics: logs after save: %v", err)
		}
		logCancel()
	}
	s.pushSettings()
	if !result.Written {
		s.pushMessages([]string{s.catalog().T("notice.noChanges")}, nil)
		return
	}
	if len(result.RestartRequired) > 0 {
		s.pushMessages([]string{
			s.catalog().T("notice.server.savedRestart", strings.Join(result.RestartRequired, ", ")),
		}, nil)
		return
	}
	s.pushMessages([]string{s.catalog().T("notice.server.saved")}, nil)
}

// loadDiagnostics reads the redacted configuration and the log tail into the
// Servers cache. It returns translated failures for a caller that shows them;
// automatic refreshes log them instead.
func (s *Shell) loadDiagnostics(profileID string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var failures []string
	if _, err := s.opts.Servers.LoadConfig(ctx, profileID); err != nil {
		failures = append(failures, s.catalog().T("error.server.configRead", err.Error()))
	}
	if _, err := s.opts.Servers.LoadLogs(ctx, profileID, 200); err != nil {
		failures = append(failures, s.catalog().T("error.server.logs", err.Error()))
	}
	return failures
}

// primeDiagnostics fills the Diagnostics cache when the Settings window opens
// or the page loads. It never shows a success notice: the first visit to the
// screen should simply have data. A second request while one is running is
// ignored, so opening the window during startup does not fetch twice.
func (s *Shell) primeDiagnostics() {
	if s.opts.Servers == nil {
		return
	}
	profileID := s.activeServerID()
	if profileID == "" {
		return
	}
	s.mu.Lock()
	if s.diagnosticsLoading {
		s.mu.Unlock()
		return
	}
	s.diagnosticsLoading = true
	s.mu.Unlock()
	go func() {
		s.updateServer(s.probeActiveServer(context.Background()))
		failures := s.loadDiagnostics(profileID)
		s.mu.Lock()
		s.diagnosticsLoading = false
		s.mu.Unlock()
		for _, failure := range failures {
			s.log.Debug("diagnostics: %s", failure)
		}
		s.pushSettings()
	}()
}

// refreshDiagnostics loads the redacted effective configuration and the log
// tail for the active profile.
func (s *Shell) refreshDiagnostics() {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	profileID := s.activeServerID()
	if profileID == "" {
		s.pushMessages(nil, []string{s.catalog().T("error.server.noProfile")})
		return
	}
	s.updateServer(s.probeActiveServer(context.Background()))
	failures := s.loadDiagnostics(profileID)
	s.pushSettings()
	if len(failures) > 0 {
		s.pushMessages(nil, failures)
		return
	}
	s.pushMessages([]string{s.catalog().T("notice.server.diagnostics")}, nil)
}

// loadServerLogs refreshes only the log tail.
func (s *Shell) loadServerLogs(tail int) {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	profileID := s.activeServerID()
	if profileID == "" {
		s.pushMessages(nil, []string{s.catalog().T("error.server.noProfile")})
		return
	}
	if tail <= 0 {
		tail = 200
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if _, err := s.opts.Servers.LoadLogs(ctx, profileID, tail); err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T("error.server.logs", err.Error())})
		return
	}
	s.pushSettings()
	s.pushMessages([]string{s.catalog().T("notice.server.logs")}, nil)
}

// runServerAction performs one lifecycle action for the active profile. It is
// the single path shared by the Tray items and the Settings buttons.
func (s *Shell) runServerAction(action string) {
	if s.opts.Servers == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.server.unavailable")})
		return
	}
	profileID := s.activeServerID()
	if profileID == "" {
		s.pushMessages(nil, []string{s.catalog().T("error.server.noProfile")})
		return
	}
	name := s.activeProfileName()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var lifecycle servers.ServerLifecycle
	var err error
	switch action {
	case actionServerRestart:
		lifecycle, err = s.opts.Servers.Restart(ctx, profileID)
	case actionServerStop:
		lifecycle, err = s.opts.Servers.Stop(ctx, profileID)
	case actionServerStart:
		lifecycle, err = s.opts.Servers.Start(ctx, profileID)
	}
	if err != nil {
		s.pushSettings()
		key := "error.server." + action
		s.pushMessages(nil, []string{s.catalog().T(key, err.Error())})
		return
	}
	// The lifecycle reply is newer than the last health probe. Applying it now
	// keeps the status bar and the Servers row from saying "Online" for up to a
	// poll interval after a soft stop, when transcription already answers 503.
	s.applyLifecycle(lifecycle)
	s.pushSettings()
	s.update(s.opts.Runtime.Status())
	switch action {
	case actionServerRestart:
		s.pushMessages([]string{s.catalog().T("notice.server.restarting", name)}, nil)
		go s.awaitServer()
	case actionServerStop:
		key := "notice.server.stopped"
		if lifecycle.Hard {
			key = "notice.server.localStopped"
		}
		s.pushMessages([]string{s.catalog().T(key, name)}, nil)
	case actionServerStart:
		key := "notice.server.started"
		wait := false
		if lifecycle.Hard && lifecycle.ModelState == "loading" {
			key = "notice.server.localStarting"
			wait = true
		}
		s.pushMessages([]string{s.catalog().T(key, name)}, nil)
		if wait {
			go s.awaitServer()
		}
	}
}

// awaitServer polls the active Server after a restart request until it is
// ready again, bounded so a Server the supervisor cannot restart does not
// leave a goroutine probing forever.
func (s *Shell) awaitServer() {
	interval := s.opts.RestartPollInterval
	if interval <= 0 {
		interval = 500 * time.Millisecond
	}
	attempts := s.opts.RestartPollAttempts
	if attempts <= 0 {
		attempts = 20
	}
	name := s.activeProfileName()
	for attempt := 0; attempt < attempts; attempt++ {
		status := s.probeActiveServer(context.Background())
		s.updateServer(status)
		if status.State == trust.StateOnline {
			s.pushMessages([]string{s.catalog().T("notice.server.ready", name)}, nil)
			return
		}
		if attempt < attempts-1 {
			time.Sleep(interval)
		}
	}
	s.pushMessages(nil, []string{s.catalog().T("error.server.notReady", name)})
}

// applyLifecycle folds a successful stop/start/restart reply into the live
// status. The Server answered the Admin API, so it is reachable; the model
// fields are authoritative for the label and the Tray's Start/Stop item.
func (s *Shell) applyLifecycle(lifecycle servers.ServerLifecycle) {
	s.mu.Lock()
	status := s.server
	s.mu.Unlock()
	if lifecycle.Hard && lifecycle.Stopped {
		// The Local Server process is gone, not soft-stopped: the Tray must
		// show it offline so Start is the action that helps.
		s.updateServer(trust.Status{
			State:      trust.StateOffline,
			Stopped:    true,
			ModelState: "stopped",
		})
		return
	}
	if lifecycle.Hard && lifecycle.ModelState == "loading" {
		status.State = trust.StateOffline
		status.Stopped = false
		status.Loaded = false
		status.ModelState = "loading"
		s.updateServer(status)
		return
	}
	status.State = trust.StateOnline
	status.Stopped = lifecycle.Stopped
	status.Loaded = lifecycle.Loaded
	if lifecycle.Model != "" {
		status.Model = lifecycle.Model
	}
	if lifecycle.ModelState != "" {
		status.ModelState = lifecycle.ModelState
	}
	if lifecycle.ModelPath != "" {
		status.ModelPath = lifecycle.ModelPath
	}
	if lifecycle.IdleUnloadSeconds != 0 {
		status.IdleUnloadSeconds = lifecycle.IdleUnloadSeconds
	}
	s.updateServer(status)
}

// setStartup enables or disables a login autostart registration through the
// platform controller. The switch is re-rendered from the real state afterwards,
// so a unit or task a desktop refuses to change does not leave a lying toggle.
func (s *Shell) setStartup(component string, enabled bool) {
	if s.opts.Startup == nil {
		s.pushMessages(nil, []string{s.catalog().T("error.startup.unavailable")})
		return
	}
	var err error
	switch component {
	case "client":
		err = s.opts.Startup.SetClient(enabled)
	case "server":
		err = s.opts.Startup.SetServer(enabled)
	default:
		err = errors.New("unknown startup component")
	}
	if err != nil {
		s.pushSettings()
		s.pushMessages(nil, []string{s.catalog().T("error.startup", err.Error())})
		return
	}
	s.pushSettings()
	label := s.catalog().T("startup.client")
	if component == "server" {
		label = s.catalog().T("startup.server")
	}
	key := "notice.startup.off"
	if enabled {
		key = "notice.startup.on"
	}
	s.pushMessages([]string{s.catalog().T(key, label)}, nil)
}

// setLogLevel writes one level to both sides: client.log_level for this
// Client and server.log_level through the Admin API. If the Server is
// unreachable the Client change still sticks and the failure is shown.
func (s *Shell) setLogLevel(value string) {
	value = strings.ToLower(strings.TrimSpace(value))
	if _, err := logging.ParseLevel(value); err != nil {
		s.pushMessages(nil, []string{s.catalog().T("error.logLevel", err.Error())})
		return
	}
	var failures []string
	if s.opts.Settings != nil {
		outcome, err := s.opts.Settings.Save(map[string]any{"log_level": value})
		if err != nil {
			failures = append(failures, s.catalog().T("error.generic", err.Error()))
		}
		failures = append(failures, outcome.Errors...)
	}
	if s.opts.Servers != nil {
		profileID := s.activeServerID()
		if profileID == "" {
			failures = append(failures, s.catalog().T("error.server.noProfile"))
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			if _, err := s.opts.Servers.PatchConfig(ctx, profileID, map[string]any{"log_level": value}); err != nil {
				failures = append(failures, s.catalog().T("error.server.config", err.Error()))
			}
			cancel()
		}
	}
	s.pushSettings()
	if len(failures) > 0 {
		s.pushMessages(nil, failures)
		return
	}
	s.pushMessages([]string{s.catalog().T("notice.logLevel", value)}, nil)
}
