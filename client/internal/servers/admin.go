package servers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// AdminError is a refusal from the Server's Admin API. Code is stable, so the
// Settings window can distinguish a revoked Device from a Server that is down.
type AdminError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *AdminError) Error() string {
	if e.StatusCode == http.StatusUnauthorized {
		return "this Device was revoked or its token is no longer accepted; pair this Server profile again"
	}
	if e.Code == "" {
		return fmt.Sprintf("the Server returned HTTP %d: %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("the Server returned HTTP %d %s: %s", e.StatusCode, e.Code, e.Message)
}

func fetchDevices(
	ctx context.Context,
	profile config.ServerProfile,
	token string,
	timeout time.Duration,
) ([]Device, error) {
	data, err := adminRequest(ctx, profile, token, http.MethodGet, "/admin/devices", nil, timeout)
	if err != nil {
		return nil, err
	}
	var body struct {
		Devices []Device `json:"devices"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		return nil, fmt.Errorf("servers: decode the Device list: %w", err)
	}
	return sortDevices(body.Devices), nil
}

func revokeDevice(
	ctx context.Context,
	profile config.ServerProfile,
	token, deviceID string,
	timeout time.Duration,
) error {
	path := "/admin/devices/" + url.PathEscape(deviceID)
	_, err := adminRequest(ctx, profile, token, http.MethodDelete, path, nil, timeout)
	return err
}

func fetchPairingCode(
	ctx context.Context,
	profile config.ServerProfile,
	token string,
	timeout time.Duration,
) (PairingCode, error) {
	data, err := adminRequest(
		ctx, profile, token, http.MethodPost, "/admin/pairing-code", nil, timeout,
	)
	if err != nil {
		return PairingCode{}, err
	}
	var code PairingCode
	if err := json.Unmarshal(data, &code); err != nil {
		return PairingCode{}, fmt.Errorf("servers: decode the Pairing code: %w", err)
	}
	if code.Code == "" {
		return PairingCode{}, fmt.Errorf("servers: the Server returned an empty Pairing code")
	}
	return code, nil
}

func setExposure(
	ctx context.Context,
	profile config.ServerProfile,
	token string,
	expose bool,
	timeout time.Duration,
) (ExposureResult, error) {
	body, err := json.Marshal(map[string]any{"expose": expose, "confirm": true})
	if err != nil {
		return ExposureResult{}, fmt.Errorf("servers: build the exposure request: %w", err)
	}
	data, err := adminRequest(
		ctx, profile, token, http.MethodPost, "/admin/exposure", body, timeout,
	)
	if err != nil {
		return ExposureResult{}, err
	}
	var result ExposureResult
	if err := json.Unmarshal(data, &result); err != nil {
		return ExposureResult{}, fmt.Errorf("servers: decode the exposure result: %w", err)
	}
	return result, nil
}

func fetchServerConfig(
	ctx context.Context,
	profile config.ServerProfile,
	token string,
	timeout time.Duration,
) (ServerConfig, error) {
	data, err := adminRequest(ctx, profile, token, http.MethodGet, "/admin/config", nil, timeout)
	if err != nil {
		return ServerConfig{}, err
	}
	var result ServerConfig
	if err := json.Unmarshal(data, &result); err != nil {
		return ServerConfig{}, fmt.Errorf("servers: decode the Server configuration: %w", err)
	}
	return result, nil
}

func patchServerConfig(
	ctx context.Context,
	profile config.ServerProfile,
	token string,
	patch map[string]any,
	timeout time.Duration,
) (ServerConfigPatch, error) {
	body, err := json.Marshal(map[string]any{"patch": patch})
	if err != nil {
		return ServerConfigPatch{}, fmt.Errorf("servers: build the configuration patch: %w", err)
	}
	data, err := adminRequest(ctx, profile, token, http.MethodPatch, "/admin/config", body, timeout)
	if err != nil {
		return ServerConfigPatch{}, err
	}
	var result ServerConfigPatch
	if err := json.Unmarshal(data, &result); err != nil {
		return ServerConfigPatch{}, fmt.Errorf("servers: decode the configuration patch: %w", err)
	}
	return result, nil
}

func fetchServerLogs(
	ctx context.Context,
	profile config.ServerProfile,
	token string,
	tail int,
	timeout time.Duration,
) (ServerLog, error) {
	if tail <= 0 {
		tail = 200
	}
	path := fmt.Sprintf("/admin/logs?tail=%d", tail)
	data, err := adminRequest(ctx, profile, token, http.MethodGet, path, nil, timeout)
	if err != nil {
		return ServerLog{}, err
	}
	var result ServerLog
	if err := json.Unmarshal(data, &result); err != nil {
		return ServerLog{}, fmt.Errorf("servers: decode the Server log: %w", err)
	}
	return result, nil
}

func serverLifecycle(
	ctx context.Context,
	profile config.ServerProfile,
	token, action string,
	timeout time.Duration,
) (ServerLifecycle, error) {
	data, err := adminRequest(
		ctx, profile, token, http.MethodPost, "/admin/"+action, nil, timeout,
	)
	if err != nil {
		return ServerLifecycle{}, err
	}
	var result ServerLifecycle
	if err := json.Unmarshal(data, &result); err != nil {
		return ServerLifecycle{}, fmt.Errorf("servers: decode the %s result: %w", action, err)
	}
	if action == "restart" {
		result.Restarting = true
	}
	return result, nil
}

func adminRequest(
	ctx context.Context,
	profile config.ServerProfile,
	token, method, path string,
	body []byte,
	timeout time.Duration,
) ([]byte, error) {
	if token == "" {
		return nil, ErrNoToken
	}
	client, err := adminClient(profile, timeout)
	if err != nil {
		return nil, err
	}
	requestURL := strings.TrimRight(profile.URL, "/") + path
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return nil, fmt.Errorf("servers: build the Admin API request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("servers: reach %s: %w", profile.Name, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("servers: read the Admin API response: %w", err)
	}
	if response.StatusCode == http.StatusNoContent {
		return nil, nil
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, decodeAdminError(response.StatusCode, data)
	}
	return data, nil
}

// adminClient uses the pinned certificate for HTTPS, so an Admin API request
// never sends a Device token to a different Server identity.
func adminClient(profile config.ServerProfile, timeout time.Duration) (*http.Client, error) {
	if strings.HasPrefix(strings.ToLower(profile.URL), "https://") {
		return trust.PinnedClient(profile.Fingerprint, timeout)
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &http.Client{Timeout: timeout}, nil
}

func decodeAdminError(status int, data []byte) error {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.Error.Message != "" {
		return &AdminError{
			StatusCode: status,
			Code:       envelope.Error.Code,
			Message:    envelope.Error.Message,
		}
	}
	message := strings.TrimSpace(string(data))
	if message == "" {
		message = http.StatusText(status)
	}
	return &AdminError{StatusCode: status, Message: message}
}
