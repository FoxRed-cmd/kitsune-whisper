package servers

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// ErrLocalUnavailable reports that the Local Server is not supervised on this
// machine, so the Client cannot start it again after a hard stop.
var ErrLocalUnavailable = errors.New(
	"servers: the Local Server is not supervised on this machine; " +
		"install the Server bundle or start it manually",
)

// LocalServer starts and stops the native Local Server's process through the OS
// supervisor. *localserver.Controller implements it.
type LocalServer interface {
	// Available reports whether the Local Server is registered with the OS
	// supervisor on this machine.
	Available() bool
	Stop() error
	Start() error
	Restart() error
}

// ConfigField is one Server setting as the Admin API reports it: the effective
// value, where it came from, whether an environment variable or command-line
// flag locks it, and whether a change applies hot or needs a restart.
type ConfigField struct {
	Value  any    `json:"value"`
	Source string `json:"source"`
	Locked bool   `json:"locked"`
	Reason string `json:"reason,omitempty"`
	Mode   string `json:"mode"`
}

// ServerConfig is the redacted effective configuration of one Server. Secrets
// (today the processing API key) are replaced by the Server with "***" and
// listed in Redacted, so this value can be rendered and copied.
type ServerConfig struct {
	Path     string                 `json:"path"`
	Config   map[string]any         `json:"config"`
	Fields   map[string]ConfigField `json:"fields"`
	Redacted []string               `json:"redacted"`
}

// ServerConfigPatch is the result of one validated configuration patch.
type ServerConfigPatch struct {
	ServerConfig
	Changed         []string `json:"changed"`
	HotApplied      []string `json:"hot_applied"`
	RestartRequired []string `json:"restart_required"`
	Written         bool     `json:"written"`
}

// ServerLog is the tail of a Server's log file.
type ServerLog struct {
	Path      string   `json:"path"`
	Lines     []string `json:"lines"`
	Truncated bool     `json:"truncated"`
	Tail      int      `json:"tail"`
}

// ServerLifecycle is a Server's model state after a start, stop, or restart.
// It mirrors the flat model keys of GET /status, so the Client can consume one
// payload from either source.
type ServerLifecycle struct {
	Stopped           bool   `json:"stopped"`
	Loaded            bool   `json:"loaded"`
	Model             string `json:"model"`
	ModelPath         string `json:"model_path,omitempty"`
	ModelState        string `json:"model_state"`
	IdleUnloadSeconds int    `json:"idle_unload_seconds,omitempty"`
	Restarting        bool   `json:"restarting,omitempty"`
	// Hard marks an action that ran against the Local Server's process: it
	// terminated, replaced, or started the daemon instead of soft-stopping it.
	Hard bool `json:"hard,omitempty"`
}

// LoadConfig reads the active profile's effective Server configuration. The
// Server masks secrets before sending it.
func (s *Service) LoadConfig(ctx context.Context, profileID string) (ServerConfig, error) {
	profile, token, err := s.credentials(profileID)
	if err != nil {
		s.cacheConfig(profileID, nil, err)
		return ServerConfig{}, err
	}
	config, err := fetchServerConfig(ctx, profile, token, s.opts.Timeout)
	if err != nil {
		s.cacheConfig(profileID, nil, err)
		return ServerConfig{}, err
	}
	s.cacheConfig(profileID, &config, nil)
	return config, nil
}

// PatchConfig validates and writes a partial Server configuration. A rejected
// patch leaves the cached configuration untouched and is reported in the view.
func (s *Service) PatchConfig(
	ctx context.Context, profileID string, patch map[string]any,
) (ServerConfigPatch, error) {
	profile, token, err := s.credentials(profileID)
	if err != nil {
		s.cacheConfigError(profileID, err)
		return ServerConfigPatch{}, err
	}
	result, err := patchServerConfig(ctx, profile, token, patch, s.opts.Timeout)
	if err != nil {
		s.cacheConfigError(profileID, err)
		return ServerConfigPatch{}, err
	}
	config := result.ServerConfig
	s.cacheConfig(profileID, &config, nil)
	return result, nil
}

