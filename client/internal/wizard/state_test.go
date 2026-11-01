package wizard_test

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/trust"
	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/wizard"
)

func fixedNow() time.Time {
	return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
}

func newService(t *testing.T, path string) *wizard.Service {
	t.Helper()
	return wizard.Open(wizard.NewFileStore(path), wizard.Options{Now: fixedNow})
}

func tempState(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "wizard.json")
}

func TestOpenStartsAtWelcomeAndIsFresh(t *testing.T) {
	service := newService(t, tempState(t))
	if !service.Fresh() {
		t.Fatal("a missing state file must be fresh")
	}
	if service.Active() {
		t.Fatal("a fresh wizard must not be active until opened")
	}
	if service.Step() != wizard.StepWelcome {
		t.Fatalf("step = %q, want welcome", service.Step())
	}
	if got := service.State().Done; len(got) != 0 {
		t.Fatalf("done = %v, want empty", got)
	}
}

func TestNextMovesThroughEveryStepAndCompletes(t *testing.T) {
	path := tempState(t)
	service := newService(t, path)
	if err := service.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	want := []wizard.Step{
		wizard.StepMicrophone,
		wizard.StepHotkey,
		wizard.StepHardware,
		wizard.StepModel,
		wizard.StepServer,
		wizard.StepTry,
	}
	for _, step := range want {
		if err := service.Next(); err != nil {
			t.Fatalf("next: %v", err)
		}
		if service.Step() != step {
			t.Fatalf("step = %q, want %q", service.Step(), step)
		}
	}
	if err := service.Next(); err != nil {
		t.Fatalf("finish: %v", err)
	}
	state := service.State()
	if !state.Completed {
		t.Fatal("the wizard did not complete")
	}
	if state.Active {
		t.Fatal("a completed wizard must not be active")
	}
	for _, step := range wizard.SetupSteps {
		found := false
		for _, done := range state.Done {
			if done == step {
				found = true
			}
		}
		if !found {
			t.Fatalf("done = %v, missing %q", state.Done, step)
		}
	}
	if !contains(state.Done, wizard.StepTry) {
		t.Fatalf("done = %v, missing the try step", state.Done)
	}
	// The persisted state survives a reload.
	reloaded := newService(t, path)
	if reloaded.Step() != wizard.StepTry || !reloaded.State().Completed {
		t.Fatalf("reloaded state = %+v", reloaded.State())
	}
}

func TestBackDoesNotLeaveTheFirstStep(t *testing.T) {
	service := newService(t, tempState(t))
	_ = service.Open()
	if err := service.Back(); err != nil {
		t.Fatalf("back at welcome: %v", err)
	}
	if service.Step() != wizard.StepWelcome {
		t.Fatalf("step = %q, want welcome", service.Step())
	}
	if err := service.Next(); err != nil {
		t.Fatalf("next: %v", err)
	}
	if err := service.Next(); err != nil {
		t.Fatalf("next: %v", err)
	}
	if service.Step() != wizard.StepHotkey {
		t.Fatalf("step = %q, want hotkey", service.Step())
	}
	done := service.State().Done
	if err := service.Back(); err != nil {
		t.Fatalf("back: %v", err)
	}
	if service.Step() != wizard.StepMicrophone {
		t.Fatalf("step = %q, want microphone", service.Step())
	}
	if len(service.State().Done) != len(done) {
		t.Fatalf("back changed the done list: %v -> %v", done, service.State().Done)
	}
}

func TestSkippingTheTryStepRecordsItAndCompletes(t *testing.T) {
	service := newService(t, tempState(t))
	_ = service.Open()
	for i := 0; i < len(wizard.SetupSteps); i++ {
		if err := service.Next(); err != nil {
			t.Fatalf("next: %v", err)
		}
	}
	if service.Step() != wizard.StepTry {
		t.Fatalf("step = %q, want try", service.Step())
	}
	if err := service.SkipStep(); err != nil {
		t.Fatalf("skip try: %v", err)
	}
	state := service.State()
	if !state.Completed || state.Active {
		t.Fatalf("state = %+v", state)
	}
	if !contains(state.Skipped, wizard.StepTry) {
		t.Fatalf("skipped = %v, want the try step", state.Skipped)
	}
	if contains(state.Done, wizard.StepTry) {
		t.Fatal("a skipped try step is also done")
	}
}

