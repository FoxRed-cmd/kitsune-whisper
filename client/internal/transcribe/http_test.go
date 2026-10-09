package transcribe_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
)

// capture is what the fake server observed from the multipart request.
type capture struct {
	audio         []byte
	audioFilename string
	fields        map[string]string
}

func newServer(t *testing.T, status int, body string) (*httptest.Server, *capture) {
	t.Helper()
	got := &capture{fields: map[string]string{}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/transcribe" {
			t.Errorf("path = %q, want /transcribe", r.URL.Path)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		file, header, err := r.FormFile("audio")
		if err == nil {
			defer file.Close()
			got.audio, _ = io.ReadAll(file)
			got.audioFilename = header.Filename
		}
		for key, values := range r.MultipartForm.Value {
			got.fields[key] = strings.Join(values, ",")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server, got
}

func TestSuccessParsesEnvelope(t *testing.T) {
	body := `{"text":"hello world","language":"en","language_probability":0.98,"duration":3.24}`
	server, got := newServer(t, http.StatusOK, body)

	client := transcribe.NewHTTP(server.URL, "", "", false, false)
	result, err := client.Transcribe(context.Background(), []byte("RIFFabcd"))
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if result.Text != "hello world" || result.Language != "en" {
		t.Fatalf("unexpected result: %+v", result)
	}
	if result.LanguageProbability != 0.98 || result.Duration != 3.24 {
		t.Fatalf("unexpected numbers: %+v", result)
	}
	if string(got.audio) != "RIFFabcd" {
		t.Fatalf("audio = %q", got.audio)
	}
	if got.audioFilename != "utterance.wav" {
		t.Fatalf("filename = %q", got.audioFilename)
	}
}

func TestOptionalFieldsSentOnlyWhenSet(t *testing.T) {
	cases := []struct {
		name          string
		language      string
		initialPrompt string
		wantLanguage  string
		wantPrompt    string
	}{
		{name: "unset", language: "", initialPrompt: ""},
		{name: "auto not sent", language: "auto", initialPrompt: ""},
		{name: "auto case-insensitive", language: "AUTO", initialPrompt: ""},
		{name: "language set", language: "ru", wantLanguage: "ru"},
		{name: "prompt set", initialPrompt: "Kitsune", wantPrompt: "Kitsune"},
		{name: "both set", language: "en", initialPrompt: "Kitsune", wantLanguage: "en", wantPrompt: "Kitsune"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, got := newServer(t, http.StatusOK, `{"text":"x"}`)
			client := transcribe.NewHTTP(server.URL, tc.language, tc.initialPrompt, false, false)
			if _, err := client.Transcribe(context.Background(), []byte("data")); err != nil {
				t.Fatalf("transcribe: %v", err)
			}
			if got.fields["language"] != tc.wantLanguage {
				t.Fatalf("language field = %q, want %q", got.fields["language"], tc.wantLanguage)
			}
			if got.fields["initial_prompt"] != tc.wantPrompt {
				t.Fatalf("initial_prompt field = %q, want %q", got.fields["initial_prompt"], tc.wantPrompt)
			}
		})
	}
}

func TestProcessingFieldsSentOnlyWhenTrue(t *testing.T) {
	cases := []struct {
		name          string
		refine        bool
		summarize     bool
		wantRefine    string
		wantSummarize string
	}{
		{name: "both off"},
		{name: "refine only", refine: true, wantRefine: "true"},
		{name: "summarize only", summarize: true, wantSummarize: "true"},
		{name: "both on", refine: true, summarize: true, wantRefine: "true", wantSummarize: "true"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server, got := newServer(t, http.StatusOK, `{"text":"x"}`)
			client := transcribe.NewHTTP(server.URL, "", "", tc.refine, tc.summarize)
			if _, err := client.Transcribe(context.Background(), []byte("data")); err != nil {
				t.Fatalf("transcribe: %v", err)
			}
			if got.fields["refine"] != tc.wantRefine {
				t.Fatalf("refine field = %q, want %q", got.fields["refine"], tc.wantRefine)
			}
			if got.fields["summarize"] != tc.wantSummarize {
				t.Fatalf("summarize field = %q, want %q", got.fields["summarize"], tc.wantSummarize)
			}
		})
	}
}

