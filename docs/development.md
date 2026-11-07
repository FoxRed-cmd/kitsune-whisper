# Development

The repository ships a [mise](https://mise.jdx.dev/) configuration in
`mise.toml`. It pins the toolchain and exposes the same commands CI runs, so a
fresh checkout needs one setup step instead of a hand-written tool list.

## One-time setup

```sh
mise trust             # approve mise.toml the first time
mise run deps:install  # Linux build + desktop runtime packages (uses sudo)
mise run setup         # toolchains and server/.venv
```

`deps:install` installs a C compiler, the X11 and ALSA **headers** (the hotkey
and microphone use cgo), and the GTK4/WebKitGTK 6.0 **runtime** for the Settings
window. It supports `dnf`, `apt`, `pacman`, and `zypper`. Use
`mise run deps:check` to only report what is missing.

`mise run setup` installs the pinned tools, creates `server/.venv` with Python
3.11, and finishes with `deps:check`. It is safe to re-run.

## Tasks

| Command | What it does |
| --- | --- |
| `mise run setup` | Toolchains, `server/.venv`, system dependency report |
| `mise run deps:check` | Report missing Linux system packages |
| `mise run deps:install` | Install those packages (needs sudo) |
| `mise run build` | Build the desktop Client to `client/kitsune-client` |
| `mise run build:server` | Build the Server wheel into `dist-local/` |
| `mise run build:artifacts` (`mise run dist`) | Full Linux release set in `dist/` |
| `mise run build:windows` | Windows Client with GUI subsystem (Windows only) |
| `mise run spec` | Regenerate `server/openapi.json` and the Client's wire types |
| `mise run spec:check` | Fail when either committed generated file is stale |
| `mise run test` | Client and Server test suites |
| `mise run test:desktop` | Desktop-tagged Wails adapter tests |
| `mise run lint` | `golangci-lint` (both tag sets) and `ruff check` |
| `mise run typecheck` | `basedpyright` |
| `mise run fmt` | `gofmt -w` and `ruff format` |
| `mise run fmt:check` | Check formatting without changing files |
| `mise run check` | Everything CI runs: format, wire contract, lint, types, tests |
| `mise run run:server` | Run the Server in the foreground |
| `mise run run:client` | Build and run the dictation loop |
| `mise run run:client-gui` | Build and run with the Settings window and Tray |
| `mise run e2e:live` | Live dictation slice (needs a model and a desktop) |
| `mise run e2e:trust` | Pairing/trust slice (needs a model) |

`mise tasks` lists every task with its description. Arguments after `--` are
passed to the script, for example:

```sh
mise run e2e:live -- --profile local-cpu --report e2e-cpu.md
```

The `run:*` tasks copy `kitsune.example.yaml` to the gitignored `kitsune.yaml`
when it does not exist yet.

## Wire contract

The Server owns the JSON contract (ADR 0014). Response models live in
`server/kitsune_server/api_models.py`; `mise run spec` writes
`server/openapi.json` and regenerates the Client's `client/internal/api` types.
Commit both files with the model change. CI regenerates them and fails on any
diff, so a renamed or forgotten field is visible in the pull request. The
generated Go file is the only codegen in the Client: transport, TLS pinning,
the Device token, multipart upload, and error classification stay hand-written.

## Full build in `dist/`

`mise run build:artifacts` (alias: `mise run dist`) builds the same layout the
release uses and puts it in the gitignored `dist/`:

```
dist/
  kitsune-client_linux_amd64.tar.gz
  kitsune-server_linux_amd64.tar.gz   # bundle with its own Python runtime
  checksums.txt
  install.sh
  VERSION
```

It resolves a standalone Python 3.11 runtime with uv, installs the Server into
`.bundle-venv`, builds both archives with `scripts/build-linux-artifacts.sh`,
and adds `install.sh` to the checksums. Install the local build with:

```sh
bash dist/install.sh --from dist --insecure
```

`--insecure` is required because local artifacts are not signed with the release
key. Use `--dry-run` first to see the plan; `--check` reports installed versus
available versions. Add `--client-only` or `--server-only` to install one
component. The task supports Linux only.

## Pinned tools

| Tool | Version | Reason |
| --- | --- | --- |
| Go | 1.26 | `client/go.mod` |
| uv | 0.13 | Server environment and bundling |
| Python | 3.11 (`UV_PYTHON`) | Same minor version as the release bundle |
| golangci-lint | 2.14.0 | Keep in sync with `.github/workflows/ci.yml` |
| zig | 0.17 | `CC="zig cc"` fallback when no gcc is installed |
| gopls | 0.23.0 | Newest release that builds with Go 1.26 |
| oapi-codegen | 2.8.2 | Generate the Client's wire types from `server/openapi.json` |

`CGO_ENABLED=1`, `GOTOOLCHAIN=local`, and the `CC` fallback live in the
`[env]` section. `server/.venv/bin` (or `server/.venv/Scripts` on Windows) is
added to `PATH`, which puts `ruff`, `basedpyright-langserver`, and `pytest`
where editors expect them.

Put machine-specific overrides in a gitignored `mise.local.toml`, for example:

```toml
[env]
CC = "clang"
```

## Platform notes

- **Linux** — `deps:install` covers the build headers and the desktop runtime.
  Without GTK4/WebKitGTK the Client still runs, but Settings opens in a browser.
- **Windows** — mise itself and the pinned tools work in PowerShell, including
  the OS-aware venv `Scripts` path and `CC="zig cc"`. Tasks that use POSIX
  shell syntax declare `shell = "bash -c"`, so mise needs Git Bash or MSYS2
  (Git for Windows ships one; set `MISE_BASH_PATH` if detection fails). Build
  the Client with `mise run build:windows`, which calls
  `packaging/windows/build-client.ps1` for the GUI subsystem, icon, and version
  resources; `build:artifacts`, `deps:*`, and the `e2e:*` shell scripts are
  Linux-only (Windows has its own `scripts/e2e-*.ps1`).
- **CI** — the workflows use the same commands without mise
  (`.github/workflows/ci.yml`); `mise run check` mirrors them locally.
