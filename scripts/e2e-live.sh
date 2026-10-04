#!/usr/bin/env bash
# Live end-to-end slice for kitsune-whisper (issue #22).
#
# Builds the real Client, then runs scripts/e2e_live.py, which brings up a real
# Server and drives the speech, silence, and error paths through the frozen HTTP
# contract.
#
#   scripts/e2e-live.sh --profile local-cpu --report e2e-cpu.md
#   scripts/e2e-live.sh --profile local-gpu --speech ./sample.wav
#
# Env: KITSUNE_E2E_CLIENT overrides the built binary path;
#      KITSUNE_E2E_NO_BUILD=1 reuses an existing binary.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
client="${KITSUNE_E2E_CLIENT:-${TMPDIR:-/tmp}/kitsune-e2e/kitsune-client}"

if [[ -z "${KITSUNE_E2E_NO_BUILD:-}" ]]; then
  mkdir -p "$(dirname "$client")"
  (cd "$root/client" && CGO_ENABLED=1 go build -o "$client" ./cmd/kitsune-client)
fi

if [[ -x "$root/server/.venv/bin/python" ]]; then
  python="$root/server/.venv/bin/python"
elif command -v python3 >/dev/null 2>&1; then
  python="python3"
else
  python="python"
fi

exec "$python" "$root/scripts/e2e_live.py" --client "$client" "$@"
