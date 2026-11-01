package shell

import (
	"context"
	"strings"
	"time"

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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s.updateServer(s.probeActiveServer(ctx))
	var failures []string
	if _, err := s.opts.Servers.LoadConfig(ctx, profileID); err != nil {
		failures = append(failures, s.catalog().T("error.server.configRead", err.Error()))
	}
	if _, err := s.opts.Servers.LoadLogs(ctx, profileID, 200); err != nil {
		failures = append(failures, s.catalog().T("error.server.logs", err.Error()))
	}
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
	var err error
	switch action {
	case actionServerRestart:
		_, err = s.opts.Servers.Restart(ctx, profileID)
	case actionServerStop:
		_, err = s.opts.Servers.Stop(ctx, profileID)
	case actionServerStart:
		_, err = s.opts.Servers.Start(ctx, profileID)
	}
	if err != nil {
		s.pushSettings()
		key := "error.server." + action
		s.pushMessages(nil, []string{s.catalog().T(key, err.Error())})
		return
	}
	s.pushSettings()
	s.update(s.opts.Runtime.Status())
	switch action {
	case actionServerRestart:
		s.pushMessages([]string{s.catalog().T("notice.server.restarting", name)}, nil)
		go s.awaitServer()
	case actionServerStop:
		s.pushMessages([]string{s.catalog().T("notice.server.stopped", name)}, nil)
	case actionServerStart:
		s.pushMessages([]string{s.catalog().T("notice.server.started", name)}, nil)
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
