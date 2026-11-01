#!/usr/bin/env bash
# Real-container Docker parity smoke (issue #78). Needs Docker and a network
# for the model download; see docs/verification/docker-smoke.md.
set -euo pipefail

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
exec python3 "$root/scripts/docker_smoke.py" "$@"
