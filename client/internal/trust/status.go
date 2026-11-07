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

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/api"
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
	// StateVersionMismatch means the Server answered but its release is not
	// compatible with this Client. It is an upgrade problem, not an auth one.
	StateVersionMismatch ServerState = "version_mismatch"
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
	// Device is the resolved compute device: cuda or cpu.
	Device string `json:"device,omitempty"`
	// ComputeType is the resolved CTranslate2 compute type.
	ComputeType string `json:"compute_type,omitempty"`
	// GPU is the CUDA availability the Server reports. A nil pointer means an
	// older Server did not report it, which is different from "no CUDA".
	GPU *GPUStatus `json:"gpu,omitempty"`
	// Network is the listener the Server reports: whether LAN exposure is on,
	// the address it binds, and the port. A nil pointer means an older Server
	// did not report it.
	Network *NetworkStatus `json:"network,omitempty"`
}

// NetworkStatus is the Server's effective listener from the authenticated
// /status body. BindHost is the address the process is actually listening on,
// which differs from the configured server.host while LAN exposure is on.
type NetworkStatus = api.NetworkStatus

// GPUStatus is the Server's CUDA availability from the authenticated /status
// body. Available is true when at least one CUDA device was detected, even if
// the Server is configured to use CPU.
type GPUStatus = api.GPUStatus

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
	var healthBody api.HealthResponse
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

// applyStatus copies the model state from /status into the health result. The
// body is already authenticated; an unreadable body only loses the advisory
// fields, not the online state. Older Servers omit keys; the zero values keep
// the reachability report intact.
func applyStatus(status *Status, body []byte) {
	var parsed api.StatusResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return
	}
	status.Model = parsed.Model
	status.ModelState = parsed.ModelState
	status.ModelPath = parsed.ModelPath
	status.IdleUnloadSeconds = parsed.IdleUnloadSeconds
	status.Device = parsed.Device
	status.ComputeType = parsed.ComputeType
	status.GPU = parsed.GPU
	status.Network = parsed.Network
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
