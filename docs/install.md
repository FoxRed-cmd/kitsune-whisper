# Install, upgrade, and uninstall

The Server runs under Docker; the Client is a single binary installed per user.
Both read the same `kitsune.yaml` (each process reads only its own section).

## Client

### Requirements

- **Windows 10/11 (x64)** or **Linux (x64)** with a working microphone.
- A reachable Server (`client.server_url`).
- **Linux**: a systemd user session for autostart. X11 is first-class; Wayland is
  best-effort (see `docs/adr/0002-wayland-v1-stance.md`).

### Install

Linux:

```sh
curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash
```

Windows (PowerShell 7+):

```powershell
irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1 | iex
```

Both scripts download the matching GitHub Release archive, verify its SHA-256
against `checksums.txt`, install the binary, drop a config template **only if
`kitsune.yaml` does not already exist**, and register autostart. Pin a release
with `KITSUNE_VERSION=vX.Y.Z`, or pass `-Version vX.Y.Z` on Windows.

From source (requires a C toolchain — `malgo` uses cgo):

```sh
cd client && go build -o kitsune-client ./cmd/kitsune-client
./kitsune-client install-autostart   # optional
```

### Wayland: synthetic paste needs a helper

On **Windows and X11** the Client synthesizes the paste itself — you need
nothing extra. On **Wayland** it cannot: the compositor gives no unprivileged
way to inject a keystroke, so the Client shells out to a helper. With
`client.wayland_tool: auto` it tries **`wtype`** first (simple, but wlroots-only —
it does not work on GNOME/Mutter) and then **`ydotool`** (works everywhere, but
needs setup). If neither is usable the transcription is left on the clipboard
and the Client logs a "press Ctrl+V" hint instead of pasting.

`ydotool` is not bundled, and it only works while its background daemon
**`ydotoold`** is running with access to `/dev/uinput`. Install and start both:

1. **Install `ydotool`.** Check your distro first — many ship it as the package
   `ydotool` (e.g. `sudo pacman -S ydotool`, `sudo dnf install ydotool`,
   `sudo apt install ydotool`). If it is not packaged, build it from source:

   ```sh
   git clone https://github.com/ReimuNotMoe/ydotool.git
   cd ydotool
   mkdir build && cd build
   cmake ..
   make -j"$(nproc)"
   sudo make install
   ```

   See the upstream repository for details and the latest instructions:
   <https://github.com/ReimuNotMoe/ydotool>.

2. **Grant `/dev/uinput` access.** `ydotoold` needs to write to `/dev/uinput`.
   The usual approach is a udev rule plus membership in the `input` group:

   ```sh
   sudo modprobe uinput
   echo 'KERNEL=="uinput", GROUP="input", MODE="0660", OPTIONS+="static_node=uinput"' \
     | sudo tee /etc/udev/rules.d/80-uinput.rules
   sudo udevadm control --reload-rules && sudo udevadm trigger
   sudo usermod -aG input "$USER"   # log out and back in for the group to apply
   ```

3. **Run `ydotoold`.** Start the daemon in your user session so its socket lands
   in your `$XDG_RUNTIME_DIR` (the path `ydotool` looks in by default):

   ```sh
   ydotoold &          # or wire it into a systemd --user unit
   ```

   If you run the daemon elsewhere, point the Client's environment at the socket
   with `YDOTOOL_SOCKET=/path/to/.ydotool_socket`.

4. **Verify.** With a text field focused:

   ```sh
   ydotool type "hello"            # types text
   ydotool key 29:1 47:1 47:0 29:0 # Ctrl+V
   ```

If `ydotoold` is not running, `ydotool` fails instantly; the Client detects this
and degrades to clipboard-only (the error is in
`~/.cache/kitsune-whisper/client.log`). Force a specific helper with
`client.wayland_tool` (`wtype`|`ydotool`|`none`). See
[Wayland support and known limitations](wayland.md) for the full session matrix.

### Where things go

| | Linux | Windows |
| --- | --- | --- |
| Binary | `~/.local/bin/kitsune-client` | `%LOCALAPPDATA%\kitsune-whisper\bin\kitsune-client.exe` |
| Config | `~/.config/kitsune-whisper/kitsune.yaml` | `%APPDATA%\kitsune-whisper\kitsune.yaml` |
| Spool | `~/.cache/kitsune-whisper/spool` | `%LOCALAPPDATA%\kitsune-whisper\spool` |
| Log | `~/.cache/kitsune-whisper/client.log` | `%LOCALAPPDATA%\kitsune-whisper\client.log` |

An explicit `--config PATH`, `KITSUNE_CONFIG`, or a `./kitsune.yaml` in the
current directory overrides the default config location. Validate a config
without starting:

```sh
kitsune-client --check-config
kitsune-client --version
```

### First dictation

Once a Server is reachable at `client.server_url`, start the Client:

```sh
kitsune-client run          # run is also the default command
```

