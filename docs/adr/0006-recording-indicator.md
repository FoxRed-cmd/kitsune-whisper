# The Recording indicator is per-platform windows over one shared renderer

**Status:** accepted

The Client had no visual feedback: a global hotkey started capture, and the only
signals were an optional Earcon and the log. The **Recording indicator** fills
that gap — a small always-on-top pill that shows the Dictation cycle's phase and
the live Input level, with a clickable stop button.

The requirement is narrow but unusual: a client-positioned, undecorated,
per-pixel-alpha window that never takes focus, floats above normal windows, and
repaints at ~30 fps while recording. That is exactly what no cross-platform GUI
toolkit exposes.

The indicator is therefore built **in-process** as a `cycle.Feedback` adapter:

- One **shared renderer** (pure Go) composes each frame into an `image.RGBA`:
  the pill, the level bars, and the stop button. Animation and level smoothing
  live here and are unit-tested without a display.
- Three thin **presenters** own only window creation and blitting: Win32
  (`CreateWindowExW` + `UpdateLayeredWindow`), X11 (`xgb`, depth-32 visual,
  override-redirect, `XShape` input region), and Wayland (`zwlr_layer_shell_v1`
  + `wl_shm`, generated binding on top of `neurlang/wayland`).
- The presenter is chosen at startup from the session: layer-shell when the
  protocol is advertised, otherwise the X11 presenter when `DISPLAY` is set,
  otherwise nothing. `client.feedback.indicator: auto|layer_shell|x11|off` pins
  it. Failure to create the window logs one warning and degrades to the old
  silent behaviour; it never stops dictation.
- GNOME on Wayland is served by the **X11 presenter over XWayland**: Mutter
  places and stacks override-redirect windows itself (overlay layer, above
  fullscreen), so no GNOME-specific component is needed.
- The Windows presenter is deferred until the Client is exercised on Windows;
  the package builds with an inert presenter there.

## Considered options

- **(a) Chosen — shared renderer plus three native presenters.** ~1.2k lines of
  platform code, no new runtime dependencies, exact control over focus,
  stacking, transparency, and hit-testing. Trade-off: three presenters to test,
  and a generated Wayland protocol binding to own.
- **(b) A GUI toolkit (Fyne, Gio, GTK4 + gtk4-layer-shell, Wails).** Rejected:
  every toolkit creates a normal `xdg-shell` window on Wayland, which the
  compositor positions and decorates; only GTK with `gtk4-layer-shell` can be a
  layer surface, and that drags GTK4, cgo, and its main loop into a headless
  daemon. Even then the toolkit solves only drawing — the trivial part for a
  text-free pill — while focus, click-through, and stacking remain
  platform-specific.
- **(c) `org.freedesktop.Notifications`.** Rejected: no live level meter; on
  GNOME every `replaces_id` update restarts the banner animation and plays a
  sound, actions are hidden until hover, and expanding the banner grabs keyboard
  focus — hostile to dictation.
- **(d) Compositor-specific OSD (`hyprctl notify`, …).** Rejected: Hyprland-only,
  no level meter, and the look is the compositor's, not the Client's.

## Consequences

- The Client gains `client/internal/indicator` and its presenters; `mic.Recorder`
  exposes an atomic Input level; `cycle` gains `CueProcessing`, `CueCancelled`,
  and `CueTooShort` so the indicator can tell "analysing" from "delivered" and
  "cancelled".
- The Wayland presenter is the largest and riskiest piece: it needs a generated
  `wlr-layer-shell-unstable-v1` binding, `wl_shm` buffers, configure/ack, frame
  callbacks, and an input region. Sway advertises v4, so the binding is capped
  at `min(advertised, 5)`.
- No text in the indicator, so no font stack and no text rendering dependency.
  Adding a timer or status label later is a new decision.
- Clicks never activate the indicator: alpha-0 hit-testing on Windows, an
  override-redirect window that is never focused on X11, and
  `keyboard_interactivity = none` plus an input region on Wayland.
- Known limits, documented in [`docs/wayland.md`](../wayland.md): no overlay over
  true fullscreen-exclusive apps on Windows; on Wayland the pill goes to the
  primary output because clients cannot see the focused monitor; X11
  focus-follows-mouse setups can redirect focus on hover.
- The indicator is silent; Earcons stay independent and off by default.

Implementation is tracked in #41; the deferred Windows presenter is #42.
