# Go client capabilities: global hotkey, audio capture, text injection, Wayland, packaging

## Scope

This document surveys mature, maintained Go libraries for the three hard client
capabilities of a desktop voice-typing client, on **Windows** and **Linux/X11**,
and describes what **Wayland** requires. It covers:

- **Global hotkey** (toggle *and* hold-to-talk): `golang.design/x/hotkey`,
  `gohook`, `robotgo` — permission requirements, key names, conflict handling.
- **Audio capture**: `malgo` (miniaudio) vs `portaudio` vs `oto` — device
  enumeration, sample-rate handling, cgo and cross-compilation.
- **Text injection**: `golang.design/x/clipboard` plus synthetic paste; per-OS
  paste keystroke; clipboard-restore pitfalls; Linux terminal paste.
- **Wayland**: what `ydotool` / `wtype` require, and whether a global hotkey is
  even possible.
- **Packaging**: single-binary cross-OS builds and running as a background
  daemon/service from Go.

Every claim is tied to the primary source that owns it. Versions and dates come
from the Go module proxy (`proxy.golang.org`, the authoritative source for module
metadata). "As of" dates reflect the module's latest tagged release.

> Notation: **Win** = Windows, **X11** = Linux/X11, **WL** = Wayland.

---

## 1. Global hotkey (toggle + hold)

### 1.1 `golang.design/x/hotkey`

