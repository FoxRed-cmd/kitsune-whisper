package hotkey

import (
	"context"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/cycle"

	xhotkey "golang.design/x/hotkey"
)

// Options configures a global-hotkey Source.
type Options struct {
	// Hotkey is the toggle / hold-to-talk spec, e.g. "Ctrl+Shift+Space".
	Hotkey string
	// CancelHotkey is the cancel spec, e.g. "Esc".
	CancelHotkey string
	// Mode selects toggle or hold-to-talk behavior for the main hotkey.
	Mode Mode
	// Backend is "auto", "portal", or "x11"; empty means "auto". It is honored
	// on Linux and ignored where the desktop has a single native mechanism.
	Backend string
	// AppID is the stable application id the GlobalShortcuts portal is told to
	// associate with the Client.
	AppID string
	// Retry bounds the startup registration retries.
	Retry RetryPolicy
	// OnRetry is an optional hook invoked for each failed registration.
	OnRetry func(attempt int, err error)
	// OnLog receives human-readable progress; nil is silent.
	OnLog func(string)
}

// Source drives Dictation-cycle triggers from a desktop input mechanism: a
// global hotkey, the GlobalShortcuts portal, or nothing when the External
// trigger's control socket is the only option.
type Source interface {
	// Run emits triggers until ctx ends, returning nil on a clean shutdown.
	Run(ctx context.Context, triggers chan<- cycle.Trigger) error
}

// New builds the Source for the configured backend, resolving the session and
// portal on Linux.
func New(opts Options) (Source, error) {
	if opts.Backend == "" {
		opts.Backend = "auto"
	}
	return newPlatformSource(opts)
}

// keySource grabs the configured global hotkeys through golang.design/x/hotkey.
// It is the thin desktop adapter: the edge gating lives in Reducer, which is
// tested.
type keySource struct {
	main    *xhotkey.Hotkey
	cancel  *xhotkey.Hotkey
	mode    Mode
	retry   RetryPolicy
	onRetry func(int, error)
}

// newKeySource parses the hotkey specs and builds a keySource. It does not touch
// the desktop until Run registers the hotkeys.
func newKeySource(opts Options) (*keySource, error) {
	mods, key, err := ParseSpec(opts.Hotkey)
	if err != nil {
		return nil, err
	}
	cancelMods, cancelKey, err := ParseSpec(opts.CancelHotkey)
	if err != nil {
		return nil, err
	}
	return &keySource{
		main:    xhotkey.New(mods, key),
		cancel:  xhotkey.New(cancelMods, cancelKey),
		mode:    opts.Mode,
		retry:   opts.Retry,
		onRetry: opts.OnRetry,
	}, nil
}

// Run registers the hotkeys -- retrying while the display is not ready -- then
// pumps key edges into triggers until ctx ends. It returns nil on clean
// shutdown, or the registration error if the retry policy gives up.
func (s *keySource) Run(ctx context.Context, triggers chan<- cycle.Trigger) error {
	if err := s.register(ctx); err != nil {
		return err
	}
	defer func() {
		_ = s.main.Unregister()
		_ = s.cancel.Unregister()
	}()

	reducer := NewReducer(s.mode)
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.main.Keydown():
			emit(ctx, triggers, reducer, Main, Down)
		case <-s.main.Keyup():
			emit(ctx, triggers, reducer, Main, Up)
		case <-s.cancel.Keydown():
			emit(ctx, triggers, reducer, Cancel, Down)
		case <-s.cancel.Keyup():
			emit(ctx, triggers, reducer, Cancel, Up)
		}
	}
}

func (s *keySource) register(ctx context.Context) error {
	return registerWithRetry(ctx, func() error {
		// Re-register from a clean slate: a previous attempt may have grabbed
		// the main hotkey before the cancel grab failed, and Register rejects a
		// combination that is already registered.
		_ = s.main.Unregister()
		_ = s.cancel.Unregister()
		if err := s.main.Register(); err != nil {
			return err
		}
		if err := s.cancel.Register(); err != nil {
			_ = s.main.Unregister()
			return err
		}
		return nil
	}, s.retry, s.onRetry)
}

// emit feeds one key edge to the reducer and forwards any resulting trigger.
func emit(ctx context.Context, triggers chan<- cycle.Trigger, r *Reducer, slot Slot, edge Edge) {
	trigger, ok := r.Feed(slot, edge)
	if !ok {
		return
	}
	select {
	case triggers <- trigger:
	case <-ctx.Done():
	}
}
