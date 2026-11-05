# Manual verification matrix

This is the release gate from issue #83. Automated checks run in CI and in the
two live harnesses; this page records the checks that need a real desktop, a
real microphone, or a real GPU. Update the **Result** columns when a run is
executed and keep the date and release.

Release under verification: **1.0.0** (`VERSION`).

## Environments

| ID | Environment | Needed for |
| --- | --- | --- |
| A | GNOME on Wayland, Linux x64 | GTK4 Settings/Tray, portal hotkey, user-installed `ydotool` injection, indicator |
| B | Windows 11 x64 | install.ps1, WebView2, Task Scheduler, Credential Manager, hidden Server |
| C | Linux or Windows with a real NVIDIA GPU | CUDA runtime, model load, latency |
| D | Two machines on one LAN | remote pairing and Device revocation (tracked with #84) |

## Automated evidence

Run these before the manual pass:

```sh
# Linux or Windows, with a model cached
scripts/e2e-live.sh --profile local-cpu --speech ./sample.wav --report e2e-cpu.md
scripts/e2e-trust.sh --model small --speech ./sample.wav --report e2e-trust.md

# Real-container parity (opt-in)
bash scripts/docker-smoke.sh --model tiny
```

`e2e_live.py` covers the speech round trip, silence/VAD, undecodable media, the
real Client, an unreachable Server, and latency. `e2e_trust.py` covers local
auto-pairing over HTTPS, a remote Pairing code, Device revocation and re-pairing,
the manual Server switch with no fallback, soft-stop/start, supervised restart,
the persisted soft-stop, and Spool save/retry/delete through the real Client.
`docker_smoke.py` covers the container contract: `docker exec` pairing, the
writable `/config`, `/state` persistence, restart policy, and recreation.

The trust harness drives remote pairing and the profile switch through the HTTP
contract and the Client's `servers`/`active_server` configuration. The Settings
and Tray clicks that exercise the same services are unit-tested and stay in the
manual matrix.

| Check | A (GNOME Wayland) | B (Windows) | C (CUDA host) |
| --- | --- | --- | --- |
| `e2e_live.py` (local CPU) | Pass 2026-11-03 (`tiny`, 8/8) | Pending | — |
| `e2e_live.py` (local GPU) | — | — | Pending |
| `e2e_trust.py` | Pass 2026-11-03 (`tiny`, 20/20) | Pending | Pending |
| `docker_smoke.py` | Pending (opt-in) | Pending (opt-in) | Pending (opt-in) |

## Manual matrix

Each row is a user-visible contract. Record date, release, and the result. A
failure opens a ticket; do not silently mark the row passed.

### Install and startup

| # | Check | A | B | C |
| --- | --- | --- | --- | --- |
| 1 | Installer completes on a clean machine; no admin rights on Windows and no `sudo` on Linux | Pending | Pending | — |
| 2 | Linux install runs no package manager, writes no udev/`modules-load.d` file, and starts/enables no unit; `--dry-run` and `--check` change nothing | Pending | — | — |
| 3 | Settings window opens with GTK4/WebKitGTK 6.0; a host without them falls back to the browser page | Pending | Pending | — |
| 4 | WebView2 is reported as missing with the manual link; the installer never downloads it | — | Pending | — |
| 5 | Tray shows idle, recording, processing, and error; no AppIndicator on GNOME still leaves Settings reachable from the menu/CLI | Pending | Pending | — |
| 6 | Autostart is off after install; turning it on enables the selected mode, turning it off removes the login registration, and the Server task/unit is registered disabled | Pending | Pending | Pending |
| 7 | Port 8765 in use stops the Server with an actionable message; changing `server.port` works | Pending | Pending | Pending |
| 8 | `server.device: auto` uses CUDA on C and logs a CPU fallback without the runtime | — | — | Pending |

### Dictation

| # | Check | A | B | C |
| --- | --- | --- | --- | --- |
| 9 | Hotkey starts and stops the cycle; cancel (`Ctrl+Shift+Q`) discards without injection | Pending | Pending | — |
| 10 | Hotkey conflict is detected and a new combination applies without a restart | Pending | Pending | — |
| 11 | Microphone picker and live level meter select the right input | Pending | Pending | — |
| 12 | Text injects into a GUI editor (`Ctrl+V`) and a terminal (`Ctrl+Shift+V`/`Shift+Insert`) | Pending | Pending | — |
| 13 | Clipboard restoration matches `client.clipboard_restore` | Pending | Pending | — |
| 14 | Wayland injection works through a user-installed `wtype` or `ydotool`; without `/dev/uinput` or the daemon it degrades to clipboard-only with a log line | Pending | — | — |
| 15 | Empty/silent utterance injects nothing and leaves the clipboard untouched | Pending | Pending | — |
| 16 | Earcons and the Recording indicator honour Settings; a pinned unavailable backend logs once and does not stop dictation | Pending | Pending | — |
| 17 | A failed utterance lands in the Spool; Retry, Copy, Insert, and Delete work against the active profile | Pending | Pending | — |

### Trust, routing, and Server management

| # | Check | A | B | C |
| --- | --- | --- | --- | --- |
| 18 | Fresh install has a Local Server profile and dictates without a code | Pending | Pending | — |
| 19 | Local certificate is pinned; deleting the state directory produces "certificate changed, pair again", and **Pair again** recovers | Pending | Pending | — |
| 20 | Remote pairing with a code from Settings pins the fingerprint and persists across both reboots | Pending | Pending | Pending |
| 21 | A revoked Device gets `401` on its next dictation; a new code re-pairs it | Pending | Pending | Pending |
| 22 | The Client's own Device is marked **This Device** and cannot be revoked from the Local Server settings | Pending | Pending | — |
| 23 | An unreachable active profile reports one error and spools; it does not retry another profile | Pending | Pending | — |
| 24 | The Tray/Settings manual switch changes routing; health is advisory and never switches by itself | Pending | Pending | — |
| 25 | Soft-stop returns `503 stopped`, keeps Admin API/logs alive, and Start resumes | Pending | Pending | Pending |
| 26 | Restart is a supervised self-exit; the Device token and a soft-stop flag survive | Pending | Pending | Pending |
| 27 | Local Server Stop/Restart use the hard supervisor path (ADR 0012); a Remote Server uses soft-stop | Pending | Pending | — |
| 28 | LAN exposure needs a confirmation, writes the YAML, and takes effect only after restart; loopback is the default | Pending | Pending | Pending |
| 29 | Model catalog downloads with progress/cancel, activates online, keeps the old model on failure, deletes a non-active model, and idle-unloads after the timeout | Pending | Pending | Pending |
| 30 | Diagnostics copy the versions, redacted config, and log tail; no token or API key appears | Pending | Pending | Pending |

### Updates and uninstall

| # | Check | A | B | C |
| --- | --- | --- | --- | --- |
| 31 | Update check shows the available version; download verifies `checksums.txt.sig` and `checksums.txt` before launching | Pending | Pending | — |
| 32 | Re-running the installer preserves config, models, Devices, and TLS; `kitsune.yaml.bak` is kept; Linux `--check` reports versions and a lower version is refused without `--force` | Pending | Pending | — |
| 33 | Uninstall asks separately about models and Server data; purge removes config, certificates, Devices, models, logs, Spool, and keychain/Credential Manager tokens; neither touches user-installed packages or a user-added udev rule | Pending | Pending | — |
| 34 | A clean uninstall/reinstall starts dictation again | Pending | Pending | — |
| 38 | The first-run wizard downloads the model with progress; the Server starts without a model and downloads nothing on its own | Pending | Pending | Pending |

### Two-machine pairing (#84)

| # | Check | Result |
| --- | --- | --- |
| 35 | `expose on --yes`, restart, and `https://<server>:8765/health` answers from the laptop | Pending |
| 36 | Pair from the laptop; the Server log names the Device; both processes restart and dictation still works | Pending |
| 37 | Revoke the laptop; its next dictation shows "revoked, pair again"; a new code restores it | Pending |

## Executed runs

Fill one row per machine when the release candidate is verified. A row that
says "Pending" has not been executed and must not be treated as a pass.

| Date | Release | Environment | Checks | Result |
| --- | --- | --- | --- | --- |
| 2026-11-03 | 1.0.0 | Fedora 44, GNOME Wayland, CPU | Automated `e2e_live.py` / `e2e_trust.py` | **Pass**: 8/8 and 20/20 |
| | 1.0.0 | Linux GNOME Wayland, CPU | Manual matrix 1–34 | Pending |
| | 1.0.0 | Windows 11 | Automated + manual matrix 1–34 | Pending |
| | 1.0.0 | Real NVIDIA GPU | Automated + manual matrix 8, 25–29 | Pending |
| | 1.0.0 | Two machines on one LAN | Manual matrix 35–37 (#84) | Pending |

## Fixes found by this verification

- **Soft-stop could not restart a model loaded from the Hugging Face cache.**
  `FileModelStorage` only recognised the catalog layout
  (`download_root/<model>/model.bin`). A Server started with `server.model` and
  an empty catalog cache loaded the HF snapshot, then refused `/admin/start`
  after a soft-stop with "not downloaded yet". This also made a supervised
  restart after a soft-stop stay stopped. The storage now recognises
  `models--Systran--faster-whisper-<model>/snapshots/<revision>` as installed,
  loads that snapshot, and deletes it with the repository directory. Regression
  tests are in `server/tests/test_models.py`.

## Parent spec #65 review and drift

The implementation was reviewed against the spec's user stories,
implementation decisions, and out-of-scope list. The following differences are
intentional and are recorded here so a future change does not mistake them for
omissions.

| Spec item | Implementation | Why |
| --- | --- | --- |
| User story 33: toggle **and** push-to-talk | Toggle only; a legacy `trigger: hold` loads as toggle | Push-to-talk was removed from the Client; see the CLI and `kitsune.example.yaml`. |
| User story 73: model progress streamed as events | Polled on the health tick and after each action | The Admin API stays simple; the UI shows progress and cancel. |
| Implementation decisions: HTML/TypeScript UI, Svelte suggested | Server-rendered HTML/JS in `internal/shell/page.go` | ADR 0010 keeps the Wails adapter thin without a Node build in release CI. |
| Soft-stop is the uniform "stop" on native and Docker | Local Server Stop/Restart are **hard** supervisor operations; Remote/Docker use soft-stop | ADR 0012: the Client on the Server host can drive the OS supervisor. |
| User stories 49/50/51/52/53: remote status, settings, models, logs, lifecycle | Implemented through the Admin API; the Server YAML is written only by that Server | Pairing grants a trusted Device the full Admin API (ADR 0008). |
| "No automatic fallback" | Enforced in the Client and covered by `e2e_trust.py` | The active profile is always explicit. |
| Multi-user isolation | Out of scope; the loopback limitation is documented | A local user still needs a Device token; see [security.md](../security.md). |
| Windows code signing | Out of scope; SmartScreen is documented | v1 ships unsigned. |
| macOS | Out of scope; nothing is built or tested | The Wails boundary does not block it. |

No other acceptance criterion from #65 was found to be missing without a
documented decision.
