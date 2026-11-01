package trust

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
)

// ServerState is the reachability and authentication state the Tray and
// Settings show. It is advisory: it never changes where an Utterance is sent.
type ServerState string

const (
	// StateUnknown means no check has completed yet.
	StateUnknown ServerState = "unknown"
	// StateOnline means /health answered and the Device token was accepted.
	StateOnline ServerState = "online"
	// StateOffline means the Server did not answer.
	StateOffline ServerState = "offline"
	// StateUnauthenticated means the Server answered but rejected the token.
	StateUnauthenticated ServerState = "unauthenticated"
	// StateUnpinned means the profile is HTTPS but has no pinned fingerprint.
	StateUnpinned ServerState = "unpinned"
	// StateMismatch means the certificate changed since it was pinned.
	StateMismatch ServerState = "mismatch"
)

// Status is one Server health check result. Model fields come from the
// authenticated /status body and are advisory like the rest of the snapshot.
type Status struct {
	State   ServerState `json:"state"`
	Version string      `json:"version,omitempty"`
	Error   string      `json:"error,omitempty"`
	// Model is the active model's name, when the Server reports one.
	Model string `json:"model,omitempty"`
	// ModelState is loaded, cold, loading, failed, or stopped.
	ModelState string `json:"model_state,omitempty"`
	// ModelPath is the local CTranslate2 directory, or "" for a catalog model.
	ModelPath string `json:"model_path,omitempty"`
	// IdleUnloadSeconds is the Server's idle unload timeout.
	IdleUnloadSeconds int `json:"idle_unload_seconds,omitempty"`
	// Stopped reports a soft-stopped Server.
	Stopped bool `json:"stopped,omitempty"`
	// Loaded reports whether the model is resident.
	Loaded bool `json:"loaded,omitempty"`
}

// CheckServer probes one Server profile: /health for liveness and the version,
// then the authenticated /status for the token. A certificate problem is
// reported distinctly so the window can say "pair again" instead of "wrong
// password". The token is only attached to the /status request, which the
// pinned client never sends when the handshake fails.
func CheckServer(
	ctx context.Context,
	profile config.ServerProfile,
	token string,
	timeout time.Duration,
) Status {
	parsed, err := url.Parse(profile.URL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return Status{State: StateOffline, Error: "invalid Server URL"}
	}
	client, err := clientFor(profile, timeout)
	if err != nil {
		return statusForError(err)
	}
	base := strings.TrimRight(profile.URL, "/")
	health, err := request(ctx, client, base+"/health", "")
	if err != nil {
		return statusForError(err)
	}
	var healthBody struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(health, &healthBody); err != nil {
		return Status{State: StateOffline, Error: "the Server returned an unreadable health response"}
	}
	status := Status{State: StateUnauthenticated, Version: healthBody.Version}

	body, err := request(ctx, client, base+"/status", token)
	if err != nil {
		var mismatch *FingerprintMismatchError
		if errors.As(err, &mismatch) {
			return statusForError(err)
		}
		if isUnauthorized(err) {
			return status
		}
		// A 404 on an old Server is an upgrade problem, not an auth one.
		return Status{State: StateOffline, Error: err.Error()}
	}
	status.State = StateOnline
	applyStatus(&status, body)
	return status
}

// statusBody is the part of the authenticated /status response the Tray and
// Diagnostics render. Older Servers omit these keys; the zero values keep the
// reachability report intact.
type statusBody struct {
	Model             string `json:"model"`
	ModelState        string `json:"model_state"`
	ModelPath         string `json:"model_path"`
	IdleUnloadSeconds int    `json:"idle_unload_seconds"`
	Lifecycle         struct {
		Stopped bool `json:"stopped"`
		Loaded  bool `json:"loaded"`
	} `json:"lifecycle"`
}

// applyStatus copies the model state from /status into the health result. The
// body is already authenticated; an unreadable body only loses the advisory
// fields, not the online state.
func applyStatus(status *Status, body []byte) {
	var parsed statusBody
	if err := json.Unmarshal(body, &parsed); err != nil {
		return
	}
	status.Model = parsed.Model
	status.ModelState = parsed.ModelState
	status.ModelPath = parsed.ModelPath
	status.IdleUnloadSeconds = parsed.IdleUnloadSeconds
	status.Stopped = parsed.Lifecycle.Stopped
	status.Loaded = parsed.Lifecycle.Loaded
}

func clientFor(profile config.ServerProfile, timeout time.Duration) (*http.Client, error) {
	parsed, err := url.Parse(profile.URL)
	if err != nil {
		return nil, err
	}
	if parsed.Scheme == "https" {
		return PinnedClient(profile.Fingerprint, timeout)
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &http.Client{Timeout: timeout}, nil
}

type unauthorizedError struct{ status int }

func (e unauthorizedError) Error() string {
	return fmt.Sprintf("the Server rejected the Device token (HTTP %d); pair this profile again", e.status)
}

func isUnauthorized(err error) bool {
	var unauthorized unauthorizedError
	return errors.As(err, &unauthorized)
}

func request(ctx context.Context, client *http.Client, endpoint, token string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("read the Server response: %w", err)
	}
	if response.StatusCode == http.StatusUnauthorized {
		return nil, unauthorizedError{status: response.StatusCode}
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("the Server returned HTTP %d: %s", response.StatusCode, excerpt(data))
	}
	return data, nil
}

func excerpt(data []byte) string {
	message := strings.TrimSpace(string(data))
	if len(message) > 200 {
		message = message[:200]
	}
	return message
}

func statusForError(err error) Status {
	var mismatch *FingerprintMismatchError
	if errors.As(err, &mismatch) {
		return Status{State: StateMismatch, Error: mismatch.Error()}
	}
	if errors.Is(err, ErrCertificateNotPinned) {
		return Status{State: StateUnpinned, Error: ErrCertificateNotPinned.Error()}
	}
	return Status{State: StateOffline, Error: err.Error()}
}
