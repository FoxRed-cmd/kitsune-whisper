// Package wizard is the Client's first-run setup flow.
//
// It is a pure state machine and view model: it owns a small progress file next
// to the effective kitsune.yaml and translates itself from the i18n catalogs.
// It deliberately imports no dictation, runtime, shell, microphone, or hotkey
// package, so its tests build without cgo, X11, a display, or audio hardware.
// The desktop shell maps its intents onto the existing services.
package wizard

import (
	"fmt"
	"sync"
	"time"
)

// Step names one screen of the Setup wizard.
type Step string

const (
	// StepWelcome shows the Client and Server status.
	StepWelcome Step = "welcome"
	// StepMicrophone selects an input device and shows a live level.
	StepMicrophone Step = "microphone"
	// StepHotkey records the dictation hotkey and the trigger mode.
	StepHotkey Step = "hotkey"
	// StepHardware chooses automatic, CPU, or CUDA.
	StepHardware Step = "hardware"
	// StepModel recommends and downloads a model.
	StepModel Step = "model"
	// StepServer creates the Local profile and optionally pairs a Remote one.
	StepServer Step = "server"
	// StepTry runs one real Dictation cycle.
	StepTry Step = "try"
)

// SetupSteps are the six skippable setup steps, in order.
var SetupSteps = []Step{
	StepWelcome,
	StepMicrophone,
	StepHotkey,
	StepHardware,
	StepModel,
	StepServer,
}

// AllSteps are the setup steps plus the final try-it step.
var AllSteps = []Step{
	StepWelcome,
	StepMicrophone,
	StepHotkey,
	StepHardware,
	StepModel,
	StepServer,
	StepTry,
}

// stateVersion is the schema version of wizard.json.
const stateVersion = 1

