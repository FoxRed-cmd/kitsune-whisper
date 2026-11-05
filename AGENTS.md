## Agent skills

### Issue tracker

Issues and specs live as GitHub issues, managed via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default triage vocabulary, mapped 1:1 to the five canonical roles. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one root `GLOSSARY.md` plus `docs/adr/`. See `docs/agents/domain.md`.

## Build, lint & typecheck

- **Environment** — `mise install` provisions the pinned toolchain and `mise run setup` also creates `server/.venv` (see `mise.toml` and `docs/development.md`). `mise run check` mirrors CI; `mise tasks` lists the shortcuts. Linux system headers/runtime packages come from `mise run deps:install`.
- **Go (client)** — run from `client/`: `go build ./...`, `go test ./...`; lint/format with `golangci-lint run` (config `.golangci.yml` at the repo root). `malgo` uses cgo: mise sets `CC="zig cc"` when no `gcc` is on `PATH`, which also clears gopls's false `undefined: malgo.*` errors. The desktop-tagged adapter tests run separately: `go test -tags "desktop purego" ./internal/shell/wails` (`mise run test:desktop`).
- **Windows client build** — build with `packaging/windows/build-client.ps1` rather than a bare `go build`: it sets the GUI subsystem (cgo's external linker ignores `-H=windowsgui`) and generates the icon and version resources.
- **Python (server)** — run from `server/`: `ruff check .` and `ruff format --check .` (config `ruff.toml` at the repo root); types with `basedpyright` (config lives in `server/pyproject.toml`; the root `pyrightconfig.json` is for the editor).
- **Search** — Python deps live in `server/.venv`; use the Glob/Grep tools (they honour `.gitignore`) instead of a recursive shell listing of `server/`.
- **CI** — `.github/workflows/ci.yml` runs all of the above on push/PR.
- **LSP** — `gopls` (Go) and `basedpyright` (Python) are wired up in `opencode.json`; mise installs `gopls` and adds `server/.venv/bin` (which holds `basedpyright-langserver`) to `PATH`.
- Config and tool changes are loaded once at opencode startup: **restart opencode** after editing `opencode.json` or installing tools.
