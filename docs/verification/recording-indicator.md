# Recording indicator manual verification (#41)

The frame model, renderer, click region, backend selection, and Input level are
covered by automated tests. The parts below depend on a real compositor, so they
are checked by hand on each desktop. Run the Client from a terminal with
`--verbose` so the phase transitions and the chosen backend appear in the log.

Setup: a running Server, a microphone, and `kitsune-client run`. Use a text
editor with the cursor in a document for the focus checks.

## Common checks (every desktop)

- [ ] Pressing the dictation hotkey shows the pill immediately; the cursor stays
      in the editor and the editor keeps keyboard focus.
- [ ] Speaking makes the seven bars rise and fall; silence keeps them low.
- [ ] Pressing the hotkey again (or the External trigger) switches to the
      travelling-wave processing state.
- [ ] The indicator fades (about 120 ms) just before the text is injected; the
      text lands where it was.
- [ ] Clicking the red stop button stops the cycle; no character is typed by the
      click itself.
- [ ] Clicking the transparent part of the pill passes the click through to the
      window underneath.
- [ ] Esc hides the indicator immediately, with no fade.
- [ ] An accidental tap (shorter than `min_recording_seconds`) shows the dim
      state for about 300 ms and inserts nothing.
- [ ] A failed Server request shows the red state for about 1.5 s, then fades;
      the utterance is spooled as before.
- [ ] An empty transcription fades without a red state and inserts nothing.
- [ ] `feedback.indicator: off` starts no window at all (check with a compositor
      window list); dictation and Earcons still work.
- [ ] A long utterance stays visible until the max-recording bound stops it.
- [ ] `kitsune-client record` and `transcribe-file` show no indicator.
- [ ] Exactly one warning appears in the log when the pinned backend is
      unavailable (`feedback.indicator: x11` with no `DISPLAY`); the Client keeps
      dictating.
- [ ] With `--verbose`, the log shows `indicator: recording`, `processing`,
      `delivering`, `finished`, and the matching cue lines.

## X11 (plain Xorg session)

- [ ] `client.feedback.indicator: x11` and `auto` both select the X11 window.
- [ ] The pill appears on the monitor that contains the focused window, 16 px
      from the top edge.
- [ ] With two monitors, focus windows on each in turn and start a cycle; the
      pill follows the focused monitor.
- [ ] On a HiDPI (Xft.dpi > 96) display the pill is crisp, not upscaled.
- [ ] The pill has no window decorations, taskbar entry, or Alt-Tab entry.
- [ ] It stays above tiled and borderless-fullscreen windows; note whether it
      stays above an exclusive-fullscreen app (compositor-dependent).
- [ ] With focus-follows-mouse, note whether resting the pointer on the pill
      moves focus; the rest of the pill still passes clicks through.

## Hyprland (Wayland, wlroots)

- [ ] `auto` and `layer_shell` both select `recording indicator: layer_shell
      backend`.
- [ ] The pill floats above tiled windows without becoming a managed window and
      does not reserve screen space.
- [ ] The pill is sharp on a scale-2 output and correctly sized.
- [ ] With two outputs, confirm the pill appears on the documented primary/first
      output (Wayland clients cannot follow focus).
- [ ] Only the stop button is clickable; keyboard focus never enters the pill.
- [ ] Fullscreen a window and start a cycle; note whether the indicator remains
      visible.

## Sway (Wayland, wlroots, layer-shell v4)

- [ ] Same checks as Hyprland. Confirm the v4 protocol path works (the binding
      caps the advertised version at 5).

## KDE Plasma (Wayland)

- [ ] `auto` selects layer-shell.
- [ ] The indicator is above active fullscreen windows.
- [ ] HiDPI and multi-output behaviour as above.

## GNOME (Wayland, XWayland)

- [ ] XWayland is running; `auto` falls back to the X11 window.
- [ ] The pill appears without taking focus and passes clicks through except on
      the stop button.
- [ ] Fullscreen stacking follows Mutter's override-redirect placement; note the
      result.

## Windows (deferred, #42)

When the Win32 layered-window presenter lands, run the same common checks plus:

- [ ] The indicator appears on the monitor with the foreground window.
- [ ] Per-monitor V2 DPI gives a crisp frame at each monitor's scale.
- [ ] It is hidden while an exclusive-fullscreen app owns the display.
- [ ] No taskbar or Alt-Tab entry; the stop button does not activate the window.
