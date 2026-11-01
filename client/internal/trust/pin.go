package trust

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var (
	// ErrFingerprintMismatch is the distinct failure a changed Server
	// certificate produces. Callers translate it into "pair again", not into
	// an authentication failure.
	ErrFingerprintMismatch = errors.New("trust: the Server certificate fingerprint does not match the pinned one")
	// ErrCertificateNotPinned means an HTTPS profile has no pinned fingerprint
	// yet, so the Client must pair before it may send a token.
	ErrCertificateNotPinned = errors.New("trust: the Server certificate is not pinned; pair this profile first")
	// ErrNotLoopback means local auto-pairing was pointed at a remote address.
	ErrNotLoopback = errors.New("trust: local auto-pairing is available only for loopback addresses")
)

// FingerprintMismatchError reports both fingerprints so the message can tell
// the user that the Server identity changed. Neither value is secret.
type FingerprintMismatchError struct {
	Pinned    string
	Presented string
}

func (e *FingerprintMismatchError) Error() string {
	return fmt.Sprintf(
		"%v (pinned %s, presented %s); re-pair this Server profile",
		ErrFingerprintMismatch,
		e.Pinned,
		e.Presented,
	)
}

func (e *FingerprintMismatchError) Is(target error) bool {
	return target == ErrFingerprintMismatch
}

// NormalizeFingerprint renders a certificate fingerprint as lowercase hex.
// Colons, spaces, and an optional "sha256:" prefix are accepted so a
// fingerprint copied from the Server log or an OpenSSL command matches.
func NormalizeFingerprint(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.TrimPrefix(normalized, "sha256:")
	normalized = strings.NewReplacer(":", "", " ", "", "-", "").Replace(normalized)
	return normalized
}

// CertificateFingerprint returns the SHA-256 fingerprint of a certificate.
func CertificateFingerprint(cert *x509.Certificate) string {
	digest := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(digest[:])
}

// PinnedClient builds an HTTP client that trusts exactly one certificate.
// A pinned fingerprint replaces CA verification, which is what makes a
// self-signed Local Server work; the handshake fails before any request, and
// therefore before any token, is sent when the certificate differs.
func PinnedClient(fingerprint string, timeout time.Duration) (*http.Client, error) {
	pinned := NormalizeFingerprint(fingerprint)
	if pinned == "" {
		return nil, ErrCertificateNotPinned
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: pinnedTransport(pinned),
	}, nil
}

func pinnedTransport(pinned string) *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = &tls.Config{
		// The pin below is stricter than the system CA store for this Server:
		// it accepts the generated self-signed certificate and nothing else.
		InsecureSkipVerify: true, //nolint:gosec // pinned in VerifyPeerCertificate
		MinVersion:         tls.VersionTLS12,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			return verifyPinned(rawCerts, pinned)
		},
	}
	return transport
}

func verifyPinned(rawCerts [][]byte, pinned string) error {
	if len(rawCerts) == 0 {
		return errors.New("trust: the Server presented no certificate")
	}
	certificate, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return fmt.Errorf("trust: parse the Server certificate: %w", err)
	}
	presented := CertificateFingerprint(certificate)
	if presented != pinned {
		return &FingerprintMismatchError{Pinned: pinned, Presented: presented}
	}
	return nil
}

// pairingTransport accepts any certificate but records the leaf fingerprint so
// the first pairing can pin it. It is used only for a loopback URL, and the
// recorded value must match the fingerprint the pairing endpoint reports.
type pairingTransport struct {
	base  *http.Transport
	leaf  []byte
	found bool
}

func newPairingTransport() *pairingTransport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	pairing := &pairingTransport{}
	transport.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true, //nolint:gosec // first pairing pins the fingerprint
		MinVersion:         tls.VersionTLS12,
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) > 0 {
				pairing.leaf = rawCerts[0]
				pairing.found = true
			}
			return nil
		},
	}
	pairing.base = transport
	return pairing
}

func (p *pairingTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return p.base.RoundTrip(request)
}

func (p *pairingTransport) fingerprint() (string, bool) {
	if !p.found {
		return "", false
	}
	certificate, err := x509.ParseCertificate(p.leaf)
	if err != nil {
		return "", false
	}
	return CertificateFingerprint(certificate), true
}

// IsLoopbackURL reports whether a Server profile URL points at this machine.
// Local auto-pairing is restricted to it; an explicit plain-HTTP profile may
// still point anywhere, because that is the operator's insecure opt-out.
func IsLoopbackURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	// 127.0.0.0/8 is loopback too; Hostname strips IPv6 brackets.
	return strings.HasPrefix(host, "127.")
}
