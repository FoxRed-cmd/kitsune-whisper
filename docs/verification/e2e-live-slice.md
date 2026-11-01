# End-to-end live slice (issue #22)

Verification that a real Server and the real Client run the complete Dictation
cycle, plus the silence and error paths. The automated half is
[`scripts/e2e_live.py`](../scripts/e2e_live.py) (driven by `scripts/e2e-live.ps1`
on Windows and `scripts/e2e-live.sh` on Linux); the desktop half (global hotkey,
microphone, injection into a GUI editor and a terminal) is a manual checklist at
the bottom, because it cannot be observed from an automated session.

## What the harness covers

It brings up a real Server (local Python or Docker, CPU or GPU), synthesizes a
speech WAV (Windows SAPI / espeak-ng, or `--speech <file>`), and checks:

- The authenticated `/status` reports `model`/`device`/`compute_type`/`ready`,
  and `/health` reports liveness and the version without a token.
- A speech WAV round-trips through `POST /transcribe` and returns non-empty text.
- The real Client binary (`kitsune-client transcribe-file`) transcribes the same
  WAV, and reports an unreachable Server as an error.
- An optional microphone pass (`--capture`) runs `kitsune-client record`, which
  exercises the real malgo capture → resample → POST path.
- A silent utterance returns empty `text` (the server-side VAD silence gate,
  which is on by default).
- An undecodable upload is rejected with the `{error:{code,message}}` envelope.
- Latency is measured (median wall time over `--repeats` runs) and reported.

## Run

```sh
# Windows
./scripts/e2e-live.ps1 -Profile local-cpu -Report e2e-cpu.md
./scripts/e2e-live.ps1 -Profile local-gpu -Capture -Report e2e-gpu.md

# Linux
./scripts/e2e-live.sh --profile local-cpu --report e2e-cpu.md
./scripts/e2e-live.sh --profile local-gpu --capture --report e2e-gpu.md

# Against an already-running Server
./scripts/e2e-live.ps1 -Profile external -ServerUrl http://192.168.1.10:8765
```

Profiles: `local-cpu` (default), `local-gpu`, `docker-cpu`, `docker-gpu`,
`external`. The local profiles reuse the Hugging Face cache at
`~/.cache/huggingface/hub` unless `KITSUNE_SERVER_DOWNLOAD_ROOT` is set; pass
`--server-config` to use an existing `kitsune.yaml` instead. The harness always
stops the Server it started (use `--keep` to leave it up).

> **The `docker-*` profiles are a latency harness, not a parity test.** They
> write a config with `tls.enabled: false` and `allow_anonymous: true` so the
> timed requests need no certificate or Device token. They cannot catch an auth,
> pairing, Admin API, restart, or persistence regression. For the authenticated
> Docker contract use [`docker-smoke.md`](docker-smoke.md).

The exit code is non-zero if any check fails, so the harness can gate CI on a
machine with the models cached.

## Docker parity smoke

`scripts/docker_smoke.py` (wrapped by `scripts/docker-smoke.sh`) runs the
authenticated flow against a real container: no unauthenticated access,
`docker exec ... kitsune-server pair`, redemption, authenticated status, the
model catalog and transcription, an Admin config write through the writable
`/config` directory, soft-stop (`503` + container alive), restart (`202` +
container restart), and recreation with the certificate, Devices, models,
config, and soft-stop flag intact. See
[`docker-smoke.md`](docker-smoke.md). It is opt-in (workflow dispatch/weekly),
not part of PR CI, and requires Docker plus a model download.

## Recorded results

Host: Windows 11, Ryzen 7 8700F, RTX 4060 8 GB, USB Audio Device microphone,
model `small`, 9.10 s SAPI speech WAV, 2026-10-05.

| Profile | Device / compute | Median wall | Realtime ratio | Checks |
| --- | --- | --- | --- | --- |
| `local-cpu` | `cpu` / `int8` | 2.1–2.7 s | 0.23–0.30× | 9/9 |
| `local-gpu` | `cuda` / `float16` | 0.32 s | 0.03–0.04× | 9/9 |

