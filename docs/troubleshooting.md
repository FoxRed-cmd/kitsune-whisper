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

**The installer warns that GTK4, WebKitGTK 6.0, ALSA, or X11 is missing.** The
installer installs no packages; it only checks and warns. Install the runtime
yourself using the command for your distribution in
[`dependencies.md`](dependencies.md#linux-packages), then restart the Client.
Without GTK4/WebKitGTK the Client still runs and Settings open in the browser;
without ALSA it cannot record; without `libX11` hotkeys and X11 injection do not
load.

**The installer warns that the Client needs a newer glibc.** The Client requires
`2.39`; the Server requires `2.35`. On Ubuntu 22.04, Debian 12, or openSUSE Leap
15.6 the Client cannot run, but the Server can: install with `--server-only` or
use another machine for the Client.

**The installer warns that a Wayland injection tool is missing.** Install
`wtype` (wlroots) or `ydotool` (GNOME/KDE, wlroots) and set up `/dev/uinput` as
described in [`dependencies.md`](dependencies.md#granting-uinput-access).
Until then transcription only reaches the clipboard. The installer never
installs the tool or writes the rule.

**The Settings window is blank.** On Linux check that the GTK4 and WebKitGTK 6.0
runtimes are present. On Windows the installer never downloads the WebView2
Evergreen Runtime; when it is missing it warns and links to the manual
download. Install it from that link, then reopen the Client.

**Port 8765 is already in use.** The Server never picks a random port. Stop the
other process or set `server.port` in `kitsune.yaml` and restart the Server. The
Client's Local Server profile must use the same port.

**The CUDA runtime is missing or its install failed.** The Server falls back to
CPU (`server.device: auto`). Neither installer downloads CUDA; install
`nvidia-cublas-cu12` into the bundle's `lib` directory with the command in
[`dependencies.md`](dependencies.md#optional-cuda-runtime), then restart the
Server. A GPU host without `nvidia-smi` is treated as CPU-only.

## Starting and the Tray

**Nothing appears after login.** Autostart is off after installation. Turn it
on in **Settings → Startup** if you want the Client (and optionally the Server)
at login; it starts hidden in the Tray. Before that, launch it from the
application menu or run `kitsune-client settings` (or `kitsune-client gui`). On
GNOME without the AppIndicator extension the Tray may not render; the
application entry and CLI are the supported fallback.

**A second launch exits immediately.** That is the single-instance lock. Use
`kitsune-client settings`, `toggle`, or `cancel` to drive the running Client.

**Nothing starts after reboot.** Autostart is off by default. Turn it on in
**Settings → Startup** and choose Client + Server, Client only, or Server only;
only modes whose Components are installed are offered. On Linux check
`systemctl --user status kitsune-client.service kitsune-server.service`; on
Windows check the per-user tasks in Task Scheduler. Turning autostart off
removes the login registration instead of leaving it disabled. The Server's
supervisor registration may remain so on-demand start still works, but it never
fires at login.

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
running, or cannot open `/dev/uinput`. The installer only warns; install the
tool, grant `/dev/uinput` access, and start `ydotoold` as described in
[dependencies.md](dependencies.md). The Client logs the reason and degrades to
clipboard-only; it never fails the dictation cycle for this.

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
cannot load. Install the optional CUDA runtime with the command in
[`dependencies.md`](dependencies.md#optional-cuda-runtime), confirm `nvidia-smi`
works for the user running the Server, and restart the Server. `GET /status`
reports the resolved `device` and `gpu` block.

## Updates

**The update check says a newer version exists but nothing installs.** Updates
are check-and-confirm by design. Press **Download and install**; the Client
fetches the release installer and verifies its SHA-256 against
`checksums.txt`; the installer then verifies `checksums.txt.sig` before
installing anything.

**The checksum does not match.** The download is corrupted or the release assets
are inconsistent. Delete the partial download and retry. Do not bypass the
check; report the release instead.

**The release signature failed to verify.** The installer aborts before
extracting anything: `checksums.txt.sig` does not match the release public key
pinned in the script, or the signature is missing. This is what a compromised
download host or a corrupted asset looks like. Fetch the installer and the
release from the official repository and retry; do not use `--insecure` /
`-Insecure` to get past it unless you are testing locally signed artifacts you
built yourself. On Linux, `openssl` must be installed for the check.

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
keeps configuration, models, certificates, pairing state, logs, Spool, and
wizard progress. Re-run with `--remove-models` / `--remove-server-data` (Linux)
or `-RemoveModels` / `-RemoveServerData` (Windows), or use `--purge` /
`-Purge` for a clean slate. A purge removes the keyring or Credential Manager
tokens and every directory we created. It does **not** remove a package you
installed, and on Linux it does not remove a udev rule you added yourself — see
[`dependencies.md`](dependencies.md#removing-a-rule-you-added). Neither
uninstall nor purge touches packages. See
[install.md](install.md#uninstall-and-purge).

## Reporting a problem

Include the release (`kitsune-client --version`, `/health`), the platform and
session type, the active profile, the exact command or click, and the copied
diagnostics. For an installation problem, say which installer you used (the
Linux one-liner or the Windows `install.ps1`) and whether it was a clean machine.
