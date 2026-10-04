package cli_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cli"
)

func wavBytes() []byte {
	const (
		sampleRate = 16000
		channels   = 1
		bits       = 16
		frames     = sampleRate
	)
	var b bytes.Buffer
	dataSize := frames * channels * (bits / 8)
	byteRate := sampleRate * channels * (bits / 8)
	blockAlign := channels * (bits / 8)
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(36+dataSize))
	b.WriteString("WAVE")
	b.WriteString("fmt ")
	_ = binary.Write(&b, binary.LittleEndian, uint32(16))
	_ = binary.Write(&b, binary.LittleEndian, uint16(1))
	_ = binary.Write(&b, binary.LittleEndian, uint16(channels))
	_ = binary.Write(&b, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(&b, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(&b, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(&b, binary.LittleEndian, uint16(bits))
	b.WriteString("data")
	_ = binary.Write(&b, binary.LittleEndian, uint32(dataSize))
	b.Write(make([]byte, dataSize))
	return b.Bytes()
}

func writeConfigFile(t *testing.T, client map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	// Keep the log file and Spool inside the test's temp dir so tests never
	// touch the real per-user cache.
	if _, ok := client["log_file"]; !ok {
		client["log_file"] = filepath.Join(dir, "client.log")
	}
	if _, ok := client["spool_dir"]; !ok {
		client["spool_dir"] = filepath.Join(dir, "spool")
	}
	path := filepath.Join(dir, "kitsune.yaml")
	raw, err := yaml.Marshal(map[string]any{"client": client})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestCheckConfigPrintsEffectiveConfig(t *testing.T) {
	path := writeConfigFile(t, map[string]any{"hotkey": "Ctrl+Alt+D"})
	var stdout, stderr bytes.Buffer

	code := cli.Run(
		[]string{"--config", path, "--check-config", "--device", "Yeti", "--verbose"},
		&stdout, &stderr,
	)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	var parsed map[string]map[string]any
	if err := yaml.Unmarshal(stdout.Bytes(), &parsed); err != nil {
		t.Fatalf("parse output: %v\n%s", err, stdout.String())
	}
	client := parsed["client"]
	if client["hotkey"] != "Ctrl+Alt+D" {
		t.Fatalf("hotkey = %v", client["hotkey"])
	}
	if client["log_level"] != "debug" {
		t.Fatalf("log_level = %v", client["log_level"])
	}
	audioSection, ok := client["audio"].(map[string]any)
	if !ok || audioSection["device"] != "Yeti" {
		t.Fatalf("audio = %v", client["audio"])
	}
}

func TestCheckConfigExitsNonZeroOnError(t *testing.T) {
	path := writeConfigFile(t, map[string]any{"bogus": 1})
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{"--config", path, "--check-config"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "client.bogus") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

func TestTranscribeFilePostsAndPrintsText(t *testing.T) {
	var gotAudio []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
		}
		file, _, err := r.FormFile("audio")
		if err == nil {
			defer file.Close()
			gotAudio, _ = io.ReadAll(file)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"hello from file","language":"en","language_probability":0.9,"duration":1.0}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	wavPath := filepath.Join(dir, "clip.wav")
	sample := wavBytes()
	if err := os.WriteFile(wavPath, sample, 0o600); err != nil {
		t.Fatalf("write wav: %v", err)
	}
	path := writeConfigFile(t, map[string]any{"server_url": server.URL})

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"--config", path, "transcribe-file", wavPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "hello from file" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if !bytes.Equal(gotAudio, sample) {
		t.Fatalf("server received %d bytes, want %d", len(gotAudio), len(sample))
	}
}

func TestTranscribeFileReportsUnreachableServer(t *testing.T) {
	// Bind then release a port so the address is known to refuse connections.
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	url := server.URL
	server.Close()

	dir := t.TempDir()
	wavPath := filepath.Join(dir, "clip.wav")
	if err := os.WriteFile(wavPath, wavBytes(), 0o600); err != nil {
		t.Fatalf("write wav: %v", err)
	}
	path := writeConfigFile(t, map[string]any{"server_url": url})

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"--config", path, "transcribe-file", wavPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "transcribe failed") || !strings.Contains(stderr.String(), url) {
		t.Fatalf("stderr = %q, want a clear unreachable-server error", stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "" {
		t.Fatalf("stdout = %q, want nothing injected", stdout.String())
	}
}

func TestTranscribeFileReportsServerErrorEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"code":"unavailable","message":"server is busy"}}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	wavPath := filepath.Join(dir, "clip.wav")
	if err := os.WriteFile(wavPath, wavBytes(), 0o600); err != nil {
		t.Fatalf("write wav: %v", err)
	}
	path := writeConfigFile(t, map[string]any{"server_url": server.URL})

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"--config", path, "transcribe-file", wavPath}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (stderr %s)", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "503") || !strings.Contains(stderr.String(), "server is busy") {
		t.Fatalf("stderr = %q, want the server error envelope", stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "" {
		t.Fatalf("stdout = %q, want nothing injected", stdout.String())
	}
}

func TestGlobalFlagsAfterSubcommand(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"text":"after subcommand"}`)
	}))
	defer server.Close()

	dir := t.TempDir()
	wavPath := filepath.Join(dir, "clip.wav")
	if err := os.WriteFile(wavPath, wavBytes(), 0o600); err != nil {
		t.Fatalf("write wav: %v", err)
	}
	path := writeConfigFile(t, map[string]any{"server_url": server.URL})

	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"transcribe-file", "--config", path, wavPath}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit = %d, stderr = %s", code, stderr.String())
	}
	if strings.TrimSpace(stdout.String()) != "after subcommand" {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestTranscribeFileRejectsNonWAV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "not.wav")
	if err := os.WriteFile(path, []byte("not a wav"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	configPath := writeConfigFile(t, map[string]any{})
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"--config", configPath, "transcribe-file", path}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
}

func TestTranscribeFileRequiresArgument(t *testing.T) {
	configPath := writeConfigFile(t, map[string]any{})
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"--config", configPath, "transcribe-file"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestRunRejectsInvalidHotkey(t *testing.T) {
	configPath := writeConfigFile(t, map[string]any{"hotkey": "Ctrl+Banana"})
	var stdout, stderr bytes.Buffer

	// Both the explicit subcommand and the default (no command) start the run
	// mode, so both must reject a malformed hotkey before touching audio.
	for _, args := range [][]string{
		{"--config", configPath, "run"},
		{"--config", configPath},
	} {
		code := cli.Run(args, &stdout, &stderr)
		if code != 1 {
			t.Fatalf("args %v: exit = %d, want 1 (stderr %s)", args, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "hotkey") {
			t.Fatalf("args %v: stderr = %q", args, stderr.String())
		}
	}
}

func TestUnknownCommandExitsTwo(t *testing.T) {
	configPath := writeConfigFile(t, map[string]any{})
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"--config", configPath, "frobnicate"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestRecordRejectsNonPositiveSeconds(t *testing.T) {
	configPath := writeConfigFile(t, map[string]any{})
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"--config", configPath, "record", "--seconds", "0"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %s)", code, stderr.String())
	}
}

func TestRecordRejectsPositionalArguments(t *testing.T) {
	configPath := writeConfigFile(t, map[string]any{})
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"--config", configPath, "record", "extra"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stderr %s)", code, stderr.String())
	}
}
