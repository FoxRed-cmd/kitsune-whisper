# Troubleshooting

Start with the evidence the app already records. **Settings → Diagnostics**
shows versions, the active Server profile, the redacted effective configuration,
and the tail of both logs; **Copy diagnostics** produces a block suitable for an
issue. The Client also has one-shot checks:

```sh
kitsune-client --version
kitsune-client --check-config   # effective client: section, exits 0/1
kitsune-server --version
kitsune-server --check-config   # effective server: section, exits 0/1
kitsune-server status           # bundle, supervisor, and pinned config path
```

| What | Linux | Windows |
| --- | --- | --- |
| Client log | `~/.cache/kitsune-whisper/client.log` | `%LOCALAPPDATA%\kitsune-whisper\client.log` |
| Server log | `~/.local/state/kitsune-whisper/server.log` | `%LOCALAPPDATA%\kitsune-whisper\server.log` |
| Config | `~/.config/kitsune-whisper/kitsune.yaml` | `%APPDATA%\kitsune-whisper\kitsune.yaml` |
| Spool | `~/.cache/kitsune-whisper/spool` | `%LOCALAPPDATA%\kitsune-whisper\spool` |
| Models | `~/.cache/kitsune-whisper/models` | `server.download_root` |

`--verbose` (or `client.log_level: debug` / `server.log_level: debug`) raises
the level for one run. Do not paste a Device token or a `processing.api_key`
into an issue; diagnostics already redact them.

## Installation

**The installer says a GTK/WebKitGTK package is missing.** The default Linux
build needs GTK4 and WebKitGTK 6.0. The installer selects the legacy GTK3 +
WebKit2GTK 4.1 build automatically on older LTS hosts. If you declined
`sudo --no-elevate`, install the runtime packages yourself or re-run the
installer with elevation. Without a desktop toolkit the Client still works and
Settings open in the browser.

**The Settings window is blank.** On Linux check that the WebKitGTK runtime
matches the build (`--gui gtk3` for old hosts). On Windows the installer never
downloads the WebView2 Evergreen Runtime; when it is missing it warns and links
to the manual download. Install it from that link, then reopen the Client.

**Port 8765 is already in use.** The Server never picks a random port. Stop the
other process or set `server.port` in `kitsune.yaml` and restart the Server. The
Client's Local Server profile must use the same port.

**The CUDA download failed or was declined.** The Server falls back to CPU
(`server.device: auto`). Re-run the installer with the CUDA task, or run
`install.sh --cuda` on Linux. A GPU host without `nvidia-smi` is treated as
CPU-only.

## Starting and the Tray

**Nothing appears after login.** The Client starts hidden in the Tray; open
**Settings** from the application menu or run `kitsune-client settings` (or
`kitsune-client gui`). On GNOME without the AppIndicator extension the Tray may
not render; the application entry and CLI are the supported fallback.

**A second launch exits immediately.** That is the single-instance lock. Use
`kitsune-client settings`, `toggle`, or `cancel` to drive the running Client.

**Autostart does not come back after reboot.** Toggle it in **Settings →
General → Startup** or run `kitsune-client install-autostart` /
`kitsune-server install`. On Linux check `systemctl --user status
kitsune-client.service kitsune-server.service`; on Windows check the two
per-user tasks in Task Scheduler. Toggling autostart off keeps the registration
but stops it from launching.

**Tray Quit closed dictation for another machine.** Quit stops the Client only.
A Remote Server keeps serving other Devices; use **Stop Server** to soft-stop
it.

## Dictation

**The hotkey does nothing.** Another application may own the combination. Change
it in **Settings → Dictation**; conflicts are reported. On Wayland, the
compositor may not grant a portal shortcut: switch `client.hotkey_backend` to
`external` and bind `kitsune-client toggle` in the desktop's keyboard settings.
See [wayland.md](wayland.md).

**The microphone is silent or the wrong device is used.** Pick the input in
**Settings → Dictation → Microphone** and watch the level meter. `--device`
overrides the config for one run. Check the OS input volume and mute switch.

**Wayland leaves the text on the clipboard.** `wtype`/`ydotool` is missing, not
running, or cannot open `/dev/uinput`. The Client logs the reason and degrades to
clipboard-only. Re-run the installer with elevation to add the `uaccess` rule and
the `ydotoold` user unit, or install a helper manually as described in
[install.md](install.md).

**Nothing is injected, but the log shows a transcription.** Injection uses the
clipboard plus a synthetic paste; some terminals need `Ctrl+Shift+V` or
`Shift+Insert`. `client.paste: false` deliberately leaves the text on the
clipboard. A locked screen or an application that rejects synthetic input also
blocks injection.

