package transcribe

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// HTTP is a Transcriber that POSTs an utterance to a Server's /transcribe.
type HTTP struct {
	BaseURL       string
	Client        *http.Client
	Language      string
	InitialPrompt string
	// Token is the Device token sent as "Authorization: Bearer". It is never
	// logged, serialised, or included in an error.
	Token string `json:"-"`
	// Fingerprint is the pinned certificate SHA-256 for an HTTPS profile.
	Fingerprint string
	// OnWarning, when non-nil, receives each warning from a successful response.
	OnWarning func(string)
}

// NewHTTPWithTrust builds an HTTP transcriber for one Server profile. An HTTPS
// profile must carry a pinned fingerprint: without it the Client refuses before
// opening a connection, so no audio or token crosses the network.
func NewHTTPWithTrust(baseURL, fingerprint, token, language, initialPrompt string) (*HTTP, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Server URL: %w", err)
	}
	transcriber := &HTTP{
		BaseURL:       baseURL,
		Client:        &http.Client{Timeout: 0},
		Language:      language,
		InitialPrompt: initialPrompt,
		Token:         token,
		Fingerprint:   fingerprint,
	}
	if parsed.Scheme == "https" {
		client, err := trust.PinnedClient(fingerprint, 0)
		if err != nil {
			return nil, err
		}
		transcriber.Client = client
	}
	return transcriber, nil
}

// NewHTTP builds an HTTP transcriber. language and initialPrompt are sent only
// when they are set to something other than the "unset" defaults. The
// deprecated Refine and Summarize switches are never sent: the Client does not
// expose text processing.
func NewHTTP(baseURL, language, initialPrompt string) *HTTP {
	return &HTTP{
		BaseURL:       baseURL,
		Client:        &http.Client{Timeout: 0},
		Language:      language,
		InitialPrompt: initialPrompt,
	}
}

func (h *HTTP) client() *http.Client {
	if h.Client != nil {
		return h.Client
	}
	return http.DefaultClient
}

// Transcribe implements Transcriber. The audio is sent verbatim as the
// multipart "audio" part; the Server is responsible for decoding it.
func (h *HTTP) Transcribe(ctx context.Context, audio []byte) (Transcription, error) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("audio", "utterance.wav")
	if err != nil {
		return Transcription{}, fmt.Errorf("build multipart: %w", err)
	}
	if _, err := part.Write(audio); err != nil {
		return Transcription{}, fmt.Errorf("write multipart: %w", err)
	}
	if language := h.effectiveLanguage(); language != "" {
		if err := writer.WriteField("language", language); err != nil {
			return Transcription{}, fmt.Errorf("write language: %w", err)
		}
	}
	if prompt := strings.TrimSpace(h.InitialPrompt); prompt != "" {
		if err := writer.WriteField("initial_prompt", prompt); err != nil {
			return Transcription{}, fmt.Errorf("write initial_prompt: %w", err)
		}
	}
	if err := writer.Close(); err != nil {
		return Transcription{}, fmt.Errorf("close multipart: %w", err)
	}

	endpoint := strings.TrimRight(h.BaseURL, "/") + "/transcribe"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, &body)
	if err != nil {
		return Transcription{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if h.Token != "" {
		req.Header.Set("Authorization", "Bearer "+h.Token)
	}

	resp, err := h.client().Do(req)
	if err != nil {
		return Transcription{}, fmt.Errorf("POST %s: %w", endpoint, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return Transcription{}, fmt.Errorf("read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return Transcription{}, decodeError(resp.StatusCode, data)
	}

	var transcription Transcription
	if err := json.Unmarshal(data, &transcription); err != nil {
		return Transcription{}, fmt.Errorf("decode response: %w", err)
	}
	if h.OnWarning != nil {
		for _, warning := range transcription.Warnings {
			h.OnWarning(warning)
		}
	}
	return transcription, nil
}

func (h *HTTP) effectiveLanguage() string {
	language := strings.TrimSpace(h.Language)
	if language == "" || strings.EqualFold(language, "auto") {
		return ""
	}
	return language
}

func decodeError(status int, data []byte) error {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(data, &envelope); err == nil && envelope.Error.Code != "" {
		return &ServerError{StatusCode: status, Code: envelope.Error.Code, Message: envelope.Error.Message}
	}
	message := strings.TrimSpace(string(data))
	if message == "" {
		message = http.StatusText(status)
	}
	return &ServerError{StatusCode: status, Code: "error", Message: message}
}