// State is the persisted progress of the Setup wizard. It intentionally holds
// no credentials, certificate pins, or speech content.
type State struct {
	Version       int       `json:"version"`
	Active        bool      `json:"active"`
	Step          Step      `json:"step"`
	Done          []Step    `json:"done,omitempty"`
	Skipped       []Step    `json:"skipped,omitempty"`
	SkippedAll    bool      `json:"skipped_all,omitempty"`
	Completed     bool      `json:"completed,omitempty"`
	Prompted      bool      `json:"prompted,omitempty"`
	DeferredModel string    `json:"deferred_model,omitempty"`
	TryPassed     bool      `json:"try_passed,omitempty"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// Store persists one wizard State. *FileStore implements it.
type Store interface {
	// Load returns the stored state and whether a state file existed.
	Load() (State, bool, error)
	// Save atomically replaces the stored state.
	Save(State) error
}

// Options configures the service.
type Options struct {
	// Now supplies UpdatedAt; nil uses time.Now.
	Now func() time.Time
	// OnLog receives corrupt-file and save-failure messages; nil is silent.
	OnLog func(string)
}

// Service owns the wizard state and persists every transition.
type Service struct {
	store Store
	opts  Options

	mu    sync.Mutex
	state State
	fresh bool
}

// Open loads the wizard progress. The store performs at most one small file
// read; a corrupt or unreadable file starts a fresh wizard instead of failing
// the Client.
func Open(store Store, opts Options) *Service {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	service := &Service{store: store, opts: opts}
	if store == nil {
		service.fresh = true
		service.state = service.initialLocked()
		return service
	}
	state, existed, err := store.Load()
	if err != nil {
		service.logf("could not read the setup progress: %v", err)
		service.fresh = true
		service.state = service.initialLocked()
		return service
	}
	if !existed {
		service.fresh = true
		service.state = service.initialLocked()
		return service
	}
	if state.Version != 0 && state.Version != stateVersion {
		service.logf("unsupported setup progress version %d; starting over", state.Version)
		service.fresh = true
		service.state = service.initialLocked()
		return service
	}
	service.state = normalize(state)
	return service
}

func (s *Service) initialLocked() State {
	return State{Version: stateVersion, Step: StepWelcome, UpdatedAt: s.now()}
}

// normalize repairs a loaded state: unknown steps fall back to the first step,
// duplicate entries are removed, a completed wizard resumes on the try step,
// and the wizard starts hidden so a restart never forces it open. The user can
// reopen it from Settings.
func normalize(state State) State {
	state.Version = stateVersion
	state.Active = false
	if !validStep(state.Step) {
		state.Step = StepWelcome
	}
	state.Done = uniqueSteps(state.Done)
	state.Skipped = uniqueSteps(state.Skipped)
	for _, step := range state.Done {
		state.Skipped = removeStep(state.Skipped, step)
	}
	for _, step := range state.Skipped {
		state.Done = removeStep(state.Done, step)
	}
	if state.Completed {
		state.SkippedAll = false
		state.Step = StepTry
	}
	if state.SkippedAll {
		state.Completed = false
	}
	return state
}

func validStep(step Step) bool {
	for _, candidate := range AllSteps {
		if candidate == step {
			return true
		}
	}
	return false
}

func uniqueSteps(steps []Step) []Step {
	if len(steps) == 0 {
		return nil
	}
	seen := map[Step]bool{}
	out := make([]Step, 0, len(steps))
	for _, step := range steps {
		if !validStep(step) || seen[step] {
			continue
		}
		seen[step] = true
		out = append(out, step)
	}
	return out
}

// State returns a copy of the current progress.
func (s *Service) State() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneState(s.state)
}

// Fresh reports whether no readable progress file existed at Open.
func (s *Service) Fresh() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.fresh
}

// Active reports whether the wizard is currently shown.
func (s *Service) Active() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Active
}

// Step returns the current screen.
func (s *Service) Step() Step {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.state.Step
}

// Open shows the wizard at the saved step.
func (s *Service) Open() error {
	return s.mutate(func(state *State) {
		state.Active = true
		state.Prompted = true
	})
}

// Close hides the wizard and keeps the progress.
func (s *Service) Close() error {
	return s.mutate(func(state *State) {
		state.Active = false
	})
}

// Next records the current setup step as done and advances. From the final
// try-it step it completes the wizard.
func (s *Service) Next() error {
	return s.mutate(func(state *State) {
		if state.Step == StepTry {
			complete(state)
			return
		}
		markDone(state, state.Step)
		state.Step = nextStep(state.Step)
		state.Active = true
	})
}

// Back steps backwards without erasing completed work. The first step is a
// no-op.
func (s *Service) Back() error {
	return s.mutate(func(state *State) {
		if index := stepIndex(state.Step); index > 0 {
			state.Step = AllSteps[index-1]
		}
	})
}

// SkipStep records the current step as skipped and advances. Skipping the
// final step completes the wizard.
func (s *Service) SkipStep() error {
	return s.mutate(func(state *State) {
		if state.Step == StepTry {
			markSkipped(state, state.Step)
			state.Completed = true
			state.SkippedAll = false
			state.Active = false
			return
		}
		markSkipped(state, state.Step)
		state.Step = nextStep(state.Step)
		state.Active = true
	})
}

// SkipAll ends the wizard and suppresses automatic reopening. Settings can
// still reopen it.
func (s *Service) SkipAll() error {
	return s.mutate(func(state *State) {
		state.SkippedAll = true
		state.Completed = false
		state.Active = false
	})
}

// Restart clears every progress record and starts at the welcome step.
func (s *Service) Restart() error {
	return s.mutate(func(state *State) {
		*state = State{Version: stateVersion, Step: StepWelcome, Active: true, Prompted: true}
	})
}

// GoTo shows one named step and persists it. Unknown steps are ignored.
func (s *Service) GoTo(step Step) error {
	if !validStep(step) {
		return fmt.Errorf("wizard: unknown step %q", step)
	}
	return s.mutate(func(state *State) {
		state.Step = step
		state.Active = true
	})
}

// DeferModel records a model the user chose to download later and advances to
// the Server step.
func (s *Service) DeferModel(name string) error {
	return s.mutate(func(state *State) {
		state.DeferredModel = name
		markSkipped(state, StepModel)
		if state.Step == StepModel {
			state.Step = StepServer
		}
		state.Active = true
	})
}

// ClearDeferred forgets a deferred model after one is downloaded and
// activated, so the launcher stops mentioning it.
func (s *Service) ClearDeferred() error {
	return s.mutate(func(state *State) {
		state.DeferredModel = ""
	})
}

// MarkTry records the outcome of the real Dictation cycle. A pass completes
// the wizard; a failure leaves it on the try step for another attempt.
func (s *Service) MarkTry(passed bool) error {
	return s.mutate(func(state *State) {
		state.TryPassed = passed
		if passed {
			markDone(state, StepTry)
			state.Completed = true
			state.SkippedAll = false
			state.Active = false
		}
	})
}

// Complete finishes the wizard without requiring a successful test.
func (s *Service) Complete() error {
	return s.mutate(func(state *State) {
		complete(state)
	})
}

// AutoOpen decides whether the wizard should open itself and, when it should,
// marks it active and prompted. A completed wizard still opens when the Client
// knows no model is active (ADR 0011); the shell calls it at most once per
// process, so a closed wizard is respected.
func (s *Service) AutoOpen(data Data) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.state.SkippedAll || s.state.Active {
		return false
	}
	presence := ModelPresence(data)
	presence.Fresh = s.fresh
	open := (presence.Fresh && !s.state.Prompted) || (presence.ModelKnown && !presence.ModelActive)
	if !open {
		return false
	}
	s.state.Active = true
	s.state.Prompted = true
	s.state.UpdatedAt = s.now()
	if s.store != nil {
		if err := s.store.Save(cloneState(s.state)); err != nil {
			s.logf("could not save the setup progress: %v", err)
		}
	}
	return true
}

func complete(state *State) {
	markDone(state, StepTry)
	state.Completed = true
	state.SkippedAll = false
	state.Active = false
}

func markDone(state *State, step Step) {
	if !validStep(step) {
		return
	}
	state.Skipped = removeStep(state.Skipped, step)
	if !containsStep(state.Done, step) {
		state.Done = append(state.Done, step)
	}
}

func markSkipped(state *State, step Step) {
	if !validStep(step) {
		return
	}
	state.Done = removeStep(state.Done, step)
	if !containsStep(state.Skipped, step) {
		state.Skipped = append(state.Skipped, step)
	}
}

func containsStep(steps []Step, step Step) bool {
	for _, candidate := range steps {
		if candidate == step {
			return true
		}
	}
	return false
}

func removeStep(steps []Step, step Step) []Step {
	out := steps[:0]
	for _, candidate := range steps {
		if candidate != step {
			out = append(out, candidate)
		}
	}
	return out
}

func stepIndex(step Step) int {
	for index, candidate := range AllSteps {
		if candidate == step {
			return index
		}
	}
	return 0
}

func nextStep(step Step) Step {
	index := stepIndex(step)
	if index+1 < len(AllSteps) {
		return AllSteps[index+1]
	}
	return StepTry
}

func (s *Service) mutate(change func(*State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := cloneState(s.state)
	change(&state)
	state.Version = stateVersion
	state.UpdatedAt = s.now()
	s.state = state
	if s.store == nil {
		return nil
	}
	if err := s.store.Save(cloneState(state)); err != nil {
		s.logf("could not save the setup progress: %v", err)
		return err
	}
	return nil
}

func cloneState(state State) State {
	state.Done = append([]Step(nil), state.Done...)
	state.Skipped = append([]Step(nil), state.Skipped...)
	return state
}

func (s *Service) now() time.Time {
	if s.opts.Now == nil {
		return time.Now()
	}
	return s.opts.Now()
}

func (s *Service) logf(format string, args ...any) {
	if s.opts.OnLog != nil {
		s.opts.OnLog(fmt.Sprintf(format, args...))
	}
}