**Transcription is empty.** A silent or too-short utterance is discarded by
design (the Server's VAD gate and `client.min_recording_seconds`). Speak after
the start cue. An empty result injects nothing and leaves the clipboard alone.

**A failed utterance disappeared.** It should be in the Spool
(**Settings → Spool**) because the Server was unreachable, timed out, or
returned an error. Use **Retry** against the active profile, then **Copy** or
**Insert**. A `4xx` is not retried automatically. From a terminal (for example
on a headless machine):

```sh
kitsune-client spool list                 # id, duration, capture time
kitsune-client spool retry <id>           # print the text
kitsune-client spool delete <id>
```

`kitsune-client transcribe-file --spool-on-error clip.wav` saves the file when
that one-shot transcription fails, which is handy for a scripted import.

**A spooled utterance will not retry.** The Spool keeps the audio but the
active profile must be able to transcribe it. Pair the Server again, start a
soft-stopped Server, or switch to a healthy profile and retry. A failed retry
leaves the entry in place.

## Server and trust

**“Server unreachable”.** Confirm the Server is running
(`kitsune-server status`), the profile URL matches `server.port`, and the
certificate has not changed. Check the network path and firewall; then test
`https://<server>:8765/health`, which needs no token.

**“401 unauthorized” or “revoked, pair again”.** The Device token is missing,
unknown, or revoked. Check `kitsune-server devices list` on the Server host.
Generate a new Pairing code and pair the profile again. Removing the profile on
the Client does not revoke the token on the Server.

**“Certificate changed, pair again”.** The Server's TLS identity was
regenerated (deleted state directory, recreated Docker volume, or a
man-in-the-middle). If you did not expect it, stop and investigate before
pairing. If you did, use **Pair again**: the Local Server re-pairs in one click,
a Remote Server asks for a fresh code.

**“503 stopped”.** The Server is soft-stopped: the process and Admin API are
alive, the model is unloaded. Press **Start** on the Server profile or run
`POST /admin/start` with a Device token.

**“Client and Server versions differ”.** A minor-version mismatch is refused to
avoid debugging an incompatible API as an auth failure. Upgrade both to the same
release; patch differences are allowed.

**The Server is not visible from the laptop.** LAN exposure is off by default.
Run `kitsune-server expose on --yes` on the Server host and restart it; then
check the host firewall. See [security.md](security.md).

**The model is not loaded or fails to load.** Open **Settings → Models** on the
profile and check the state (`cold`, `loading`, `failed`). Download the model,
activate a different one, or point `server.model` at a local CTranslate2
directory. An activation failure keeps the previous model active. See
[models.md](models.md).

**CUDA is not used.** `server.device: auto` resolves to CPU when cuBLAS/cuDNN
cannot load. Install the CUDA runtime (the installer offers it; `install.sh
--cuda`), confirm `nvidia-smi` works for the user running the Server, and restart
the Server. `GET /status` reports the resolved `device` and `gpu` block.

## Updates

**The update check says a newer version exists but nothing installs.** Updates
are check-and-confirm by design. Press **Download and install**; the Client
fetches the release installer, verifies its SHA-256 against `checksums.txt`, and
only then launches it.

**The checksum does not match.** The download is corrupted or the release assets
are inconsistent. Delete the partial download and retry. Do not bypass the
check; report the release instead.

**An upgrade lost settings.** Re-running the installer preserves
`kitsune.yaml`, models, Devices, and TLS. The installer keeps the previous config
as `kitsune.yaml.bak` and runs `migrate-config`; inspect both files if a value
looks wrong.

## Docker

**`docker exec ... kitsune-server pair` cannot find the config.** The image sets
`KITSUNE_CONFIG=/config/kitsune.yaml`; mount a writable `/config` directory and
keep it across recreations. The pairing command prints the file it read.

**A recreated container made every Client re-pair.** The state volume changed.
Mount `kitsune-state` at `/state`; it holds TLS, Devices, pairing codes, and the
soft-stop flag. Models live on a separate `kitsune-models` volume.

**Stop from the Client did not stop the container.** For a Remote Server **Stop**
is a soft-stop, because Docker owns the process. Use `docker compose stop` on
the Docker host for a hard stop. See [docker-smoke.md](verification/docker-smoke.md).

## Uninstall and purge

**Models or credentials remain after uninstalling.** The default uninstaller
keeps user data. Re-run with `--remove-models` / `--remove-server-data` (Linux)
or use `--purge` for a clean slate. A purge also removes the udev rule on Linux
and the keychain tokens. Windows uninstall currently keeps configuration,
models and pairing state; removing those data classes is a later installer
revision. See [install.md](install.md#uninstall).

## Reporting a problem

Include the release (`kitsune-client --version`, `/health`), the platform and
session type, the active profile, the exact command or click, and the copied
diagnostics. For an installation problem, say which installer you used (the
Linux one-liner or the Windows `install.ps1`) and whether it was a clean machine.