func TestSkipStepMarksSkippedAndAdvances(t *testing.T) {
	service := newService(t, tempState(t))
	_ = service.Open()
	_ = service.Next() // microphone
	if err := service.SkipStep(); err != nil {
		t.Fatalf("skip: %v", err)
	}
	if service.Step() != wizard.StepHotkey {
		t.Fatalf("step = %q, want hotkey", service.Step())
	}
	state := service.State()
	if len(state.Skipped) != 1 || state.Skipped[0] != wizard.StepMicrophone {
		t.Fatalf("skipped = %v", state.Skipped)
	}
	if state.Completed {
		t.Fatal("skipping one step completed the wizard")
	}
	for _, step := range state.Done {
		if step == wizard.StepMicrophone {
			t.Fatal("a skipped step is also in done")
		}
	}
}

func TestSkipAllSuppressesAutoOpen(t *testing.T) {
	path := tempState(t)
	service := newService(t, path)
	_ = service.Open()
	if err := service.SkipAll(); err != nil {
		t.Fatalf("skip all: %v", err)
	}
	data := activeModelServers()
	if service.AutoOpen(data) {
		t.Fatal("skip all must suppress auto-open")
	}
	reloaded := newService(t, path)
	if !reloaded.State().SkippedAll {
		t.Fatal("skip all was not persisted")
	}
	if reloaded.AutoOpen(data) {
		t.Fatal("a reloaded skip-all state still auto-opened")
	}
}

func TestClosePersistsAndOpenResumes(t *testing.T) {
	path := tempState(t)
	service := newService(t, path)
	_ = service.Open()
	_ = service.Next()
	_ = service.Next() // hotkey
	if err := service.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if service.Active() {
		t.Fatal("close left the wizard active")
	}
	reloaded := newService(t, path)
	if reloaded.Step() != wizard.StepHotkey {
		t.Fatalf("reloaded step = %q, want hotkey", reloaded.Step())
	}
	if reloaded.Active() {
		t.Fatal("a closed wizard reopened itself")
	}
	if err := reloaded.Open(); err != nil {
		t.Fatalf("open: %v", err)
	}
	if !reloaded.Active() || reloaded.Step() != wizard.StepHotkey {
		t.Fatalf("reopened at %q active=%v", reloaded.Step(), reloaded.Active())
	}
}

func TestRestartClearsProgress(t *testing.T) {
	service := newService(t, tempState(t))
	_ = service.Open()
	_ = service.Next()
	_ = service.SkipStep()
	_ = service.DeferModel("medium")
	_ = service.MarkTry(true)
	if err := service.Restart(); err != nil {
		t.Fatalf("restart: %v", err)
	}
	state := service.State()
	if state.Step != wizard.StepWelcome || !state.Active {
		t.Fatalf("state = %+v", state)
	}
	if len(state.Done) != 0 || len(state.Skipped) != 0 {
		t.Fatalf("restart kept progress: %+v", state)
	}
	if state.DeferredModel != "" || state.TryPassed || state.Completed {
		t.Fatalf("restart kept terminal state: %+v", state)
	}
}

func TestCorruptStateFileFallsBackAndKeepsTheFile(t *testing.T) {
	path := tempState(t)
	if err := writeFile(path, "{not json"); err != nil {
		t.Fatal(err)
	}
	var logs []string
	service := wizard.Open(wizard.NewFileStore(path), wizard.Options{
		Now:   fixedNow,
		OnLog: func(message string) { logs = append(logs, message) },
	})
	if !service.Fresh() {
		t.Fatal("a corrupt state file must be treated as fresh")
	}
	if service.Step() != wizard.StepWelcome {
		t.Fatalf("step = %q", service.Step())
	}
	if len(logs) == 0 {
		t.Fatal("a corrupt state file was not logged")
	}
	if !fileExists(path + ".bak") {
		t.Fatal("the corrupt state file was not backed up")
	}
}

