package servers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
)

// Model is one entry in the Model catalog as the Server reports it.
type Model struct {
	Name           string `json:"name"`
	DiskBytes      int64  `json:"disk_bytes"`
	MemoryBytes    int64  `json:"memory_bytes"`
	Speed          string `json:"speed"`
	Quality        string `json:"quality"`
	Recommended    bool   `json:"recommended"`
	Installed      bool   `json:"installed"`
	InstalledBytes int64  `json:"installed_bytes"`
	// Download is the progress of this model's download, or nil.
	Download *ModelDownload `json:"download"`
}

// ModelDownload is one download's progress and outcome.
type ModelDownload struct {
	Model         string `json:"model"`
	State         string `json:"state"`
	ReceivedBytes int64  `json:"received_bytes"`
	TotalBytes    int64  `json:"total_bytes"`
	Error         string `json:"error"`
}

// ModelsView is the Server's Model catalog and lifecycle state.
type ModelsView struct {
	Device            string  `json:"device"`
	Recommended       string  `json:"recommended"`
	State             string  `json:"state"`
	ActiveModel       string  `json:"active_model"`
	ActivePath        string  `json:"active_path"`
	Error             string  `json:"error"`
	IdleUnloadSeconds int     `json:"idle_unload_seconds"`
	Models            []Model `json:"models"`
	// Warning reports a change that applied but could not be persisted.
	Warning string `json:"warning,omitempty"`
}

// LoadModels reads the active or named Server's Model catalog.
func (s *Service) LoadModels(ctx context.Context, profileID string) (ModelsView, error) {
	profile, token, err := s.credentials(profileID)
	if err != nil {
		s.cacheModels(profileID, nil, err)
		return ModelsView{}, err
	}
	view, err := fetchModels(ctx, profile, token, s.opts.Timeout)
	if err != nil {
		s.cacheModels(profileID, nil, err)
		return ModelsView{}, err
	}
	s.cacheModels(profileID, &view, nil)
	return view, nil
}

// DownloadModel asks the Server to queue one catalog download.
func (s *Service) DownloadModel(ctx context.Context, profileID, model string) (ModelsView, error) {
	return s.modelAction(ctx, profileID, "/admin/models/download", map[string]any{
		"model": model,
	})
}

// CancelDownload cancels a queued or running download.
func (s *Service) CancelDownload(ctx context.Context, profileID, model string) (ModelsView, error) {
	return s.modelAction(ctx, profileID, "/admin/models/cancel", map[string]any{
		"model": model,
	})
}

// ActivateModel loads a downloaded catalog model or a local CTranslate2
// directory and swaps it in without restarting the Server.
func (s *Service) ActivateModel(
	ctx context.Context, profileID, model, path string,
) (ModelsView, error) {
	body := map[string]any{}
	if path != "" {
		body["path"] = path
	} else {
		body["model"] = model
	}
	return s.modelAction(ctx, profileID, "/admin/models/activate", body)
}

// SetIdleTimeout sets the idle unload timeout; 0 keeps the model resident.
func (s *Service) SetIdleTimeout(
	ctx context.Context, profileID string, seconds int,
) (ModelsView, error) {
	return s.modelAction(ctx, profileID, "/admin/models/idle-unload", map[string]any{
		"seconds": seconds,
	})
}

// DeleteModel removes an unused downloaded model and refreshes the catalog.
func (s *Service) DeleteModel(ctx context.Context, profileID, model string) error {
	profile, token, err := s.credentials(profileID)
	if err != nil {
		s.cacheModels(profileID, nil, err)
		return err
	}
	path := "/admin/models/" + url.PathEscape(model)
	if _, err := adminRequest(ctx, profile, token, http.MethodDelete, path, nil, s.opts.Timeout); err != nil {
		s.cacheModels(profileID, nil, err)
		return err
	}
	_, err = s.LoadModels(ctx, profileID)
	return err
}

func (s *Service) modelAction(
	ctx context.Context,
	profileID, path string,
	body map[string]any,
) (ModelsView, error) {
	profile, token, err := s.credentials(profileID)
	if err != nil {
		s.cacheModels(profileID, nil, err)
		return ModelsView{}, err
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return ModelsView{}, fmt.Errorf("servers: build the model request: %w", err)
	}
	data, err := adminRequest(ctx, profile, token, http.MethodPost, path, encoded, s.opts.Timeout)
	if err != nil {
		s.cacheModels(profileID, nil, err)
		return ModelsView{}, err
	}
	view, err := decodeModels(data)
	if err != nil {
		s.cacheModels(profileID, nil, err)
		return ModelsView{}, err
	}
	s.cacheModels(profileID, &view, nil)
	return view, nil
}

func (s *Service) cacheModels(profileID string, view *ModelsView, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.modelsFor = profileID
	s.models = view
	s.modelsError = ""
	if err != nil {
		s.modelsError = err.Error()
	}
}

func fetchModels(
	ctx context.Context,
	profile config.ServerProfile,
	token string,
	timeout time.Duration,
) (ModelsView, error) {
	data, err := adminRequest(ctx, profile, token, http.MethodGet, "/admin/models", nil, timeout)
	if err != nil {
		return ModelsView{}, err
	}
	return decodeModels(data)
}

func decodeModels(data []byte) (ModelsView, error) {
	var view ModelsView
	if err := json.Unmarshal(data, &view); err != nil {
		return ModelsView{}, fmt.Errorf("servers: decode the Model catalog: %w", err)
	}
	return view, nil
}
