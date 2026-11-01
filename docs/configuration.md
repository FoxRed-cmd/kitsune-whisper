# Configuration reference (`kitsune.yaml`)

Both processes read a single `kitsune.yaml` with two top-level sections: `server:`
and `client:`. Each process reads **only its own section** and ignores the other,
so one file configures a whole install. A fully commented template lives in
[`kitsune.example.yaml`](../kitsune.example.yaml).

## File discovery

The first match wins:

1. `--config PATH` (must exist).
2. `$KITSUNE_CONFIG` (must exist).
3. `./kitsune.yaml` in the current working directory.
4. The OS user-config directory:
   - Linux: `$XDG_CONFIG_HOME/kitsune-whisper/kitsune.yaml` (default
     `~/.config/kitsune-whisper/kitsune.yaml`).
   - Windows: `%APPDATA%\kitsune-whisper\kitsune.yaml`.

If no file is found, built-in defaults apply: the Local Server profile points at
`https://localhost:8765`, which is also the Server's default port (TLS is on by
default). An existing `client.server_url` is migrated into the active Server
profile on load, so an older configuration keeps working without edits; a
plain-HTTP URL keeps working only when the Server runs with `tls.enabled: false`.

## Precedence

Highest wins:

```
CLI flags  >  environment  >  file  >  built-in defaults
```

- **CLI flags** are a curated subset: `--config`, `--device`, `--verbose`,
  `--check-config` (both binaries).
- **Environment** variables are `KITSUNE_<SECTION>_<KEY>`, with nesting via a
  double underscore:
  - `KITSUNE_CLIENT_SERVER_URL` (deprecated; migrates to a profile)
  - `KITSUNE_CLIENT_ACTIVE_SERVER`
  - `KITSUNE_CLIENT_AUDIO__DEVICE`
  - `KITSUNE_SERVER_MODEL`
  - `KITSUNE_SERVER_DECODE__BEAM_SIZE`
  - `KITSUNE_SERVER_PROCESSING__BASE_URL`
  - `KITSUNE_SERVER_PROCESSING__API_KEY`
- `--device` overrides the audio input device on the Client and the compute
  device on the Server; `--verbose` is shorthand for `log_level: debug`.

## Validation

Keys are `snake_case`; units are suffixes (`_seconds`, `_mb`). Unknown keys in
the binary's own section are a **startup error** (typo safety) — the other
section is ignored entirely. Validation is fail-fast and names the offending
field path. The configuration is read once at startup. The configuration stores
(the Server's `kitsune_server.store` and the Client's settings store) classify
each change as hot-applied or restart-required, and write their section
atomically while preserving the rest of the file. The Server's Admin API now
applies a narrow hot subset (log level, upload and audio limits, decode language
and initial prompt) and reports the rest as restart-required; other hot
classification remains advisory until the runtime reloads those values.

Check a config without starting either process:

```sh
kitsune-server --check-config   # prints the effective server: section, exits 0/1
kitsune-client --check-config   # prints the effective client: section, exits 0/1
```

## `server:` section

| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `expose_to_network` | bool | `false` | LAN exposure switch. On = the Server binds `0.0.0.0`; off = it binds `host` (loopback unless that is explicit). Needs a restart. |
| `host` | string | `127.0.0.1` | Bind address when `expose_to_network` is off. An explicit value is used as given, including the legacy `host: 0.0.0.0`; prefer `expose_to_network: true`. A non-loopback `host` without that switch still works and logs a migration warning. |
| `port` | int | `8765` | Bind port (1–65535). The default moved from `8000` to `8765` for the desktop install; set `port: 8000` to keep an existing setup. |
| `model` | string | `small` | Active model: a faster-whisper size name (`tiny`, `base`, `small`, `medium`, `large-v3`, `turbo`, `distil-large-v3`) **or** a path to a local CTranslate2 model directory. |
| `device` | `auto`\|`cpu`\|`cuda` | `auto` | `auto` uses CTranslate2's CUDA detection and falls back to CPU. |
| `compute_type` | enum | `auto` | `auto` → `float16` on CUDA, `int8` on CPU. Other values: `int8`, `int8_float16`, `int8_float32`, `int16`, `float16`, `bfloat16`, `float32`. |
| `workers` | int | `0` | One knob for both HTTP concurrency and engine threads. `0` = auto (1 on GPU, 2 on CPU). A request beyond the cap gets an immediate `503` + `Retry-After`; there is no wait backlog. |
| `download_root` | string | `~/.cache/kitsune-whisper/models` | Hugging Face cache for models. In Docker this is overridden to `/models` (a named volume). |
| `catalog_file` | string | `""` | Model catalog metadata file; `""` uses the catalog bundled with the release. |
| `offline` | bool | `false` | Sets `HF_HUB_OFFLINE`/`local_files_only`; no network, and the model must already be cached. |
| `devices_file` | string | `""` | Paired Device store; `""` uses `devices.json` in the OS state directory. The file stores names, timestamps, and token **hashes**, never plaintext tokens. |
| `max_audio_seconds` | int | `300` | Requests longer than this return `413`. |
| `max_upload_mb` | float | `30` | Requests larger than this return `413`. |
| `idle_unload_seconds` | int | `600` | Unload the model after this many idle seconds; `0` keeps it resident. |
| `tls.enabled` | bool | `true` | TLS is on by default. On first start the Server generates a self-signed certificate in the state directory (`tls/server.crt` and `tls/server.key`), reuses it on later starts, and logs its SHA-256 fingerprint. `false` is the explicit insecure-HTTP opt-out. |
| `tls.cert_file` | string | unset | Certificate override; must be set together with `tls.key_file`. |
| `tls.key_file` | string | unset | Private-key override; must be set together with `tls.cert_file`. |
| `allow_anonymous` | bool | `false` | `POST /transcribe` and the authenticated status endpoint require a Device token by default; set `true` only to allow anonymous access. |
| `log_level` | `debug`\|`info`\|`warning`\|`error` | `info` | Logs to stderr and appends to `server.log` in the user state directory (`~/.local/state/kitsune-whisper/`, `%LOCALAPPDATA%\kitsune-whisper\` on Windows). |
| `decode.task` | `transcribe`\|`translate` | `transcribe` | Whisper task. |
| `decode.language` | string | `auto` | Fallback language when a request omits one; `auto` detects. |
| `decode.beam_size` | int | `5` | Beam search width (≥1). |
| `decode.temperature` | float | `0.0` | Sampling temperature (≥0). |
| `decode.vad_filter` | bool | `true` | Server-side VAD; the authoritative silence gate. Keep on so silent utterances return empty text rather than hallucinated words. |
| `decode.initial_prompt` | string | `""` | Fallback custom vocabulary when a request omits one. |
| `processing.enabled` | bool | `false` | Master switch for Refine/Summarize post-processing over an OpenAI-compatible endpoint. Off = `/transcribe` output is unchanged. |
| `processing.base_url` | string | `http://127.0.0.1:8080` | Root URL of the OpenAI-compatible endpoint; the Server POSTs to `{base_url}/v1/chat/completions`. Loopback by default, so processing stays local unless you opt into an external provider. |
| `processing.api_key` | string | unset | Sent as `Authorization: Bearer` when set; leave unset for the bundled local sidecar. |
| `processing.model` | string | `Qwen3-0.6B-Q8_0.gguf` | Model name sent to the endpoint. The sidecar serves its loaded GGUF and ignores it; set it for Ollama or a cloud API. |
| `processing.extra_body` | map | `{}` | Keys merged into each request body, e.g. `{chat_template_kwargs: {enable_thinking: false}}`. |
| `processing.max_output_tokens` | int | `1024` | Per-utterance cap on tokens the model may generate (`max_tokens`). |
| `processing.stage_timeout_seconds` | float | `30` | Per-stage timeout; a stage that exceeds it degrades to the previous text. |

`/transcribe` accepts per-request `language`, `initial_prompt`, `refine`, and
`summarize` form fields; when omitted, the `decode:` values above and processing
off are used. All other decode parameters are server-owned. With processing on
and `refine=true` and/or `summarize=true`, the Server sends each stage to the
configured endpoint (greedy, capped at `max_output_tokens`, bounded by
`stage_timeout_seconds`) and returns the **Delivered text** in `text`, the
untouched **Transcription** in `raw_text`, plus `applied: {refine, summarize}`
and `warnings: []`. When both are requested, **Refine runs first, then
Summarize**, so the summary is built from the refined text. Refine and Summarize
share the same determinism, language, timeout, and token-cap rules.

Processing never breaks transcription: a step that is disabled, has no reachable
endpoint, raises, exceeds `stage_timeout_seconds`, or produces empty text is
skipped, and the Server returns the previous stage's text with the reason in
`warnings` — transcription itself never becomes a `5xx` because of processing.
The Client logs those warnings.

The authenticated `GET /status` advertises it as a
`processing: {enabled, refine, summarize}` block; the unauthenticated
`GET /health` reports liveness and version only. The endpoint is any
OpenAI-compatible service; see
[Optional post-processing](install.md#optional-post-processing) for the bundled
local sidecar and for pointing at an external provider.

The removed in-process keys `model_repo`, `model_file`, and `gpu_layers` are now a
**startup error**: they moved to the bundled sidecar's `.env` / compose flags. An
operator migrating from the old in-process model must delete them and set
`base_url` (plus `api_key` / `model` for an external provider), or start the
sidecar.

> **Privacy:** the default `base_url` is loopback, so processing stays local.
> Pointing it at any other host sends the transcribed **text off the host**
> (audio still stays on the LAN). The bundled sidecar is reached over the compose
> network, so its appearance of "local" depends on where that network runs —
> the privacy call is the operator's, made through `base_url`.

## Trust, TLS, and Devices

The Server identifies itself with a self-signed certificate generated on first
start. The certificate stays in `tls/` under the Server's state directory (or
`tls.cert_file`/`tls.key_file` when set), so a restart keeps the same identity.
Its SHA-256 fingerprint is logged at startup and returned by pairing; it is not
a secret. Regenerate it only by deleting the pair, which forces every Device to
pair again.

Every `/transcribe` and `/status` request needs `Authorization: Bearer
<Device token>`. Missing, malformed, unknown, and revoked tokens all return
`401` with the `{error:{code:"unauthorized",message:...}}` envelope and a
`WWW-Authenticate: Bearer` header. `/health` stays unauthenticated and reports
only liveness and the version.

`POST /pair/local` issues a Device token without a code, but only to a genuine
loopback peer. A Docker port forward makes the peer the bridge gateway, so a
Docker Server cannot use local auto-pairing. The endpoint returns the token once;
the Server stores only its hash.

Revoke a Device from the Server host without an Admin API client:

```sh
kitsune-server devices list
kitsune-server devices revoke <ID_OR_NAME>
```

The next request from a revoked Device is rejected immediately, including in a
running Server: the store notices the file changed and reloads it. On a Docker
Server the store lives on the `kitsune-state` volume; run the same command
inside the container (`docker exec <container> kitsune-server devices list`).

Remote pairing uses a one-time **Pairing code**. Generate one on the Server
host, or inside a Docker container:

```sh
kitsune-server pair
```

The command prints an 8-character code, its expiry, and the certificate
fingerprint to check. The code is single use, valid for 10 minutes, and failed
attempts are rate-limited. On the Client, open **Settings → Servers and Devices**,
enter the Server address and the code, and press **Pair**. The Client pins the
certificate, stores the Device token in the keychain, and makes the new profile
active.

The same screen lists trusted Devices with their last-seen time and revokes
one. The Admin API endpoints behind it are
`POST /admin/pairing-code`, `GET /admin/devices`, and
`DELETE /admin/devices/{id}`; they require the same Device token as
`/status`. Pairing itself is `POST /pair` with `{"code": "...", "name": "..."}`
and needs no token. Wrong, expired, reused, and rate-limited codes return
distinct `4xx` codes (`pairing_code_wrong`, `pairing_code_expired`,
`pairing_code_reused`, `pairing_rate_limited`).

LAN exposure is off by default. Turn it on deliberately; the change is written
to the Server YAML and takes effect only after a restart:

```sh
kitsune-server expose on --yes
kitsune-server expose off
```

`expose on` without `--yes` refuses and explains the change. The Settings
window's **Servers and Devices** screen offers the same on/off buttons with a
confirmation. The Admin API's
`POST /admin/exposure` with `{"expose": true, "confirm": true}` writes the same
setting and reports `restart_required`; it never rebinds a running listener.

## Server management and diagnostics

The same Device token protects the Server-management endpoints. The Settings
window and the Tray call them for the active Server profile, including a Remote
Server:

| Endpoint | Purpose |
| --- | --- |
| `GET /admin/config` | Effective configuration, per-field source and hot/restart classification, and the list of redacted paths. |
| `PATCH /admin/config` | Validate and atomically write `{"patch": {...}}` with the same rules as startup. An invalid patch returns `400` and leaves the file byte-for-byte unchanged. |
| `GET /admin/logs?tail=N` | The last `N` lines (1–1000, default 200) of the Server log and whether older lines were cut. |
| `POST /admin/restart` | Answer `202`, then exit with code `75`; systemd `Restart=on-failure`, Task Scheduler `RestartOnFailure`, and Docker bring the Server back. The Client polls health until it is ready. |
| `POST /admin/stop` | Soft-stop: unload the model, persist `stopped`, keep the Admin API alive. `/transcribe` returns `503` with code `stopped`. |
| `POST /admin/start` | Clear the soft-stop, load the model again, and accept transcription. |

A soft-stop survives a process restart, so a supervised restart does not
silently reload a model the user deliberately unloaded. A hard process stop is
still available only on the Server host (`kitsune-server stop`).

`GET /admin/config` masks every secret before sending it. Today that is
`server.processing.api_key`, shown as `***` and listed under `redacted`; Device
token hashes never leave the Device store and are not part of the
configuration. A patch that echoes the `***` placeholder back is dropped rather
than writing the marker over the real key. Changes the running Server can apply
immediately (log level, upload and audio limits, decode language and initial
prompt) are reported under `hot_applied`; everything else is reported under
`restart_required`.

The Client keeps each token in the OS keychain (Windows Credential Manager,
Linux Secret Service) and falls back to a file with mode `0600` under
`$XDG_CONFIG_HOME/kitsune-whisper/tokens` (`%APPDATA%` on Windows) when no
keychain is available. Tokens are never written to `kitsune.yaml` and never
logged. The certificate fingerprint **is** written to the Server profile; an
HTTPS profile without one is refused before any token is sent, and a changed
fingerprint produces a distinct "certificate changed, pair again" error.

## `client:` section

| Key | Type | Default | Notes |
| --- | --- | --- | --- |
| `servers` | list | Local Server profile | Ordered **Server profiles**. Each entry has `id` (required, unique), `name`, `url` (required, absolute), `fingerprint` (pinned Server certificate SHA-256; `""` until pairing), and `auto_pair` (`true` only for the Local Server). Unknown profile keys and duplicate ids are rejected. |
| `active_server` | string | first profile | The `id` of the profile that receives every Utterance. Exactly one profile is active; there is no automatic fallback. Defaults to the first profile when omitted. |
| `server_url` | URL | `https://localhost:8765` | **Deprecated.** Full URL of a single Server. When no `servers` list exists it migrates into one active profile (`Default Server`); once `servers` exists it no longer decides routing, but it must still be a valid URL when present. |
| `hotkey` | string | `Ctrl+Shift+Space` | Global toggle/hold key. |
| `hotkey_backend` | `auto`\|`portal`\|`x11` | `auto` | Linux session backend. See [Wayland](wayland.md). |
| `cancel_hotkey` | string | `Esc` | Aborts the current utterance without injecting. |
| `trigger` | `toggle`\|`hold` | `toggle` | `toggle` = press to start, press again to stop; `hold` = push-to-talk. |
| `max_recording_seconds` | number | `300` | Hard stop for an utterance; keep in step with `server.max_audio_seconds`. |
| `min_recording_seconds` | number | `0.3` | Utterances shorter than this are discarded as accidental taps. |
| `timeout_seconds` | number | `0` | Request timeout. `0` = `max(30 s, 2 × audio length)`. |
| `language` | string | `auto` | Sent per request only when not `auto`. |
| `ui_language` | `auto`\|`en`\|`ru` | `auto` | Language of the Settings window and Tray. `auto` follows the OS locale (`LC_ALL`/`LANGUAGE`/`LANG` on Linux, the user locale on Windows) and falls back to English. |
| `initial_prompt` | string | `""` | Sent per request only when non-empty. |
| `refine` | bool | `false` | **Deprecated.** Accepted for one release and ignored with a warning. The Client never sends Refine per request; use the Server API directly if needed. |
| `summarize` | bool | `false` | **Deprecated.** Accepted for one release and ignored with a warning. The Client never sends Summarize per request. |
| `audio.device` | string | `""` | Input device name or index; `""` = system default. `--device` overrides. |
| `paste` | bool | `true` | Synthesize a paste after writing the clipboard; `false` is clipboard-only. |
| `paste_shortcut` | `auto`\|`ctrl_v`\|`ctrl_shift_v`\|`shift_insert` | `auto` | `auto` picks `ctrl_shift_v` in terminals and `ctrl_v` elsewhere; `shift_insert` is the classic console paste chord, so on X11/Wayland the transcription is published to the primary selection (the buffer a terminal pastes) as well as the clipboard. |
| `clipboard_restore` | `auto`\|`always`\|`never` | `auto` | `auto` restores the previous clipboard where the OS reports when a synthetic paste is consumed (X11 and Wayland); Windows keeps the transcription. |
| `wayland_tool` | `auto`\|`wtype`\|`ydotool`\|`none` | `auto` | Wayland injection chain. Ignored on X11/Windows. See [Wayland](wayland.md). |
| `feedback.earcons` | bool | `false` | Optional start/stop/error tones. Progress cues stay silent. |
| `feedback.indicator` | `auto`\|`layer_shell`\|`x11`\|`off` | `auto` | On-screen Recording indicator and Input level meter. `auto` tries wlr-layer-shell on Wayland, then the X11 window when `DISPLAY` is set, then disables it. A pinned backend never falls back; an unavailable one logs one warning and dictation continues. See [Wayland](wayland.md). |
| `spool_dir` | string | `""` | Where failed utterances are saved; `""` = `~/.cache/kitsune-whisper/spool` (`%LOCALAPPDATA%` on Windows). |
| `log_level` | enum | `info` | Same levels as the Server. |
| `log_file` | string | `""` | `""` = `~/.cache/kitsune-whisper/client.log` (`%LOCALAPPDATA%` on Windows). |

A failed Dictation cycle (Server unreachable, timeout, decode error) writes the
utterance to `spool_dir` so speech is never lost; a successful-but-empty
transcription injects nothing and leaves the clipboard untouched.

## See also

- [Install, upgrade, and uninstall](install.md)
- [Recommended hardware](install.md#recommended-hardware)
- [Wayland support and known limitations](wayland.md)