// LoadLogs reads the last tail lines of the active profile's Server log.
func (s *Service) LoadLogs(ctx context.Context, profileID string, tail int) (ServerLog, error) {
	profile, token, err := s.credentials(profileID)
	if err != nil {
		s.cacheLogs(profileID, nil, err)
		return ServerLog{}, err
	}
	logs, err := fetchServerLogs(ctx, profile, token, tail, s.opts.Timeout)
	if err != nil {
		s.cacheLogs(profileID, nil, err)
		return ServerLog{}, err
	}
	s.cacheLogs(profileID, &logs, nil)
	return logs, nil
}

// Restart replaces the active Server. A Local Server process is restarted
// through the OS supervisor; a Remote Server answers 202 and exits for its own
// supervisor to bring it back. The shell polls health until it is ready.
func (s *Service) Restart(ctx context.Context, profileID string) (ServerLifecycle, error) {
	return s.runLifecycle(ctx, profileID, "restart")
}

// Stop stops the active Server. A Local Server is hard-stopped: the process
// exits and the OS supervisor leaves it stopped. A Remote Server is soft-stopped:
// the model is unloaded, transcription answers 503, and the Admin API stays
// reachable.
func (s *Service) Stop(ctx context.Context, profileID string) (ServerLifecycle, error) {
	return s.runLifecycle(ctx, profileID, "stop")
}

// Start starts the active Server. A Local Server process is started through the
// OS supervisor; a Remote Server clears its soft-stop and loads the model.
func (s *Service) Start(ctx context.Context, profileID string) (ServerLifecycle, error) {
	return s.runLifecycle(ctx, profileID, "start")
}

func (s *Service) runLifecycle(
	ctx context.Context, profileID, action string,
) (ServerLifecycle, error) {
	profile, token, err := s.credentials(profileID)
	if err != nil {
		s.cacheLifecycle(profileID, nil, err)
		return ServerLifecycle{}, err
	}
	if isLocalServer(profile) && s.opts.LocalServer != nil {
		result, err := s.runLocalLifecycle(ctx, profile, token, action)
		if err != nil {
			s.cacheLifecycle(profileID, nil, err)
			return ServerLifecycle{}, err
		}
		s.cacheLifecycle(profileID, &result, nil)
		s.recordLifecycle(profileID, result)
		return result, nil
	}
	result, err := serverLifecycle(ctx, profile, token, action, s.opts.Timeout)
	if err != nil {
		s.cacheLifecycle(profileID, nil, err)
		return ServerLifecycle{}, err
	}
	if action == "restart" && result.ModelState == "" {
		// An older Server returns only {"restarting": true}. Keep the last
		// known model state instead of blanking the Tray with an empty reply.
		s.mu.Lock()
		if s.lifecycleFor == profileID && s.lifecycle != nil {
			merged := *s.lifecycle
			merged.Restarting = true
			result = merged
		}
		s.mu.Unlock()
	}
	s.cacheLifecycle(profileID, &result, nil)
	s.recordLifecycle(profileID, result)
	return result, nil
}

// runLocalLifecycle performs a hard stop, start, or restart of the Local
// Server. A reachable process answers the hard Admin endpoints so it can clear
// a persisted soft-stop before exiting; a process that is already down is
// driven through its OS supervisor. A Remote Server never reaches this path.
func (s *Service) runLocalLifecycle(
	ctx context.Context,
	profile config.ServerProfile,
	token, action string,
) (ServerLifecycle, error) {
	local := s.opts.LocalServer
	online := s.opts.Check(ctx, profile, token, s.opts.Timeout).State == trust.StateOnline
	switch action {
	case "stop":
		if online {
			if _, err := serverLifecycle(ctx, profile, token, "hard-stop", s.opts.Timeout); err == nil {
				return ServerLifecycle{Hard: true, Stopped: true, ModelState: "stopped"}, nil
			} else if !canUseSupervisorFallback(err) || !local.Available() {
				return ServerLifecycle{}, err
			}
		}
		if !local.Available() {
			return ServerLifecycle{}, ErrLocalUnavailable
		}
		if err := local.Stop(); err != nil {
			return ServerLifecycle{}, err
		}
		return ServerLifecycle{Hard: true, Stopped: true, ModelState: "stopped"}, nil
	case "start":
		if online {
			result, err := serverLifecycle(ctx, profile, token, "start", s.opts.Timeout)
			if err != nil {
				return ServerLifecycle{}, err
			}
			return result, nil
		}
		if !local.Available() {
			return ServerLifecycle{}, ErrLocalUnavailable
		}
		if err := local.Start(); err != nil {
			return ServerLifecycle{}, err
		}
		return ServerLifecycle{Hard: true, ModelState: "loading"}, nil
	case "restart":
		// Unlike stop, a restart must be able to bring the process back. Ask
		// for the supervisor before killing a running Server.
		if !local.Available() {
			return ServerLifecycle{}, ErrLocalUnavailable
		}
		if online {
			if result, err := serverLifecycle(ctx, profile, token, "hard-restart", s.opts.Timeout); err == nil {
				result.Hard = true
				return result, nil
			} else if !canUseSupervisorFallback(err) {
				return ServerLifecycle{}, err
			}
		}
		if err := local.Restart(); err != nil {
			return ServerLifecycle{}, err
		}
		return ServerLifecycle{Hard: true, Restarting: true, ModelState: "loading"}, nil
	default:
		return ServerLifecycle{}, fmt.Errorf("servers: unknown lifecycle action %q", action)
	}
}