Sanity check against the prototype (#8), which measured `small` at ≈0.6–0.7× on
CPU and ≈0.02–0.2× on GPU:

- GPU agrees: near-instant, sub-second for a 9 s utterance.
- CPU is faster here than the prototype's figure. The prototype timed the whole
  request including model load on a cold process; this harness warms the model
  and reports median request latency. Either way CPU is usable but the gap to
  GPU is two orders of magnitude.

Both profiles also passed the silence check (`text=""`), the undecodable-upload
`415` envelope, the real-Client round trip, the unreachable-Server error, and the
`--capture` microphone pass on this host.

Manual Windows pass (2026-10-05, same host): the Client's log shows injection
into Notepad++ (`ctrl_v`, focused app `notepad++.exe`) and Windows Terminal
(`ctrl_shift_v`, terminal `WindowsTerminal.exe`), with the transcription left on
the clipboard as documented for Windows. Toggle and transcription were confirmed
live.

## Fixes found by this verification

Two live regressions surfaced that the unit-test seams could not reach:

1. **Silent utterances hallucinated.** `server.decode.vad_filter` defaulted to
   `false`, contradicting #10 (server-side VAD is the authoritative silence
   gate, always on). A 3 s silent utterance transcribed to `"You"`. The default
   is now `true`; the silent utterance returns `""`. The harness deliberately
   does not set `vad_filter`, so this check exercises the default.
2. **Windows capture panicked.** `internal/mic/malgo.go` stored a Go pointer
   (`unsafe.Pointer(&id)`) in the device config handed to C, which trips cgo's
   "Go pointer to unpinned Go pointer" check whenever a device is selected by ID
   (the default path). The microphone pass crashed the Client on every start.
   The device ID is now copied into C memory via `DeviceID.Pointer()` and cached
   per device index.

## Manual checklist

The hotkey, live microphone speech, and injection must be checked by a person on
each desktop. Build the Client first:

```sh
# Windows (zig) — or any C compiler
$env:CGO_ENABLED=1; $env:CC="zig cc"; go build ./cmd/kitsune-client
# Linux
CGO_ENABLED=1 go build ./cmd/kitsune-client
```

Then run `kitsune-client run` against a running Server and, for each row, put the
cursor in the app, press the hotkey, speak, press it again.

### Windows

Verified 2026-10-05 (Client log):

- [x] Notepad++ (GUI editor): injected via `Ctrl+V`; the clipboard holds the
      transcription (restore is impossible on Windows).
- [x] Windows Terminal: injected via the auto-selected `Ctrl+Shift+V`.
- [x] Toggle starts and stops the cycle; speech transcribes and injects.
- [ ] `Ctrl+Shift+Q` cancels an in-progress utterance with no injection.
- [x] Push-to-talk is no longer offered; a legacy `trigger: hold` file loads as
      toggle and the hotkey still starts and stops the cycle.
- [x] No injection on an empty/silent utterance (verified live); injection never
      runs, so the clipboard is untouched.

### Linux X11

- [ ] `xterm`/`gnome-terminal` (terminal): text is injected via `Ctrl+Shift+V`.
- [ ] A GUI editor (e.g. `gedit`/`kate`): text is injected via `Ctrl+V`.
- [ ] The previous clipboard is restored after injection (`clipboard_restore`).
- [ ] Hotkey, toggle, and `Ctrl+Shift+Q` behave as on Windows.
- [ ] `kitsune-client toggle` from a script drives the cycle (External trigger).

### Wayland (best effort)

Out of v1's Windows + Linux X11 scope; see ADR 0002 for the portal hotkey and
`wtype`/`ydotool`/clipboard-only injection chain.

## Two-machine pairing and revocation

Run the Server on one machine and the Client on another on the same LAN. The
manual pass is tracked in issue #84 and is run once the desktop work is
complete:

1. On the Server host, run `kitsune-server expose on --yes` and restart the
   Server. Check that `https://<server>:8765/health` answers from the laptop.
2. In the Client's **Settings → Servers and Devices**, generate a Pairing code
   (or run `kitsune-server pair` on the Server host) and note the fingerprint.
3. On the laptop, enter `https://<server>:8765`, the code, and a Device name;
   press **Pair**. The profile becomes active.
4. Dictate once: the Server host's log shows the request and the laptop's Device
   name. Restart both processes and dictate again without re-pairing.
5. In the Devices list, revoke the laptop. Its next dictation shows the
   "revoked, pair again" error and the Server returns `401`.
6. Generate a new code and pair the laptop again; dictation works.