Press **Ctrl+Shift+Space** to start and stop recording; **Esc** cancels without
injecting. See [The hotkey](../README.md#the-hotkey) for push-to-talk and
changing the keys.

Handy subcommands:

```sh
kitsune-client record --seconds 5   # capture once and print the text
kitsune-client transcribe-file f.wav  # transcribe an existing WAV
kitsune-client toggle               # external trigger (Wayland compositor bind)
```

See [Configuration](configuration.md) for the full `kitsune.yaml` reference and
[Wayland support and known limitations](wayland.md) for the Linux session matrix.

### Autostart

The client runs in the logged-in **user session**, never as a system/SCM
service — a service cannot see the user's hotkeys or clipboard
(`docs/adr/0001-client-runs-in-user-session.md`). The installers register
autostart by calling the installed binary:

```sh
kitsune-client install-autostart      # optional --config PATH pins a config
kitsune-client uninstall-autostart
```

- **Linux** — a systemd **user** unit at
  `~/.config/systemd/user/kitsune-client.service`, `WantedBy`/`After`
  `graphical-session.target`, `Restart=on-failure`. No linger (linger would start
  it at boot with no display). The installer also writes the
  `io.github.FoxRed-cmd.kitsune-whisper.desktop` file the Wayland
  GlobalShortcuts portal needs.
- **Windows** — a per-user **Task Scheduler** task `kitsune-client` at logon,
  running interactively with least privilege and restart-on-failure. If task
  registration is denied, it falls back to the
  `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` value `kitsune-whisper`.

### Managing the running client

`kitsune-client run` is a long-lived foreground process: started from a terminal
it holds the terminal until you stop it, so prefer the installed autostart. On
Linux that is a systemd **user** unit:

```sh
systemctl --user status kitsune-client.service          # running? last log lines
systemctl --user restart kitsune-client.service         # after editing kitsune.yaml
systemctl --user stop kitsune-client.service            # stop now, keep autostart
systemctl --user disable --now kitsune-client.service   # stop and don't start at login
systemctl --user enable --now kitsune-client.service    # re-enable and start
journalctl --user -u kitsune-client.service -e          # the service log
```

On Windows it is the Task Scheduler task `kitsune-client`:

```powershell
Get-ScheduledTask -TaskName kitsune-client        # state
Start-ScheduledTask   -TaskName kitsune-client
Stop-ScheduledTask    -TaskName kitsune-client
Disable-ScheduledTask -TaskName kitsune-client    # don't start at logon
Enable-ScheduledTask  -TaskName kitsune-client
```

`kitsune-client uninstall-autostart` removes the registration on either platform.
Configuration is read at startup, so restart the service after editing
`kitsune.yaml`. To trigger the running client by hand (the external trigger), run
`kitsune-client toggle`; the client's own log — selected backend, Wayland
fallbacks, injections — is at `~/.cache/kitsune-whisper/client.log`
(`%LOCALAPPDATA%\kitsune-whisper\client.log` on Windows).

### Upgrade

Re-run the installer. It replaces the binary and refreshes the unit/task and the
`.desktop` file, and leaves `kitsune.yaml` untouched. Config-schema changes are
surfaced by the fail-fast validation (`--check-config`).

### Uninstall

```sh
# Linux
curl -fsSL .../install.sh | bash -s -- --uninstall          # keeps config/spool/logs
curl -fsSL .../install.sh | bash -s -- --uninstall --purge  # also removes them
```

```powershell
# If you have the script locally:
./install.ps1 -Uninstall
./install.ps1 -Uninstall -Purge

# Or straight from the repository, matching the install one-liner:
& ([scriptblock]::Create((irm https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.ps1))) -Uninstall
```

Uninstall stops and removes the autostart entry and the binary; `--purge` /
`-Purge` additionally removes the config, Spool, and log.

## Server

The Server runs as a Docker container with `restart: unless-stopped`
(`compose.yaml`), so it comes back after a reboot or crash. Start it with one
profile:

```sh
cp kitsune.example.yaml kitsune.yaml
docker compose --profile cpu up -d    # or: --profile gpu
```

Upgrade:

```sh
docker compose pull && docker compose up -d
```

Uninstall (drops the downloaded models volume):

```sh
docker compose down -v
```

### Optional post-processing

The Server can optionally post-process each **Transcription** with an instruct
LLM, so speech is delivered cleaner or shorter:

- **Refine** removes filler words, stutters, and spoken self-corrections, and
  adds punctuation and casing, without paraphrasing or dropping content.
- **Summarize** compresses the text to its key points in plain prose.

A request asks for them per utterance (`refine` / `summarize`); the Client sends
those flags from `client.refine` / `client.summarize`. When both are set,
**Refine runs first, then Summarize**. It is a Server master switch — everything
is **off by default** (`server.processing.enabled: false`).

Processing is a thin HTTP client of any **OpenAI-compatible** chat-completions
endpoint: it POSTs to `{base_url}/v1/chat/completions`. One code path backs the
bundled local sidecar, an LLM service you already run (Ollama, LM Studio, another
host), and a cloud API.

Processing can never break transcription. If the feature is disabled, the
endpoint is unreachable, a step raises, exceeds
`server.processing.stage_timeout_seconds`, or returns empty text, the affected
step is skipped: the response keeps the previous stage's text (ultimately the raw
**Transcription**), reports `applied: {refine, summarize}` for what actually ran,
and lists the reason in `warnings` (which the Client logs). `GET /health`
advertises the capability as a `processing: {enabled, refine, summarize}` block.

