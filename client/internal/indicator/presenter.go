package indicator

import (
	"errors"
	"fmt"
	"image"
)

// Presenter presents frames and owns the platform window. Implementations must
// be safe for concurrent Show/Hide/Close calls from the indicator loop, and
// Show and Hide must never block on the platform event loop.
type Presenter interface {
	// Name identifies the backend for logs ("layer_shell", "x11", "win32").
	Name() string
	// Scale is the output scale factor, so the indicator can render crisp
	// pixels instead of upscaling. Fractional factors are allowed (1.25 for a
	// 125% desktop).
	Scale() float64
	// Show presents a premultiplied RGBA frame. Input is the clickable region
	// in frame pixels; the zero rectangle means the whole frame passes clicks
	// through.
	Show(frame *image.RGBA, input image.Rectangle)
	// Hide removes the window from the screen without closing it.
	Hide()
	// Close destroys the window and releases platform resources.
	Close()
	// Clicks delivers stop-button presses.
	Clicks() <-chan struct{}
}

// Factories construct presenters. They are injected so backend selection is
// testable without a display.
type Factories struct {
	LayerShell func() (Presenter, error)
	X11        func() (Presenter, error)
	Windows    func() (Presenter, error)
}

// Environment describes what the current session can offer. LayerShell means a
// Wayland session, where the layer-shell presenter confirms the protocol is
// actually advertised. X11 means DISPLAY is set (including XWayland). Windows
// means the Client runs on a Windows desktop, which is decisive: stray display
// environment variables do not override it.
type Environment struct {
	LayerShell bool
	X11        bool
	Windows    bool
}

// ErrNoBackend means auto selection found no usable presenter.
var ErrNoBackend = errors.New("no indicator backend available")

// ErrUnsupported means the platform has no presenter yet.
var ErrUnsupported = errors.New("indicator presenter is not implemented on this platform")

// Open selects and constructs the configured presenter. It never silently
// falls back from an explicitly pinned backend. auto uses the Win32 window on
// Windows and otherwise tries layer-shell, then X11. A nil presenter with a nil
// error means the indicator is off.
func Open(configured string, env Environment, factories Factories) (Presenter, error) {
	switch configured {
	case "", "auto":
		// The host OS decides on Windows: a stray DISPLAY or
		// WAYLAND_DISPLAY from a bundled X server must not move the pill into
		// it. There is no fallback to another platform's window.
		if env.Windows {
			presenter, err := makePresenter("win32", factories.Windows)
			if err != nil {
				return nil, fmt.Errorf("indicator disabled: %w", err)
			}
			return presenter, nil
		}
		var errs []error
		if env.LayerShell {
			presenter, err := makePresenter("layer_shell", factories.LayerShell)
			if err == nil {
				return presenter, nil
			}
			errs = append(errs, err)
		}
		if env.X11 {
			presenter, err := makePresenter("x11", factories.X11)
			if err == nil {
				return presenter, nil
			}
			errs = append(errs, err)
		}
		if len(errs) > 0 {
			return nil, fmt.Errorf("indicator disabled: %w", errors.Join(errs...))
		}
		return nil, ErrNoBackend
	case "layer_shell":
		if !env.LayerShell {
			return nil, errors.New("indicator disabled: layer_shell requested but no Wayland session is available")
		}
		presenter, err := makePresenter("layer_shell", factories.LayerShell)
		if err != nil {
			return nil, fmt.Errorf("indicator disabled: %w", err)
		}
		return presenter, nil
	case "x11":
		if !env.X11 {
			return nil, errors.New("indicator disabled: x11 requested but DISPLAY is not set")
		}
		presenter, err := makePresenter("x11", factories.X11)
		if err != nil {
			return nil, fmt.Errorf("indicator disabled: %w", err)
		}
		return presenter, nil
	case "off":
		return nil, nil
	default:
		return nil, fmt.Errorf("indicator disabled: unknown backend %q", configured)
	}
}

func makePresenter(name string, factory func() (Presenter, error)) (Presenter, error) {
	if factory == nil {
		return nil, fmt.Errorf("%s presenter is unavailable", name)
	}
	presenter, err := factory()
	if err != nil {
		return nil, fmt.Errorf("%s presenter: %w", name, err)
	}
	if presenter == nil {
		return nil, fmt.Errorf("%s presenter returned nothing", name)
	}
	return presenter, nil
}