// failingStore records saves and fails them.
type failingStore struct {
	state wizard.State
	saves int
}

func (s *failingStore) Load() (wizard.State, bool, error) { return wizard.State{}, false, nil }
func (s *failingStore) Save(state wizard.State) error {
	s.saves++
	s.state = state
	return errors.New("read-only state directory")
}

func TestSaveFailureKeepsInMemoryState(t *testing.T) {
	store := &failingStore{}
	service := wizard.Open(store, wizard.Options{Now: fixedNow})
	if err := service.Next(); err == nil {
		t.Fatal("next hid the save failure")
	}
	if service.Step() != wizard.StepMicrophone {
		t.Fatalf("step = %q, want the in-memory advance", service.Step())
	}
	if store.saves != 1 {
		t.Fatalf("saves = %d", store.saves)
	}
}

func TestClearDeferredForgetsTheDeferredModel(t *testing.T) {
	service := newService(t, tempState(t))
	_ = service.Open()
	_ = service.Next()
	_ = service.Next()
	_ = service.Next()
	_ = service.Next()
	_ = service.DeferModel("medium")
	if err := service.ClearDeferred(); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got := service.State().DeferredModel; got != "" {
		t.Fatalf("deferred model = %q", got)
	}
}

func TestAutoOpenRules(t *testing.T) {
	active := activeModelServers()
	none := noModelServers()
	cases := []struct {
		name      string
		prepare   func(*wizard.Service)
		data      wizard.Data
		wantOpen  bool
		callTwice bool
	}{
		{name: "fresh with an active model", data: active, wantOpen: true},
		{name: "fresh with an unknown model", data: wizard.Data{}, wantOpen: true},
		{name: "fresh already prompted", prepare: func(s *wizard.Service) { _ = s.Open(); _ = s.Close() }, data: active},
		{name: "not fresh, no active model", prepare: func(s *wizard.Service) { _ = s.Open(); _ = s.Close() }, data: none, wantOpen: true},
		{name: "not fresh, active model", prepare: func(s *wizard.Service) { _ = s.Open(); _ = s.Close() }, data: active},
		{name: "completed", prepare: func(s *wizard.Service) {
			_ = s.Open()
			for range wizard.AllSteps {
				_ = s.Next()
			}
		}, data: none},
		{name: "skipped all", prepare: func(s *wizard.Service) { _ = s.Open(); _ = s.SkipAll() }, data: none},
		{name: "already active", prepare: func(s *wizard.Service) { _ = s.Open() }, data: active},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			service := newService(t, tempState(t))
			if tc.prepare != nil {
				tc.prepare(service)
			}
			if got := service.AutoOpen(tc.data); got != tc.wantOpen {
				t.Fatalf("AutoOpen = %v, want %v", got, tc.wantOpen)
			}
		})
	}
}

func TestAutoOpenPersistsPrompted(t *testing.T) {
	path := tempState(t)
	service := newService(t, path)
	if !service.AutoOpen(wizard.Data{}) {
		t.Fatal("a fresh wizard did not auto-open")
	}
	if !service.State().Prompted {
		t.Fatal("auto-open did not record prompted")
	}
	reloaded := newService(t, path)
	if reloaded.AutoOpen(wizard.Data{}) {
		t.Fatal("a prompted wizard auto-opened again")
	}
}

func TestStateJSONCarriesNoCredentialsOrSpeech(t *testing.T) {
	service := newService(t, tempState(t))
	_ = service.Open()
	_ = service.MarkTry(true)
	data, err := json.Marshal(service.State())
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, forbidden := range []string{"token", "fingerprint", "transcript", "text", "device_token"} {
		if strings.Contains(strings.ToLower(text), forbidden) {
			t.Fatalf("state JSON contains %q: %s", forbidden, text)
		}
	}
}

func activeModelServers() wizard.Data {
	models := &structModelView
	return wizard.Data{
		Servers: serversViewWith(models),
		Server:  trust.Status{State: trust.StateOnline, Model: "small", ModelState: "loaded"},
	}
}
