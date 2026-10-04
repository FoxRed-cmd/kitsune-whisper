# Client autostart runs in the user session, never as a system service

**Status:** accepted

The client needs global hotkeys and the interactive clipboard, so it must run inside the logged-in user's desktop session. On Linux it is a systemd **user** unit bound to `graphical-session.target`; on Windows it is a per-user **Task Scheduler** task at logon (falling back to the `HKCU\...\Run` key). A system/SCM service is never used, because a Windows service runs in Session 0 with its own window station — it cannot see the user's hotkeys or clipboard — and a Linux system service lacks `DISPLAY`/`WAYLAND_DISPLAY`, `XDG_RUNTIME_DIR`, the session bus, and the audio socket. The server may run as a service; the client may not.

## Considered options

- **System/SCM service** (`kardianos/service`) — rejected: Session 0 isolation on Windows breaks hotkeys and clipboard; system scope on Linux breaks the graphical-session environment.
- **XDG autostart `.desktop`** — viable fallback, but no restart-on-failure supervision.
- **Windows `HKCU\...\Run`** — used only as a fallback when Task Scheduler registration is denied.

## Consequences

- The installer registers autostart in the user session, not system-wide.
- The client waits/retries the hotkey grab at startup, since it may launch before the display is ready.
- macOS (next effort) will need a per-user launchd `LaunchAgent`, not a daemon.