// canUseSupervisorFallback reports whether a failed hard Admin action may be
// replaced by the local supervisor. A Server that predates the endpoint (404)
// or a connection that dropped is safe to replace; an authentication or Server
// error must be shown instead of killing or restarting the daemon anyway.
func canUseSupervisorFallback(err error) bool {
	var adminErr *AdminError
	if !errors.As(err, &adminErr) {
		return true
	}
	return adminErr.StatusCode == http.StatusNotFound
}

// isLocalServer reports whether a profile points at the Local Server on this
// machine. AutoPair identifies the profile the Client created for this host;
// the loopback check keeps a repointed profile from driving the local unit.
func isLocalServer(profile config.ServerProfile) bool {
	if !trust.IsLoopbackURL(profile.URL) {
		return false
	}
	return profile.AutoPair || profile.ID == config.LocalServerID
}

// recordLifecycle folds a just-completed stop/start/restart into the cached
// health of one profile. A Remote Server request reached the Server, so a
// profile without a health result is online; its model state is now what the
// reply reported. A hard Local Server action that left the process down is
// reported offline instead. Without this, the profile list would keep calling a
// soft-stopped Server "Online" until the next background probe.
func (s *Service) recordLifecycle(profileID string, result ServerLifecycle) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if result.Hard && result.Stopped {
		// A hard-stopped process answers no Admin API; "online but stopped"
		// would misreport the daemon as reachable.
		s.health[profileID] = trust.Status{
			State:      trust.StateOffline,
			Stopped:    true,
			ModelState: "stopped",
		}
		return
	}
	if result.Hard && result.ModelState == "loading" {
		s.health[profileID] = trust.Status{
			State:      trust.StateOffline,
			ModelState: "loading",
		}
		return
	}
	status, known := s.health[profileID]
	if known && status.State != trust.StateOnline {
		return
	}
	status.State = trust.StateOnline
	status.Stopped = result.Stopped
	status.Loaded = result.Loaded
	if result.Model != "" {
		status.Model = result.Model
	}
	if result.ModelState != "" {
		status.ModelState = result.ModelState
	}
	if result.ModelPath != "" {
		status.ModelPath = result.ModelPath
	}
	if result.IdleUnloadSeconds != 0 {
		status.IdleUnloadSeconds = result.IdleUnloadSeconds
	}
	s.health[profileID] = status
}

func (s *Service) cacheConfig(profileID string, config *ServerConfig, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configFor = profileID
	s.config = config
	s.configError = ""
	if err != nil {
		s.configError = err.Error()
	}
}

func (s *Service) cacheConfigError(profileID string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.configFor = profileID
	s.configError = err.Error()
}

func (s *Service) cacheLogs(profileID string, logs *ServerLog, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logsFor = profileID
	s.logs = logs
	s.logsError = ""
	if err != nil {
		s.logsError = err.Error()
	}
}

func (s *Service) cacheLifecycle(profileID string, result *ServerLifecycle, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lifecycleFor = profileID
	s.lifecycle = result
	s.lifecycleError = ""
	if err != nil {
		s.lifecycleError = err.Error()
	}
}
