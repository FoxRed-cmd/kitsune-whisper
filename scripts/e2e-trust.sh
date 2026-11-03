#!/usr/bin/env bash
# Authenticated end-to-end slice for kitsune-whisper (issue #83).
#
# Builds the real Client, then runs scripts/e2e_trust.py, which starts a
# TLS-enabled Server and exercises local pairing, remote pairing and revocation,
# manual Server switch, soft-stop/start, supervised restart, an unreachable
# Server, and Spool recovery.
#
#   scripts/e2e-trust.sh --model small --report e2e-trust.md
#   scripts/e2e-trust.sh --speech ./sample.wav
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

exec "$python" "$root/scripts/e2e_trust.py" --client "$client" "$@"
