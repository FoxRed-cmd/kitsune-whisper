package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/config"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/logging"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/transcribe"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
)

// countingServer answers /transcribe from a fixed status and records every
// request, so a test can prove no second profile was contacted.
type countingServer struct {
	*httptest.Server
	requests atomic.Int32
	status   int
	text     string
	delay    time.Duration
}

func newCountingServer(t *testing.T, status int, text string) *countingServer {
	t.Helper()
	fake := &countingServer{status: status, text: text}
	fake.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.requests.Add(1)
		if fake.delay > 0 {
			select {
			case <-time.After(fake.delay):
			case <-r.Context().Done():
				return
			}
		}
		if fake.status != http.StatusOK {
			w.WriteHeader(fake.status)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error": map[string]string{"code": "engine_failed", "message": "the model failed"},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(transcribe.Transcription{Text: fake.text})
	}))
	t.Cleanup(fake.Close)
	return fake
}

func (s *countingServer) profile(id, name string) config.ServerProfile {
	return config.ServerProfile{ID: id, Name: name, URL: s.URL}
}

func testLogger(t *testing.T) *logging.Logger {
	t.Helper()
	logger := logging.New(logging.Options{
		Level:  logging.Error,
		File:   filepath.Join(t.TempDir(), "client.log"),
		Stderr: io.Discard,
	})
	t.Cleanup(func() { _ = logger.Close() })
	return logger
}

func testManager(t *testing.T) *trust.Manager {
	t.Helper()
	return trust.NewManager(trust.NewTokenStoreWithKeyring(nil, t.TempDir()), nil, nil)
}

// TestUtterancesGoOnlyToTheActiveProfile proves a manual switch decides the
// destination and a failure is never retried on the other profile.
func TestUtterancesGoOnlyToTheActiveProfile(t *testing.T) {
	active := newCountingServer(t, http.StatusInternalServerError, "")
	other := newCountingServer(t, http.StatusOK, "wrong profile")
	cfg := config.ClientConfig{
		ActiveServer: "a",
		Servers:      []config.ServerProfile{active.profile("a", "Active"), other.profile("b", "Other")},
		Language:     "auto",
	}
	logger := testLogger(t)
	manager := testManager(t)

	transcriber := newTranscriber(cfg, logger, manager)
	if _, err := transcriber.Transcribe(context.Background(), []byte("audio")); err == nil {
		t.Fatal("a 5xx was reported as success")
	}
	if got := active.requests.Load(); got != 1 {
		t.Fatalf("active requests = %d, want 1", got)
	}
	if got := other.requests.Load(); got != 0 {
		t.Fatalf("the other profile received %d requests, want 0", got)
	}

	cfg.ActiveServer = "b"
	result, err := newTranscriber(cfg, logger, manager).Transcribe(context.Background(), []byte("audio"))
	if err != nil {
		t.Fatalf("transcribe after the switch: %v", err)
	}
	if result.Text != "wrong profile" {
		t.Fatalf("text = %q", result.Text)
	}
	if got := other.requests.Load(); got != 1 {
		t.Fatalf("new active requests = %d, want 1", got)
	}
	if got := active.requests.Load(); got != 1 {
		t.Fatalf("old active requests = %d, want 1", got)
	}
}

// stubRecorder, stubInjector, and stubSpool are the Dictation cycle's ports.
type stubRecorder struct{}

func (stubRecorder) Start() error { return nil }
func (stubRecorder) Stop() (cycle.Utterance, error) {
	return cycle.Utterance{Audio: []byte("wav"), Duration: time.Second}, nil
}
func (stubRecorder) Cancel() error { return nil }

type stubInjector struct {
	mu    sync.Mutex
	texts []string
}

func (i *stubInjector) Inject(text string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.texts = append(i.texts, text)
	return nil
}

func (i *stubInjector) count() int {
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.texts)
}

type stubSpool struct {
	saves atomic.Int32
}

func (s *stubSpool) Save(cycle.Utterance) error {
	s.saves.Add(1)
	return nil
}

// runFailingCycle drives one Utterance through the real Dictation cycle and
// returns the observable failure results.
func runFailingCycle(
	t *testing.T,
	cfg config.ClientConfig,
	timeout time.Duration,
) (saves int, injected int, failures int) {
	t.Helper()
	logger := testLogger(t)
	inj := &stubInjector{}
	spool := &stubSpool{}
	failed := make(chan struct{}, 1)
	core := cycle.New(cycle.Options{
		Recorder:    stubRecorder{},
		Transcriber: newTranscriber(cfg, logger, testManager(t)),
		Injector:    inj,
		Spool:       spool,
		OnError: func(error) {
			failures++
			select {
			case failed <- struct{}{}:
			default:
			}
		},
		Limits: cycle.Limits{MinRecording: 0, MaxRecording: time.Minute, Timeout: timeout},
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	triggers := make(chan cycle.Trigger)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = core.Run(ctx, triggers)
	}()

	triggers <- cycle.Toggle
	triggers <- cycle.Toggle
	select {
	case <-failed:
	case <-time.After(5 * time.Second):
		close(triggers)
		<-done
		t.Fatal("the failed cycle never reported an error")
	}
	close(triggers)
	<-done
	return int(spool.saves.Load()), inj.count(), failures
}

// TestAFailedUtteranceSpoolsOnceAndInjectsNothing proves a 5xx and a timeout
// each produce one error, one Spool entry, no Injection, and no request to the
// other Server profile.
func TestAFailedUtteranceSpoolsOnceAndInjectsNothing(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		delay   time.Duration
		timeout time.Duration
	}{
		{name: "5xx", status: http.StatusInternalServerError, timeout: 2 * time.Second},
		{name: "timeout", status: http.StatusOK, delay: 500 * time.Millisecond, timeout: 50 * time.Millisecond},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			active := newCountingServer(t, tc.status, "late")
			active.delay = tc.delay
			other := newCountingServer(t, http.StatusOK, "other")
			cfg := config.ClientConfig{
				ActiveServer: "a",
				Servers: []config.ServerProfile{
					active.profile("a", "Active"),
					other.profile("b", "Other"),
				},
				Language: "auto",
			}

			saves, injected, failures := runFailingCycle(t, cfg, tc.timeout)

			if saves != 1 {
				t.Fatalf("Spool entries = %d, want 1", saves)
			}
			if injected != 0 {
				t.Fatalf("injections = %d, want 0", injected)
			}
			if failures != 1 {
				t.Fatalf("user-facing failures = %d, want 1", failures)
			}
			if got := other.requests.Load(); got != 0 {
				t.Fatalf("the other profile received %d requests, want 0", got)
			}
			if got := active.requests.Load(); got != 1 {
				t.Fatalf("active requests = %d, want exactly 1", got)
			}
		})
	}
}

// TestTimeoutErrorMessageIsActionable sanity-checks that the error the Client
// surfaces names the failure rather than an empty string.
func TestTimeoutErrorMessageIsActionable(t *testing.T) {
	server := newCountingServer(t, http.StatusOK, "late")
	server.delay = 500 * time.Millisecond
	cfg := config.ClientConfig{
		ActiveServer: "a",
		Servers:      []config.ServerProfile{server.profile("a", "Active")},
		Language:     "auto",
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := newTranscriber(cfg, testLogger(t), testManager(t)).Transcribe(ctx, []byte("audio"))
	if err == nil {
		t.Fatal("a timed-out request reported success")
	}
	if err.Error() == "" {
		t.Fatal("the timeout has no message")
	}
}
