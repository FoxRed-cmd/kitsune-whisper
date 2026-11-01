package servers

import (
	"context"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

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

// Restart asks the Server for a supervised self-exit. The Server answers 202
// before it exits; the shell polls its health until it is ready again.
func (s *Service) Restart(ctx context.Context, profileID string) (ServerLifecycle, error) {
	return s.runLifecycle(ctx, profileID, "restart")
}

// Stop soft-stops the Server: the model is unloaded, transcription answers
// 503, and the Admin API stays reachable.
func (s *Service) Stop(ctx context.Context, profileID string) (ServerLifecycle, error) {
	return s.runLifecycle(ctx, profileID, "stop")
}

// Start clears a soft-stop and loads the model again.
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

// recordLifecycle folds a just-completed stop/start/restart into the cached
// health of one profile. The request reached the Server, so a profile without
// a health result is online; its model state is now what the reply reported.
// Without this, the profile list would keep calling a soft-stopped Server
// "Online" until the next background probe.
func (s *Service) recordLifecycle(profileID string, result ServerLifecycle) {
	s.mu.Lock()
	defer s.mu.Unlock()
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