func TestProcessingResponseFieldsParse(t *testing.T) {
	body := `{"text":"clean text","raw_text":"um clean text","language":"en",` +
		`"language_probability":0.9,"duration":2.0,` +
		`"applied":{"refine":true,"summarize":false},"warnings":["summarize unavailable"]}`
	server, _ := newServer(t, http.StatusOK, body)

	client := transcribe.NewHTTP(server.URL, "", "", true, true)
	result, err := client.Transcribe(context.Background(), []byte("data"))
	if err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if result.Text != "clean text" {
		t.Fatalf("text = %q, want Delivered text", result.Text)
	}
	if result.RawText != "um clean text" {
		t.Fatalf("raw_text = %q", result.RawText)
	}
	if !result.Applied.Refine || result.Applied.Summarize {
		t.Fatalf("applied = %+v", result.Applied)
	}
	if len(result.Warnings) != 1 || result.Warnings[0] != "summarize unavailable" {
		t.Fatalf("warnings = %v", result.Warnings)
	}
}

func TestWarningsDeliveredToHook(t *testing.T) {
	body := `{"text":"x","warnings":["refine failed: boom","summarize unavailable"]}`
	server, _ := newServer(t, http.StatusOK, body)

	var got []string
	client := transcribe.NewHTTP(server.URL, "", "", true, true)
	client.OnWarning = func(warning string) { got = append(got, warning) }
	if _, err := client.Transcribe(context.Background(), []byte("data")); err != nil {
		t.Fatalf("transcribe: %v", err)
	}
	if len(got) != 2 || got[0] != "refine failed: boom" || got[1] != "summarize unavailable" {
		t.Fatalf("warnings delivered = %v", got)
	}
}

func TestErrorEnvelopeDecoded(t *testing.T) {
	server, _ := newServer(t, http.StatusServiceUnavailable, `{"error":{"code":"unavailable","message":"server is busy"}}`)
	client := transcribe.NewHTTP(server.URL, "", "", false, false)
	_, err := client.Transcribe(context.Background(), []byte("data"))

	var serverErr *transcribe.ServerError
	if !errors.As(err, &serverErr) {
		t.Fatalf("expected *ServerError, got %v", err)
	}
	if serverErr.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d", serverErr.StatusCode)
	}
	if serverErr.Code != "unavailable" || serverErr.Message != "server is busy" {
		t.Fatalf("envelope = %+v", serverErr)
	}
}

func TestErrorWithoutEnvelopeFallsBackToBody(t *testing.T) {
	server, _ := newServer(t, http.StatusInternalServerError, "boom")
	client := transcribe.NewHTTP(server.URL, "", "", false, false)
	_, err := client.Transcribe(context.Background(), []byte("data"))

	var serverErr *transcribe.ServerError
	if !errors.As(err, &serverErr) {
		t.Fatalf("expected *ServerError, got %v", err)
	}
	if serverErr.Message != "boom" {
		t.Fatalf("message = %q", serverErr.Message)
	}
}

func TestTrailingSlashInBaseURL(t *testing.T) {
	server, _ := newServer(t, http.StatusOK, `{"text":"ok"}`)
	client := transcribe.NewHTTP(server.URL+"/", "", "", false, false)
	if _, err := client.Transcribe(context.Background(), []byte("data")); err != nil {
		t.Fatalf("transcribe: %v", err)
	}
}

func TestMalformedSuccessBodyIsError(t *testing.T) {
	server, _ := newServer(t, http.StatusOK, `not json`)
	client := transcribe.NewHTTP(server.URL, "", "", false, false)
	if _, err := client.Transcribe(context.Background(), []byte("data")); err == nil {
		t.Fatal("expected error for malformed success body")
	}
}