#### Run the bundled local LLM

The repo ships an opt-in `llm` profile with a pure llama.cpp sidecar on the
official `ghcr.io/ggml-org/llama.cpp` images — no build step, nothing to compile.
Set `server.processing.enabled: true` in `kitsune.yaml`, then start the Server
together with the matching sidecar:

```sh
# CPU
docker compose up -d server-cpu llama-cpu
# GPU (needs the NVIDIA driver + nvidia-container-toolkit)
docker compose up -d server-gpu llama-gpu
```

Targeting the Server and sidecar together starts exactly those two (and
auto-enables their profiles). The sidecar answers under the stable alias `llama`,
which the Server services already point at via
`KITSUNE_SERVER_PROCESSING__BASE_URL=http://llama:8080`; its model download is
cached in the shared `kitsune-models` volume, so restarts reuse it.

Tune the sidecar without editing compose, via a `.env` file next to
`compose.yaml`:

| Variable | Default | Meaning |
| --- | --- | --- |
| `LLM_MODEL_REPO` | `Qwen/Qwen3-0.6B-GGUF` | GGUF repository. |
| `LLM_MODEL_FILE` | `Qwen3-0.6B-Q8_0.gguf` | GGUF file within it. |
| `LLM_GPU_LAYERS` | `-1` | `-ngl` offload; `-1` = all, `0` = none (ignored by the CPU image). |
| `LLM_OFFLINE` | unset | Non-empty adds `--offline` (air-gapped, from a warm cache). |

The sidecar disables Qwen3's thinking mode itself (`--reasoning off`), so the
Server sends the same portable request to any provider.

#### Use an external endpoint

To reuse an LLM you already run — Ollama, LM Studio, another host, or a cloud
API — point `base_url` at it and set `enabled: true`. For example, Ollama on the
same host:

```yaml
server:
  processing:
    enabled: true
    base_url: http://127.0.0.1:11434
    model: qwen2.5:1.5b
```

A cloud provider also needs its `api_key` (sent as `Authorization: Bearer`) and
the right `model` name; `extra_body` merges arbitrary provider fields into each
request. The Server reads these from its environment, so you can keep secrets
and per-host addresses out of `kitsune.yaml` — for Docker, add them to the
Server service's `environment:` (a `compose.override.yaml` is picked up
automatically):

```yaml
# compose.override.yaml
services:
  server-cpu:
    environment:
      KITSUNE_SERVER_PROCESSING__BASE_URL: https://api.example.com
      KITSUNE_SERVER_PROCESSING__API_KEY: sk-...
      KITSUNE_SERVER_PROCESSING__MODEL: gpt-4o-mini
```

The full field list — `enabled`, `base_url`, `api_key`, `model`, `extra_body`,
`max_output_tokens`, `stage_timeout_seconds` — is in the
[configuration reference](configuration.md#server-section).

> **Privacy:** the built-in default `base_url` is loopback
> (`http://127.0.0.1:8080`), so a Server run on its own keeps processing on the
> machine. An **external `base_url`** (a different host or a cloud API) sends
> the transcribed **text off the host**; audio still never leaves your network.
> The bundled sidecar runs beside the Server in the same compose project, so
> keep the privacy call in mind when deciding where that project runs.

The removed in-process keys `model_repo`, `model_file`, and `gpu_layers` are now
a **startup error** (`--check-config` names them). They moved to the sidecar's
`.env` / compose flags above.

### Recommended hardware

An **NVIDIA/CUDA GPU is recommended**. On the measured host (Ryzen 7 8700F,
RTX 4060) the default `small` model transcribes at ≈0.02–0.2× realtime — near
instant. CPU-only hosts are **supported but slower**, at ≈0.6–0.7× realtime.

- **GPU** — any recent NVIDIA driver; the container ships the CUDA 12.3 /
  cuDNN 9 runtime. `compute_type: auto` resolves to `float16`. On a low-VRAM GPU
  set `server.compute_type: int8_float16` to fit the model.
- **CPU** — `compute_type: auto` resolves to `int8`. For lower latency set
  `server.model` to `base` (the best accuracy↔speed trade for dictation, roughly
  2–3× faster than `small`) or `tiny` on the weakest hosts.

The `base`/`tiny` figures are **derived estimates, not measured** — validate them
on your own host. The Server logs a `WARN` when it resolves to CPU, and
`GET /health` reports the resolved `device`/`compute_type` so you can confirm the
GPU is actually in use.

For GPU prerequisites, see [GPU prerequisites](../README.md#gpu-prerequisites).
For the full config surface, see [Configuration](configuration.md).
