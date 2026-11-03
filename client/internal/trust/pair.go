package trust

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Device is a trusted Server-side Device as returned by pairing.
type Device struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	CreatedAt   float64  `json:"created_at"`
	LastSeen    *float64 `json:"last_seen"`
	LastAddress string   `json:"last_address"`
}

// Pairing is the result of a successful pair: the token to store, the Device
// record, and the certificate fingerprint to pin.
type Pairing struct {
	Device      Device `json:"device"`
	Token       string `json:"token"`
	Fingerprint string `json:"fingerprint"`
	// Version is the Server release. An older Server omits it, which disables
	// the compatibility check.
	Version string `json:"version,omitempty"`
}

// String renders a Pairing without its token, so logging a decoded value by
// accident cannot leak the credential.
func (p Pairing) String() string {
	return fmt.Sprintf(
		"Pairing{device:%q fingerprint:%q token:<redacted>}",
		p.Device.ID,
		p.Fingerprint,
	)
}

// PairLocal calls the Local Server's loopback-only auto-pairing endpoint.
//
// The URL must be loopback: this function is the only place that talks to a
// Server before its certificate is pinned, and it must never do so across the
// network. For HTTPS the fingerprint presented by the peer is captured and
// checked against both the caller's existing pin and the fingerprint the
// endpoint reports, so a proxy or a changed certificate is a distinct error.
func PairLocal(ctx context.Context, baseURL, name, pinned string) (Pairing, error) {
	if !IsLoopbackURL(baseURL) {
		return Pairing{}, ErrNotLoopback
	}
	if name == "" {
		name = "Local client"
	}
	return pair(ctx, baseURL, "/pair/local", map[string]string{"name": name}, pinned)
}

// PairRemote redeems a one-time Pairing code at a Remote Server.
//
// The code is the only secret in the request; the long-lived Device token comes
// back in the response. On HTTPS the leaf certificate is captured and pinned
// exactly like local pairing: an existing pin must match before the code is
// sent, and the endpoint's reported fingerprint must match the presented one.
func PairRemote(
	ctx context.Context, baseURL, code, name, pinned string,
) (Pairing, error) {
	code = normalizePairingCode(code)
	if code == "" {
		return Pairing{}, ErrPairingCodeRequired
	}
	if name == "" {
		name = "Device"
	}
	return pair(
		ctx,
		baseURL,
		"/pair",
		map[string]string{"code": code, "name": name},
		pinned,
	)
}

func normalizePairingCode(code string) string {
	normalized := strings.ToUpper(strings.TrimSpace(code))
	normalized = strings.NewReplacer(" ", "", "-", "", "_", "").Replace(normalized)
	return normalized
}

// pair performs one pairing POST against path. The caller has already applied
// the loopback restriction, when one applies.
func pair(
	ctx context.Context,
	baseURL, path string,
	body map[string]string,
	pinned string,
) (Pairing, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return Pairing{}, fmt.Errorf("trust: invalid Server URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return Pairing{}, fmt.Errorf("trust: unsupported Server URL scheme %q", parsed.Scheme)
	}
	// With a pin already known, verify the peer before the pairing POST. A
	// changed certificate must not mint a Device the Client will then
	// refuse to use.
	if parsed.Scheme == "https" && NormalizeFingerprint(pinned) != "" {
		presented, err := probeFingerprint(ctx, parsed)
		if err != nil {
			return Pairing{}, fmt.Errorf("trust: verify %s: %w", baseURL, err)
		}
		if presented != NormalizeFingerprint(pinned) {
			return Pairing{}, &FingerprintMismatchError{
				Pinned:    NormalizeFingerprint(pinned),
				Presented: presented,
			}
		}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Pairing{}, fmt.Errorf("trust: build the pairing request: %w", err)
	}
	requestURL := strings.TrimRight(baseURL, "/") + path
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(payload))
	if err != nil {
		return Pairing{}, fmt.Errorf("trust: build the pairing request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	pairingTransport := newPairingTransport()
	client := &http.Client{
		Timeout:   15 * time.Second,
		Transport: pairingTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			// Never follow a redirect away from the pairing endpoint.
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return Pairing{}, fmt.Errorf("trust: pair with %s: %w", baseURL, err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Pairing{}, fmt.Errorf("trust: read the pairing response: %w", err)
	}
	if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
		return Pairing{}, serverError(response.StatusCode, data)
	}
	var paired Pairing
	if err := json.Unmarshal(data, &paired); err != nil {
		return Pairing{}, fmt.Errorf("trust: decode the pairing response: %w", err)
	}
	if paired.Token == "" {
		return Pairing{}, errors.New("trust: the Server returned an empty Device token")
	}
	if parsed.Scheme == "https" {
		presented, ok := pairingTransport.fingerprint()
		if !ok {
			return Pairing{}, errors.New("trust: the Server presented no certificate")
		}
		if NormalizeFingerprint(pinned) != "" && NormalizeFingerprint(pinned) != presented {
			return Pairing{}, &FingerprintMismatchError{
				Pinned:    NormalizeFingerprint(pinned),
				Presented: presented,
			}
		}
		if paired.Fingerprint != "" && NormalizeFingerprint(paired.Fingerprint) != presented {
			return Pairing{}, &FingerprintMismatchError{
				Pinned:    presented,
				Presented: NormalizeFingerprint(paired.Fingerprint),
			}
		}
		paired.Fingerprint = presented
	} else {
		// Plain HTTP has no peer certificate to pin.
		paired.Fingerprint = ""
	}
	return paired, nil
}

// probeFingerprint opens a TLS connection to the loopback endpoint and returns
// the leaf certificate fingerprint. It is the preflight that keeps a changed
// certificate from creating a Device before the Client rejects it.
func probeFingerprint(ctx context.Context, parsed *url.URL) (string, error) {
	address := parsed.Host
	if _, _, err := net.SplitHostPort(address); err != nil {
		address = net.JoinHostPort(parsed.Hostname(), "443")
	}
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	connection, err := (&tls.Dialer{
		NetDialer: dialer,
		Config: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // the fingerprint is compared below
			MinVersion:         tls.VersionTLS12,
		},
	}).DialContext(ctx, "tcp", address)
	if err != nil {
		return "", err
	}
	defer connection.Close()
	state := connection.(*tls.Conn).ConnectionState()
	if len(state.PeerCertificates) == 0 {
		return "", errors.New("the Server presented no certificate")
	}
	return CertificateFingerprint(state.PeerCertificates[0]), nil
}

func serverError(status int, data []byte) error {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.Error.Message != "" {
		return &PairingHTTPError{
			StatusCode: status,
			Code:       envelope.Error.Code,
			Message:    envelope.Error.Message,
		}
	}
	message := strings.TrimSpace(string(data))
	if message == "" {
		message = http.StatusText(status)
	}
	return &PairingHTTPError{StatusCode: status, Message: message}
}