- **What it is:** "cross platform hotkey package in Go... Global hotkey
  registration without focus on a window", supporting macOS, Linux (X11) and
  Windows only ([README](https://github.com/golang-design/hotkey/blob/main/README.md)).
- **Version:** `v0.6.4`, tagged 2026-09-26
  ([proxy.golang.org](https://proxy.golang.org/golang.design/x/hotkey/@latest)).
- **Windows mechanism:** calls the Win32 `RegisterHotKey` API
  (`hotkey_windows.go`:
  [source](https://github.com/golang-design/hotkey/blob/main/hotkey_windows.go)).
  Modifier constants map to `MOD_ALT=0x1`, `MOD_CONTROL=0x2`, `MOD_SHIFT=0x4`,
  `MOD_WIN=0x8`; keys map to Win32 virtual-key codes (`KeyA=0x41`, `KeyF1=0x70`,
  media/volume keys `KeyMediaPlayPause=0xB3` etc.), documented against
  [Microsoft's RegisterHotKey](https://docs.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-registerhotkey)
  and [virtual-key codes](https://docs.microsoft.com/en-us/windows/win32/inputdev/virtual-key-codes).
- **Hold-to-talk limitation (important):** Win32 `RegisterHotKey` only delivers a
  key-down message. The library *emulates* key-up by polling
  `GetAsyncKeyState` on a 100 Hz ticker (`time.NewTicker(time.Second / 100)`), and
  only after the key-up channel is drained does `auto`/`keyupIn` move on
  (`hotkey_windows.go`
  [source](https://github.com/golang-design/hotkey/blob/main/hotkey_windows.go)).
  So push-to-talk is synthesised, not native, and its release latency is bounded
  by the poll interval.
- **Linux mechanism:** X11 `XGrabKey` via a private invisit window; synchronous
  grab so conflicts are returned as errors rather than crashing under Xlib's
  default `BadAccess` handler. Uses a package-global `grabMu`
  ([ctx7/docs, `hotkey-x11.go`](https://github.com/golang-design/hotkey/blob/main/_autodocs/api-reference/hotkey-x11.md)).
- **Permissions:** none on Windows or X11. On macOS a CGEventTap requires
  Accessibility/Input Monitoring and `Register` fails without it (README).
- **Key names / X11 quirks:** on X11 the API uses X keycodes, not names; the
  README warns that some keys map to multiple Mod keys (e.g. `Ctrl+Alt+S` may
  need `Ctrl+Mod2+Mod4+S`) and that with AutoRepeat enabled the server emits
  continuous KeyUp while KeyDown continues
  ([README](https://github.com/golang-design/hotkey/blob/main/README.md)).
- **Conflict handling:** X11 `XGrabKey` returns `errRegisterFailed` if another
  client already grabbed the combination; Windows surfaces the underlying
  `RegisterHotKey` failure wrapped as `errRegisterFailed`
  ([xtodosix docs](https://github.com/golang-design/hotkey/blob/main/_autodocs/api-reference/hotkey-x11.md),
  [hotkey_windows.go](https://github.com/golang-design/hotkey/blob/main/hotkey_windows.go)).
- **Wayland:** not supported (X11-only per README). Under a Wayland session this
  only works if `DISPLAY`/XWayland handles it.

### 1.2 `gohook` (`github.com/robotn/gohook`)

- **What it is:** "Go global keyboard and mouse listener hook", based on
  [`libuiohook`](https://github.com/kwhat/libuiohook)
  ([README](https://github.com/robotn/gohook/blob/master/README.md)).
- **Version:** `v0.50.0`, tagged 2026-10-04
  ([proxy.golang.org](https://proxy.golang.org/github.com/robotn/gohook/@latest)) — actively maintained.
- **Semantics:** it *observes* low-level events (`KeyDown`/`KeyUp`/`KeyHold`,
  `MouseDown`/`MouseUp`/`MouseHold`, wheel) on a channel; it does **not** grab a
  hotkey, so there is no registration conflict — but it sees all keystrokes and
  must filter for the shortcut itself
  ([ctx7/docs](https://context7.com/robotn/gohook/llms.txt),
  [README](https://github.com/robotn/gohook/blob/master/README.md)).
- **Hold:** native `KeyHold`/`KeyUp` events make true push-to-talk feasible
  without the 100 Hz polling trick.
- **Linux requirements:** libuiohook needs X11 client libraries; robotgo lists
  the event-hook dependencies as "xcb, xkb, libxkbcommon"
  ([robotgo README](https://github.com/go-vgo/robotgo/blob/master/README.md)).
  gohook's own README points at
  [robotgo's requirements](https://github.com/go-vgo/robotgo#requirements) and
  documents a `purego` build tag.
- **Wayland:** gohook ships an experimental pure-Go Wayland backend
  (`wayland.go`), but its own source comment is explicit: *"Wayland deliberately
  has NO global keylogging/mouse-hooking primitive... It captures input only while
  a surface owned by this process has keyboard/pointer focus... TRUE global
  capture on Wayland requires the xdg-desktop-portal InputCapture / RemoteDesktop
  portals plus the libei (EI) protocol."*
  ([wayland.go](https://github.com/robotn/gohook/blob/master/wayland.go)).
  So gohook's Wayland backend is **not** a global hotkey source.

### 1.3 `robotgo` (`github.com/go-vgo/robotgo`)

- **What it is:** desktop automation (mouse/keyboard/screen/window) plus a global
  event listener ([README](https://github.com/go-vgo/robotgo/blob/master/README.md)).
- **Version:** `v1.2.1`, tagged 2026-10-04
  ([proxy.golang.org](https://proxy.golang.org/github.com/go-vgo/robotgo/@latest)).
- **Hotkey/hook path:** robotgo delegates global listening to `gohook`
  (`robotn/gohook`), so the same libuiohook semantics and Linux library
  requirements apply ([README](https://github.com/go-vgo/robotgo/blob/master/README.md)).
- **Default (cgo) Linux deps:** GCC, X11 with XTest (`libXtst`), `xsel`/`xclip`,
  `libpng`, and for the hook `xcb`, `xkb`, `libxkbcommon`
  ([README](https://github.com/go-vgo/robotgo/blob/master/README.md)).
- **Experimental cgo-free backends** (build tags, `CGO_ENABLED=0`): `win`, `mac`,
  `x11` (pure-Go X protocol/XTEST), `wayland` (wlroots protocols), `libei`
  (GNOME/KDE via `xdg-desktop-portal` RemoteDesktop), and `purego` as a shortcut.
  The Wayland tag needs a wlroots compositor exposing
  `zwlr_virtual_pointer_v1`, `zwp_virtual_keyboard_v1`, `zwlr_screencopy_v1`,
  `zwlr_foreign_toplevel_management_v1`; GNOME/KDE do not. The `libei` tag works
  on GNOME/KDE via the portal
  ([README](https://github.com/go-vgo/robotgo/blob/master/README.md)).
- **Key-name style:** string names (`"enter"`, `"i"`, `["alt","command"]`),
  which is convenient for injection but different from `hotkey`'s typed constants
  ([examples](https://github.com/go-vgo/robotgo/blob/master/examples/README.md)).
- **Caveat:** hotkey **registration** (grab) is not robotgo's model — it is an
  event observer like gohook. If you need an exclusive, failure-on-conflict grab,
  `golang.design/x/hotkey` is the only one of the three built for it.

### Capability summary (hotkey)

| Library | Version (date) | cgo | Win | X11 | WL | Toggle | Hold | Grab/conflict |
|---|---|---|---|---|---|---|---|---|
| `golang.design/x/hotkey` | v0.6.4 (2026-09-26) | no | ✅ RegisterHotKey | ✅ XGrabKey | ❌ | ✅ | emulated (Win 100 Hz poll); noisy under X11 AutoRepeat | ✅ returns error on conflict |
| `robotn/gohook` | v0.50.0 (2026-10-04) | yes on Linux (libuiohook) | ✅ | ✅ | ⚠️ focus-only | ✅ (via filtering) | ✅ native KeyHold | ❌ no grab (observer) |
| `go-vgo/robotgo` | v1.2.1 (2026-10-04) | yes by default; pure-Go tags | ✅ | ✅ | ✅ wlroots / libei tags | ✅ (via gohook) | ✅ (via gohook) | ❌ no grab (observer) |

---

## 2. Audio capture

### 2.1 `malgo` (`github.com/gen2brain/malgo`)

- **What it is:** Go bindings for [miniaudio](https://github.com/dr-soft/miniaudio):
  "cross-platform audio capture, playback, and streaming"
  ([README](https://github.com/gen2brain/malgo/blob/master/README.md)).
- **Version:** `v0.11.26`, tagged 2026-08-18
  ([proxy.golang.org](https://proxy.golang.org/github.com/gen2brain/malgo/@latest)).
- **cgo:** *"Requires cgo but does not require linking to anything on the
  Windows/macOS and it links only `-ldl` on Linux/BSDs."*
  ([README](https://github.com/gen2brain/malgo/blob/master/README.md)). So
  Windows builds still need a C compiler (e.g. MinGW) with `CGO_ENABLED=1`;
  cross-OS builds need a C cross-toolchain. There is **no pure-Go mode**.
- **Backends:** Windows (WASAPI, DirectSound, WinMM); Linux (PulseAudio, ALSA,
  JACK); BSDs; macOS; Android ([README](https://github.com/gen2brain/malgo/blob/master/README.md)).
- **Device enumeration:** `context.Devices(malgo.Capture)` returns `[]DeviceInfo`;
  `context.DeviceInfo(...)` gives formats and default status
  ([ctx7 / `_autodocs/api-reference/device-info.md`](https://github.com/gen2brain/malgo/blob/master/_autodocs/api-reference/device-info.md)).
- **Sample-rate handling:** `DeviceConfig.SampleRate = 0` means "device native
  rate", and *"miniaudio converts internally"*; `Resampling.Algorithm` selects
  `Linear` (default) or `Speex`. Negotiated values can differ from the request
  (`Device.CaptureInternalSampleRate()` vs `Device.SampleRate()`), e.g. under
  ALSA `dsnoop` resampling
  ([ctx7 / `_autodocs/configuration.md`, `device.md`](https://github.com/gen2brain/malgo/blob/master/_autodocs/configuration.md)).
- **Capture example:** `DeviceConfig.Capture.Format = FormatS16`,
  `Channels = 1`, `SampleRate = 44100`, callback appends `pSample` bytes; note the
  example sets `deviceConfig.Alsa.NoMMap = 1`
  ([capture example](https://github.com/gen2brain/malgo/blob/master/_examples/capture/capture.go)).
- **Verdict:** the most ergonomic capture option; miniaudio hides format/rate
  conversion and enumerates devices cleanly. Cost is cgo.

### 2.2 `portaudio` (`github.com/gordonklaus/portaudio`)

- **What it is:** Go bindings to the PortAudio C library
  ([package doc](https://pkg.go.dev/github.com/gordonklaus/portaudio)).
- **Version:** no tagged release; pseudo-version
  `v0.0.0-20260203164431-765aa7dfa631` (2026-02-03)
  ([proxy.golang.org](https://proxy.golang.org/github.com/gordonklaus/portaudio/@latest)).
- **cgo / build:** `#cgo pkg-config: portaudio-2.0`, and the README states you
  *"must first have the PortAudio development headers and libraries installed"*
  (e.g. `apt-get install portaudio19-dev`)
  ([portaudio.go](https://github.com/gordonklaus/portaudio/blob/master/portaudio.go),
  [README](https://github.com/gordonklaus/portaudio/blob/master/README.md)).
- **Capture + enumeration:** full-duplex/input streams; `DefaultInputDevice()`,
  `Devices()`, `DeviceInfo{Name, MaxInputChannels, DefaultSampleRate, ...}`,
  `OpenDefaultStream(numInputChannels, ...)`, blocking `Read()` or a callback
  ([portaudio.go](https://github.com/gordonklaus/portaudio/blob/master/portaudio.go)).
- **Sample rate:** caller-supplied `StreamParameters.SampleRate`; binding returns
  `Error`/`InvalidSampleRate`; PortAudio does not auto-resample, so you must
  negotiate/convert. `HighLatencyParameters`/`LowLatencyParameters` pick a rate
  from the device defaults ([portaudio.go](https://github.com/gordonklaus/portaudio/blob/master/portaudio.go)).
- **Verdict:** capable and battle-tested at the C level, but cgo + external
  system package + no tagged Go release make it heavier than `malgo` for this
  use case.

### 2.3 `oto` (`github.com/ebitengine/oto/v3`) — not a capture library

- **What it is:** *"A low-level library to play sound."* The README lists playback
  only; there is no capture/input API
  ([README](https://github.com/ebitengine/oto/blob/main/README.md)).
- **Version:** `v3.5.1`, tagged 2026-09-12
  ([proxy.golang.org](https://proxy.golang.org/github.com/ebitengine/oto/v3/@latest)).
- **cgo:** none on Windows/Linux; PulseAudio via pure-Go
  `github.com/jfreymuth/pulse`, ALSA fallback loads `libasound.so.2` at runtime;
  cross-compiling is `GOOS`-only
  ([README](https://github.com/ebitengine/oto/blob/main/README.md)).
- **Verdict:** excellent for *playback* (e.g. feedback tones), **unusable for
  microphone capture**. Listed here only to rule it out.

### Capability summary (audio)

| Library | Version (date) | Capture | cgo | Enumeration | Sample-rate | Cross-compile |
|---|---|---|---|---|---|---|
| `gen2brain/malgo` | v0.11.26 (2026-08-18) | ✅ | **yes** (no libs on Win, `-ldl` on Linux) | ✅ `Devices(Capture)` | 0 = native, miniaudio converts; Speex/Linear | needs C cross-toolchain |
| `gordonklaus/portaudio` | pseudo 2026-02-03 | ✅ input streams | **yes** + `pkg-config portaudio-2.0` | ✅ `Devices()`, `DefaultInputDevice()` | caller-set; no resampling | needs PortAudio dev libs + toolchain |
| `ebitengine/oto/v3` | v3.5.1 (2026-09-12) | ❌ playback only | no (Win/Linux) | n/a | n/a | easy (`GOOS`) |

---

## 3. Text injection (clipboard + synthetic paste)

The robust pattern is: **write the transcript to the clipboard, then synthesise
the paste keystroke** — rather than typing characters one by one (slow, layout
dependent). Two parts: a clipboard library and a key-injection library/tool.

### 3.1 Clipboard: `golang.design/x/clipboard`

- **Version:** `v0.11.0`, tagged 2026-09-26
  ([proxy.golang.org](https://proxy.golang.org/golang.design/x/clipboard/@latest)).
- **cgo-free on desktop**, including Linux: *"No Cgo on the desktop: no C compiler
  to build, no `libX11` or `libwayland` to run."*
  ([README](https://github.com/golang-design/clipboard/blob/main/README.md)).
- **Linux backend selection:** native Wayland via the **data-control** protocol
  when the compositor advertises `ext-data-control-v1` (GNOME ≥ 49, KDE, Sway,
  Hyprland, wlroots) or the wlroots `zwlr_data_control_manager_v1`; otherwise
  falls back to X11, which under a Wayland session means XWayland; GNOME before
  49 is reached through XWayland. With no display at all, `Init` returns an error
  ([README](https://github.com/golang-design/clipboard/blob/main/README.md),
  [wayland-support design](https://github.com/golang-design/clipboard/blob/main/specs/wayland-support.md)).
- **Ownership pitfall (X11/Wayland):** *"the process that writes to the clipboard
  owns the selection and serves its content to other applications on demand. Once
  the writing process exits, the data is gone — unless a clipboard manager is
  running... To keep written data available after your program exits, keep the
  process running... or rely on a clipboard manager."*
  ([README](https://github.com/golang-design/clipboard/blob/main/README.md)).
- **Windows Session 0 pitfall:** a Windows *service* runs in Session 0 with its
  own window station and therefore its own clipboard: `Write` succeeds but the
  logged-in user never sees it, and `Watch` never fires for the user's copies.
  The fix is to do clipboard work in a helper process inside the interactive
  session, started via `CreateProcessAsUser` with the token from
  `WTSQueryUserToken(WTSGetActiveConsoleSessionId())`, talking over IPC
  ([README](https://github.com/golang-design/clipboard/blob/main/README.md)).
- **Restore pitfall:** `Loops(n)` (auto-clear after N pastes / "paste once, then
  gone") works **only on X11 and Wayland**; on Windows and macOS the system keeps
  the copy and *"your program never hears about pastes"*, so it is silently
  ignored ([README](https://github.com/golang-design/clipboard/blob/main/README.md)).
  Practical consequence: a reliable "save current clipboard → inject → restore"
  flow is only truly observable on Linux; on Windows you cannot detect when the
  paste was consumed, so restoring on a timer risks clobbering the user's
  next copy. The safe options are (a) don't restore, or (b) restore after an
  explicit user action / fixed delay, accepting the race. `Write` returns a
  channel that fires when something else replaces your write, and the data is
  served after `Write` returns; do not wait on it to know the write finished
  ([README](https://github.com/golang-design/clipboard/blob/main/README.md)).

### 3.2 Synthetic paste keystroke

- **Per-OS shortcut:** **Windows:** `Ctrl+V`. **Linux GUI apps:** `Ctrl+V`.
  **Linux terminal emulators:** `Ctrl+Shift+V` — GNOME Terminal's documented
  default is *Paste = Shift+Ctrl+V* and *Copy = Shift+Ctrl+C*
  ([GNOME Help](https://help.gnome.org/users/gnome-terminal/stable/adv-keyboard-shortcuts.html.en)).
  (Most terminals, e.g. `konsole`/`xterm`-compatibles, share this convention, but
  it is a per-terminal setting; detect terminal focus if you need reliability.)
- **Windows / X11 injection:** `robotgo` `KeyTap("v", "ctrl")` / `Type` uses XTest
  on X11 and Win32 `SendInput` on Windows
  ([robotgo README](https://github.com/go-vgo/robotgo/blob/master/README.md),
  [examples](https://github.com/go-vgo/robotgo/blob/master/examples/README.md)).
  `gohook` is only a listener; it does not inject.
- **Wayland injection options:**
  - `wtype` requires the compositor to expose
    `zwp_virtual_keyboard_manager_v1`; `main.c` fails with *"Compositor does not
    support the virtual keyboard protocol"* otherwise. This is a wlroots protocol,
    so **GNOME and KDE do not support it**
    ([wtype `main.c`](https://github.com/atx/wtype/blob/master/main.c),
    [robotgo README](https://github.com/go-vgo/robotgo/blob/master/README.md)).
  - `ydotool` uses the kernel `uinput` framework, not Wayland: it works on X11,
    Wayland and even consoles, but `ydotoold` *"requires access to `/dev/uinput`.
    This usually requires root permissions"* and must run as a persistent
    background service
    ([ydotool README](https://github.com/ReimuNotMoe/ydotool/blob/master/README.md)).
  - Pure-Go `uinput` wrapper `github.com/bendahl/uinput` can create a virtual
    keyboard from Go; it likewise needs write access to `/dev/uinput` (udev rule
    or group membership)
    ([bendahl/uinput README](https://github.com/bendahl/uinput/blob/master/README.md)).
  - `robotgo`'s `libei` backend injects keyboard/mouse via the
    `xdg-desktop-portal` RemoteDesktop interface, so it works on **GNOME/KDE**
    (with user consent), and its `wayland` backend uses the wlroots virtual
    keyboard/pointer protocols
    ([robotgo README](https://github.com/go-vgo/robotgo/blob/master/README.md)).

---

## 4. Wayland: what is actually possible

### 4.1 Global hotkey on Wayland

- **Core Wayland has no global hotkey/keylogging primitive.** From gohook's
  Wayland source: *"Wayland deliberately has NO global keylogging/mouse-hooking
  primitive. A `wl_seat` only delivers `wl_keyboard` / `wl_pointer` events to a
  surface while THAT surface holds input focus. There is no XRecord equivalent."*
  ([wayland.go](https://github.com/robotn/gohook/blob/master/wayland.go)).
- **The sanctioned mechanism is the `xdg-desktop-portal` GlobalShortcuts portal.**
  It lets an app create a session and bind shortcuts that *"are activated
  regardless of the focused state of the application window"*, with
  `Activated`/`Deactivated` signals
  ([portal spec](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.GlobalShortcuts.html)).
  The spec's `BindShortcuts` presents a user-facing dialog (`preferred_trigger`
  is a hint) and `Activated`/`Deactivated` are distinct signals — which is the
  hook for **hold-to-talk**.
- **Backend availability:**
  - **GNOME 48+:** *"With GNOME 48, it is now possible for apps to register
    system-wide global shortcuts... This feature is fully supported by GNOME 48"*
    ([GNOME 48 release notes](https://release.gnome.org/48/developers/)); the
    backend landed in xdg-desktop-portal-gnome 48.rc ("Add global shortcuts portal
    backend") ([NEWS](https://github.com/gnome/xdg-desktop-portal-gnome/blob/main/NEWS)).
  - **KDE Plasma 5.27+:** *"Plasma on Wayland has also gained support for the
    Global Shortcuts portal. This allows apps on Wayland to offer a standardized
    user interface for setting and editing global shortcuts."*
    ([KDE Plasma 5.27 announcement](https://kde.org/announcements/plasma/5/5.27.0/)).
  - **wlroots compositors (Sway, Hyprland, …):** typically bind shortcuts in the
    compositor config, not via the portal; the portal's GlobalShortcuts backend
    is not universal there.
- **No Go library was found that wraps the GlobalShortcuts portal.** The
  plausible route is to speak D-Bus directly with `github.com/godbus/dbus/v5`
  (native pure-Go D-Bus client). This is an integration you would write, not a
  drop-in.
- **Alternatives that do not give you a real "global hotkey":**
  - Reading `/dev/input` (evdev): possible without a compositor, but needs root
    or `input` group membership, sees every keystroke (privacy), and cannot tell
    which app is focused.
  - The `InputCapture` / `RemoteDesktop` portals + libei: designed for input
    *capture/control* sessions (remote desktop, screen sharing), not lightweight
    hotkeys; gohook's comment names these as the true-global-capture path
    ([wayland.go](https://github.com/robotn/gohook/blob/master/wayland.go)).

**Bottom line:** on Wayland a global hotkey is possible **only** through the
GlobalShortcuts portal, and only where the compositor implements it (GNOME 48+,
KDE Plasma 5.27+). It is not a code-path shared with the Windows/X11 hotkey
libraries.

### 4.2 Wayland text injection

See §3.2. In short: `wtype` is wlroots-only (virtual-keyboard protocol);
`ydotool` needs root + `ydotoold`; `robotgo`'s `libei` backend uses the portal and
works on GNOME/KDE with consent.

### 4.3 Wayland clipboard

Native and cgo-free in `golang.design/x/clipboard` via data-control, with the
XWayland fallback and the GNOME < 49 caveat described in §3.1.

---

## 5. Packaging

### 5.1 Single binaries / cross-compilation

- **cgo-free transitively** (build with `CGO_ENABLED=0`, set `GOOS`/`GOARCH`):
  `golang.design/x/hotkey`, `golang.design/x/clipboard`, `ebitengine/oto/v3`
  (playback), and `robotgo` **only** under its experimental pure-Go tags
  (`purego`, `win`, `mac`, `x11`, `wayland`, `libei`)
  ([hotkey README](https://github.com/golang-design/hotkey/blob/main/README.md),
  [clipboard README](https://github.com/golang-design/clipboard/blob/main/README.md),
  [oto README](https://github.com/ebitengine/oto/blob/main/README.md),
  [robotgo README](https://github.com/go-vgo/robotgo/blob/master/README.md)).
- **Requires cgo** and therefore a C toolchain (`CGO_ENABLED=1`):
  `malgo` (no libs on Windows/macOS, `-ldl` on Linux), `portaudio` (also needs
  PortAudio dev headers), and `robotgo` under its default backend
  ([malgo README](https://github.com/gen2brain/malgo/blob/master/README.md),
  [portaudio README](https://github.com/gordonklaus/portaudio/blob/master/README.md)).
- **Cross-OS consequences:** Go disables cgo on cross-compiles by default
  ([oto README](https://github.com/ebitengine/oto/blob/main/README.md)). To build
  a Windows binary from Linux (or vice versa) while using `malgo`, you need a C
  cross-compiler and `CC` for the target (e.g. `mingw-w64` for Windows). Building
  the Linux binary must happen on Linux or with a Linux-targeting
  cross-toolchain (or a container), because of the cgo dependency. This is the
  main reason to prefer cgo-free libraries where possible.
- **Practical shape:** a single Go binary per OS is achievable for the
  hotkey + clipboard + capture stack if audio capture is `malgo` and you build
  each target with its own toolchain (or use `zig cc`/containers for the cgo
  parts). A fully `CGO_ENABLED=0` artifact is only possible if you avoid
  `malgo`/`portaudio` or use a pure-Go capture path (none is mature here).

### 5.2 Running as a background daemon/service

- **`github.com/kardianos/service` (v1.3.0, 2026-07-06)** installs/uninstalls and
  runs a Go program as a service: *"Currently supports Windows XP+, Linux/(systemd
  | Upstart | SysV), and OSX/Launchd."* It also detects whether it was started
  from an interactive terminal or a service manager
  ([README](https://github.com/kardianos/service/blob/master/README.md),
  [proxy.golang.org](https://proxy.golang.org/github.com/kardianos/service/@latest)).
- **Platform environment:** a Linux service must inherit the user session's
  `DISPLAY`/`WAYLAND_DISPLAY`, `XDG_RUNTIME_DIR`, `DBUS_SESSION_BUS_ADDRESS`
  (for portals), and `PULSE_SERVER` as relevant. A system boot service does not
  have these; a **systemd user service** (or equivalent) started with the graphical
  session does. The portal path in particular needs the session bus.
- **Windows interactive-session constraint (critical):** a Windows *service* runs
  in **Session 0**, which has no access to the interactive desktop's clipboard and
  receives no user hotkeys/clipboard events; clipboard `Write` "works" only inside
  Session 0 ([clipboard README](https://github.com/golang-design/clipboard/blob/main/README.md)).
  So a Windows SCM service is the wrong host for hotkey + clipboard + text
  injection. Use instead a **per-user auto-start** mechanism (Startup folder /
  `Run` registry key / Task Scheduler "at logon") running inside the interactive
  session, optionally with a background helper. `kardianos/service` is still
  useful for a headless daemon, just not for the session-bound capabilities.

---

## 6. Recommendation

### Recommended library per capability (Windows + Linux/X11)

| Capability | Recommendation | Why |
|---|---|---|
| **Global hotkey** | **`golang.design/x/hotkey` (v0.6.4)** | Only one of the three designed as an actual *grab* (`RegisterHotKey`/`XGrabKey`) with explicit conflict errors; cgo-free; single API across Windows + X11. Use `gohook` (v0.50.0) instead if you need **true native key-up/hold** and can accept libuiohook's Linux deps and observer (non-exclusive) semantics. |
| **Audio capture** | **`gen2brain/malgo` (v0.11.26)** | Device enumeration, built-in format/sample-rate conversion (0 = native, Linear/Speex), backend coverage on Windows and Linux. Accept cgo + per-target toolchain. |
| **Clipboard** | **`golang.design/x/clipboard` (v0.11.0)** | cgo-free desktop, native X11 and Wayland data-control, text/image, watch, and honest documented failure modes. |
| **Text injection** | **`go-vgo/robotgo` (v1.2.1)** for synthesis (`KeyTap`/`Type`), or platform tools | Cross-platform synthetic `Ctrl+V`; on Wayland use `ydotool` (root) / `wtype` (wlroots) / robotgo `libei` (GNOME/KDE). Prefer clipboard + paste over per-character typing. |
| **Service/daemon** | **`kardianos/service` (v1.3.0)**, with per-user session hosting on Windows | Standard installer/runner for Windows SCM and systemd/Upstart/SysV; but keep hotkey/clipboard/injection in the interactive session. |

### Wayland caveat

There is **no portable global-hotkey library** for Wayland. A global hotkey is
possible only via the **GlobalShortcuts portal** (`xdg-desktop-portal`), supported
by **GNOME 48+** and **KDE Plasma 5.27+**, and you would have to implement the
D-Bus client yourself (e.g. `godbus/dbus/v5`). On wlroots compositors the norm is
compositor-configured shortcuts, not an app API. Text injection on Wayland is
similarly fragmented: `wtype` is wlroots-only, `ydotool` needs root, and
`robotgo`'s `libei` backend works on GNOME/KDE via the portal. Clipboard works
natively and cgo-free via data-control (GNOME 48+; older GNOME falls back through
XWayland). **Plan a degraded Wayland experience**, or drive it through portals
where available.

---

## 7. Open questions

1. **Hold-to-talk on the GlobalShortcuts portal:** the spec defines both
   `Activated` and `Deactivated`, but it is unverified from primary sources
   whether GNOME 48/49 and KDE Plasma reliably emit `Deactivated` for
   push-to-talk, and with what latency. Needs a hands-on test on both desktops.
2. **`golang.design/x/hotkey` on Wayland/XWayland:** does `XGrabKey` via XWayland
   reliably deliver `KeyUp` (the README notes X11 AutoRepeat emits continuous
   KeyUp), and does it conflict with the compositor's own grabs? Untested here.
3. **Cross-compiling `malgo` from Windows to Linux:** confirm the exact
   `CC`/toolchain story (zig cc / mingw vs a Linux container) and whether cgo
   linking of `-ldl` is the only Linux dependency in practice.
4. **Clipboard restore timing on Windows:** since the OS never reports when a
   paste is consumed, is "write → paste → restore" ever safe, or must the tool
   simply leave the transcript on the clipboard? Determine acceptable UX.
5. **Terminal paste detection on Linux:** all terminals do not use `Ctrl+Shift+V`
   (it is a default, not a guarantee). Is a configurable shortcut or an
   app-aware heuristic required?
6. **Portal D-Bus library:** no maintained Go wrapper for GlobalShortcuts was
   found; confirm that `godbus/dbus/v5` is sufficient and identify any existing
   xdg-desktop-portal Go helpers worth reusing.
7. **Packaging a fully cgo-free Windows/Linux pair:** only achievable if capture
   avoids `malgo`/`portaudio`; there is no mature pure-Go Linux capture path.
   Decide whether to ship cgo builds (with per-OS CI) or change the capture
   approach.
8. **`robotgo` pure-Go backends are labelled "experimental"** — ascertain
   stability/code coverage before depending on `libei`/`wayland` tags in
   production.

---

## Appendix: primary sources consulted

- `golang.design/x/hotkey` — [README](https://github.com/golang-design/hotkey/blob/main/README.md),
  [hotkey_windows.go](https://github.com/golang-design/hotkey/blob/main/hotkey_windows.go),
  [X11 API notes](https://github.com/golang-design/hotkey/blob/main/_autodocs/api-reference/hotkey-x11.md),
  [module proxy](https://proxy.golang.org/golang.design/x/hotkey/@latest)
- `robotn/gohook` — [README](https://github.com/robotn/gohook/blob/master/README.md),
  [wayland.go](https://github.com/robotn/gohook/blob/master/wayland.go),
  [module proxy](https://proxy.golang.org/github.com/robotn/gohook/@latest)
- `go-vgo/robotgo` — [README](https://github.com/go-vgo/robotgo/blob/master/README.md),
  [examples](https://github.com/go-vgo/robotgo/blob/master/examples/README.md),
  [module proxy](https://proxy.golang.org/github.com/go-vgo/robotgo/@latest)
- `gen2brain/malgo` — [README](https://github.com/gen2brain/malgo/blob/master/README.md),
  [capture example](https://github.com/gen2brain/malgo/blob/master/_examples/capture/capture.go),
  [config docs](https://github.com/gen2brain/malgo/blob/master/_autodocs/configuration.md),
  [module proxy](https://proxy.golang.org/github.com/gen2brain/malgo/@latest)
- `gordonklaus/portaudio` — [README](https://github.com/gordonklaus/portaudio/blob/master/README.md),
  [portaudio.go](https://github.com/gordonklaus/portaudio/blob/master/portaudio.go),
  [module proxy](https://proxy.golang.org/github.com/gordonklaus/portaudio/@latest)
- `ebitengine/oto` — [README](https://github.com/ebitengine/oto/blob/main/README.md),
  [module proxy](https://proxy.golang.org/github.com/ebitengine/oto/v3/@latest)
- `golang.design/x/clipboard` — [README](https://github.com/golang-design/clipboard/blob/main/README.md),
  [Wayland design](https://github.com/golang-design/clipboard/blob/main/specs/wayland-support.md),
  [module proxy](https://proxy.golang.org/golang.design/x/clipboard/@latest)
- XDG Desktop Portal — [GlobalShortcuts spec](https://flatpak.github.io/xdg-desktop-portal/docs/doc-org.freedesktop.portal.GlobalShortcuts.html)
- GNOME — [48 release notes](https://release.gnome.org/48/developers/),
  [xdg-desktop-portal-gnome NEWS](https://github.com/gnome/xdg-desktop-portal-gnome/blob/main/NEWS)
- KDE — [Plasma 5.27 announcement](https://kde.org/announcements/plasma/5/5.27.0/)
- `wtype` — [README](https://github.com/atx/wtype/blob/master/README.md),
  [main.c](https://github.com/atx/wtype/blob/master/main.c)
- `ydotool` — [README](https://github.com/ReimuNotMoe/ydotool/blob/master/README.md)
- `bendahl/uinput` — [README](https://github.com/bendahl/uinput/blob/master/README.md)
- `kardianos/service` — [README](https://github.com/kardianos/service/blob/master/README.md),
  [module proxy](https://proxy.golang.org/github.com/kardianos/service/@latest)
- GNOME Terminal paste shortcut — [GNOME Help](https://help.gnome.org/users/gnome-terminal/stable/adv-keyboard-shortcuts.html.en)
- Go cgo cross-compilation defaults — [oto README](https://github.com/ebitengine/oto/blob/main/README.md)
