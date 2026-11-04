#!/usr/bin/env bash
# Build the Linux release artifacts locally, with the same names and layout that
# install.sh and release CI use:
#
#   kitsune-client_linux_amd64.tar.gz          GTK4/WebKitGTK 6.0 Client
#   kitsune-client_linux_amd64-legacy.tar.gz   GTK3/WebKit2GTK 4.1 Client
#   kitsune-server_linux_amd64.tar.gz          native Server bundle
#   checksums.txt
#
#   scripts/build-linux-artifacts.sh --output dist
#
# The Client needs a C toolchain (hotkey uses cgo). The Server bundle needs a
# standalone runtime and a resolved site-packages directory; without them the
# server artifact is skipped.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_DIR="dist"
# VERSION is the single source of truth for local builds; release CI passes the
# tag explicitly. git describe remains a fallback for an older checkout.
VERSION="${KITSUNE_VERSION:-$(cat "$REPO_ROOT/VERSION" 2>/dev/null || git -C "$REPO_ROOT" describe --tags --always 2>/dev/null || echo 0.1.0)}"
GO="${GO:-go}"
CC_VALUE="${CC:-}"
NO_CLIENT=0
NO_SERVER=0
SERVER_RUNTIME="${KITSUNE_SERVER_RUNTIME:-}"
SERVER_SITE_PACKAGES="${KITSUNE_SERVER_SITE_PACKAGES:-}"
SERVER_PYTHON="${KITSUNE_SERVER_PYTHON:-python3}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output)
      shift; [[ $# -gt 0 ]] || { echo "error: --output needs a directory" >&2; exit 2; }
      OUTPUT_DIR="$1" ;;
    --version)
      shift; [[ $# -gt 0 ]] || { echo "error: --version needs a value" >&2; exit 2; }
      VERSION="$1" ;;
    --runtime)
      shift; [[ $# -gt 0 ]] || { echo "error: --runtime needs a directory" >&2; exit 2; }
      SERVER_RUNTIME="$1" ;;
    --site-packages)
      shift; [[ $# -gt 0 ]] || { echo "error: --site-packages needs a directory" >&2; exit 2; }
      SERVER_SITE_PACKAGES="$1" ;;
    --server-python)
      shift; [[ $# -gt 0 ]] || { echo "error: --server-python needs a path" >&2; exit 2; }
      SERVER_PYTHON="$1" ;;
    --no-client) NO_CLIENT=1 ;;
    --no-server) NO_SERVER=1 ;;
    -h|--help)
      sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'
      exit 0 ;;
    *) echo "error: unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done

mkdir -p "$OUTPUT_DIR"
OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd)"
work="$(mktemp -d "${TMPDIR:-/tmp}/kitsune-artifacts.XXXXXX")"
trap 'rm -rf "$work"' EXIT

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "error: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

build_client() {
  local tags="$1" asset="$2"
  local stage="$work/client-$asset"
  mkdir -p "$stage"
  echo "Building $asset..."
  local env_prefix=(env "CGO_ENABLED=1")
  if [[ -n "$CC_VALUE" ]]; then env_prefix+=("CC=$CC_VALUE"); fi
  "${env_prefix[@]}" "$GO" build -C "$REPO_ROOT/client" -trimpath \
    -tags "$tags" \
    -ldflags "-X github.com/FoxRed-cmd/kitsune-whisper/client/internal/cli.Version=$VERSION" \
    -o "$stage/kitsune-client" ./cmd/kitsune-client
  cp "$REPO_ROOT/kitsune.example.yaml" "$stage/"
  tar -C "$stage" -czf "$OUTPUT_DIR/$asset" kitsune-client kitsune.example.yaml
  echo "Wrote $OUTPUT_DIR/$asset"
}

if [[ "$NO_CLIENT" == 0 ]]; then
  command -v "$GO" >/dev/null 2>&1 || { echo "error: go is required" >&2; exit 1; }
  build_client "desktop purego" "kitsune-client_linux_${ARCH}.tar.gz"
  build_client "desktop purego gtk3" "kitsune-client_linux_${ARCH}-legacy.tar.gz"
fi

build_server() {
  [[ -n "$SERVER_RUNTIME" ]] || { echo "note: --runtime not set; skipping the Server bundle"; return 0; }
  [[ -n "$SERVER_SITE_PACKAGES" ]] || { echo "note: --site-packages not set; skipping the Server bundle"; return 0; }
  "$SERVER_PYTHON" -c 'import kitsune_server' >/dev/null 2>&1 \
    || { echo "error: $SERVER_PYTHON cannot import kitsune_server; use the repository venv" >&2; exit 1; }
  echo "Building the native Server bundle..."
  PYTHONPATH="$REPO_ROOT/server${PYTHONPATH:+:$PYTHONPATH}" "$SERVER_PYTHON" -m kitsune_server bundle build \
    --output "$OUTPUT_DIR/kitsune-server_linux_${ARCH}.tar.gz" \
    --runtime "$SERVER_RUNTIME" \
    --site-packages "$SERVER_SITE_PACKAGES" \
    --version "$VERSION"
}

if [[ "$NO_SERVER" == 0 ]]; then
  build_server
fi

(
  cd "$OUTPUT_DIR"
  : > checksums.txt
  for file in kitsune-*; do
    [[ -f "$file" && "$file" != checksums.txt ]] || continue
    sha256sum "$file" >> checksums.txt
  done
)
echo "Wrote $OUTPUT_DIR/checksums.txt"
