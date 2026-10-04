## Agent skills

### Issue tracker

Issues and specs live as GitHub issues, managed via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

Default triage vocabulary, mapped 1:1 to the five canonical roles. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: one root `GLOSSARY.md` plus `docs/adr/`. See `docs/agents/domain.md`.

## Build, lint & typecheck

- **Go (client)** — run from `client/`: `go build ./...`, `go test ./...`; lint/format with `golangci-lint run` (config `.golangci.yml` at the repo root). `malgo` uses cgo: with no `gcc` on `PATH`, prefix commands with `CC="zig cc"` (zig is installed), which also clears gopls's false `undefined: malgo.*` errors.
- **Python (server)** — run from `server/`: `ruff check .` and `ruff format --check .` (config `ruff.toml` at the repo root); types with `basedpyright` (config lives in `server/pyproject.toml`; the root `pyrightconfig.json` is for the editor).
- **Search** — Python deps live in `server/.venv`; use the Glob/Grep tools (they honour `.gitignore`) instead of a recursive shell listing of `server/`.
- **CI** — `.github/workflows/ci.yml` runs all of the above on push/PR.
- **LSP** — `gopls` (Go) and `basedpyright` (Python) are wired up in `opencode.json`; both must be on `PATH`.
- Config and tool changes are loaded once at opencode startup: **restart opencode** after editing `opencode.json` or installing tools.
