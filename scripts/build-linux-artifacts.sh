#!/usr/bin/env bash
# Build the Linux release artifacts locally, with the same names and layout that
# install.sh and release CI use:
#
#   kitsune-client_linux_amd64.tar.gz          GTK4/WebKitGTK 6.0 Client
#   kitsune-client_linux_amd64-legacy.tar.gz   GTK3/WebKit2GTK 4.1 Client
#   kitsune-server_linux_amd64.tar.gz          native Server bundle
#   kitsune-wayland_linux_amd64.tar.gz         ydotool/ydotoold/wtype
#   kitsune-whisper_<version>_amd64.deb        Debian/Ubuntu package (optional)
#   kitsune-whisper-<version>-1.x86_64.rpm     Fedora/RHEL package (optional)
#   kitsune-whisper-<version>-x86_64.AppImage  portable AppImage (optional)
#   checksums.txt
#
#   scripts/build-linux-artifacts.sh --output dist
#
# The Client needs a C toolchain (hotkey uses cgo). The Server bundle needs a
# standalone runtime and a resolved site-packages directory; without them the
# server artifact is skipped. Wayland helpers come from --wayland-dir or from
# the ydotool/wtype already on PATH (handy for a local smoke test). The RPM and
# AppImage are built when rpmbuild and appimagetool (or $APPIMAGETOOL) exist.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUTPUT_DIR="dist"
VERSION="${KITSUNE_VERSION:-$(git -C "$REPO_ROOT" describe --tags --always 2>/dev/null || echo 0.1.0)}"
GO="${GO:-go}"
CC_VALUE="${CC:-}"
NO_CLIENT=0
NO_SERVER=0
NO_WAYLAND=0
SERVER_RUNTIME="${KITSUNE_SERVER_RUNTIME:-}"
SERVER_SITE_PACKAGES="${KITSUNE_SERVER_SITE_PACKAGES:-}"
SERVER_PYTHON="${KITSUNE_SERVER_PYTHON:-python3}"
WAYLAND_DIR="${KITSUNE_WAYLAND_DIR:-}"
BUILD_DEB=1
BUILD_RPM=1
BUILD_APPIMAGE=1

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
    --wayland-dir)
      shift; [[ $# -gt 0 ]] || { echo "error: --wayland-dir needs a directory" >&2; exit 2; }
      WAYLAND_DIR="$1" ;;
    --no-client) NO_CLIENT=1 ;;
    --no-server) NO_SERVER=1 ;;
    --no-wayland) NO_WAYLAND=1 ;;
    --no-deb) BUILD_DEB=0 ;;
    --no-rpm) BUILD_RPM=0 ;;
    --no-appimage) BUILD_APPIMAGE=0 ;;
    -h|--help)
      sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
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

GTK4_CLIENT_BIN=""
GTK3_CLIENT_BIN=""
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
  if [[ "$asset" == "kitsune-client_linux_${ARCH}.tar.gz" ]]; then
    GTK4_CLIENT_BIN="$stage/kitsune-client"
  else
    GTK3_CLIENT_BIN="$stage/kitsune-client"
  fi
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

build_wayland() {
  local stage="$work/wayland"
  mkdir -p "$stage/bin"
  if [[ -n "$WAYLAND_DIR" ]]; then
    for tool in ydotool ydotoold wtype; do
      [[ -x "$WAYLAND_DIR/$tool" ]] && cp "$WAYLAND_DIR/$tool" "$stage/bin/"
    done
  else
    for tool in ydotool ydotoold wtype; do
      if command -v "$tool" >/dev/null 2>&1; then
        cp "$(command -v "$tool")" "$stage/bin/$tool"
      fi
    done
  fi
  if [[ -z "$(ls -A "$stage/bin")" ]]; then
    echo "note: no Wayland helpers found; skipping the bundle (install ydotool/wtype)"
    return 0
  fi
  chmod 0755 "$stage/bin/"*
  tar -C "$stage" -czf "$OUTPUT_DIR/kitsune-wayland_linux_${ARCH}.tar.gz" bin
  echo "Wrote $OUTPUT_DIR/kitsune-wayland_linux_${ARCH}.tar.gz"
}

if [[ "$NO_WAYLAND" == 0 ]]; then
  build_wayland
fi

server_asset="$OUTPUT_DIR/kitsune-server_linux_${ARCH}.tar.gz"
wayland_asset="$OUTPUT_DIR/kitsune-wayland_linux_${ARCH}.tar.gz"
package_assets=()
[[ -f "$server_asset" ]] && package_assets+=(--server-artifact "$server_asset")
[[ -f "$wayland_asset" ]] && package_assets+=(--wayland-artifact "$wayland_asset")

build_package_variant() {
  local builder="$1" client="$2" label="$3"
  shift 3
  "$builder" --client "$client" "$@" \
    --version "$VERSION" --output "$OUTPUT_DIR" "${package_assets[@]}" \
    || echo "note: the $label build failed; the other artifacts are still usable"
}

if [[ "$BUILD_DEB" == 1 && "$NO_CLIENT" == 0 && -n "$GTK4_CLIENT_BIN" ]]; then
  if [[ -x "$REPO_ROOT/packaging/deb/build-deb.sh" ]] && command -v ar >/dev/null 2>&1; then
    build_package_variant "$REPO_ROOT/packaging/deb/build-deb.sh" "$GTK4_CLIENT_BIN" ".deb"
  fi
fi

if [[ "$BUILD_RPM" == 1 && "$NO_CLIENT" == 0 && -n "$GTK4_CLIENT_BIN" ]]; then
  if [[ -x "$REPO_ROOT/packaging/rpm/build-rpm.sh" ]] && command -v rpmbuild >/dev/null 2>&1; then
    build_package_variant "$REPO_ROOT/packaging/rpm/build-rpm.sh" "$GTK4_CLIENT_BIN" ".rpm"
    if [[ -n "$GTK3_CLIENT_BIN" ]]; then
      build_package_variant "$REPO_ROOT/packaging/rpm/build-rpm.sh" "$GTK3_CLIENT_BIN" "legacy .rpm" --legacy
    fi
  else
    echo "note: rpmbuild is not installed; skipping the .rpm build"
  fi
fi

if [[ "$BUILD_APPIMAGE" == 1 && "$NO_CLIENT" == 0 && -n "$GTK4_CLIENT_BIN" ]]; then
  appimagetool_bin="${APPIMAGETOOL:-$(command -v appimagetool 2>/dev/null || true)}"
  if [[ -x "$REPO_ROOT/packaging/appimage/build-appimage.sh" && -n "$appimagetool_bin" ]]; then
    build_package_variant "$REPO_ROOT/packaging/appimage/build-appimage.sh" "$GTK4_CLIENT_BIN" \
      "AppImage" --appimagetool "$appimagetool_bin"
    if [[ -n "$GTK3_CLIENT_BIN" ]]; then
      build_package_variant "$REPO_ROOT/packaging/appimage/build-appimage.sh" "$GTK3_CLIENT_BIN" \
        "legacy AppImage" --legacy --appimagetool "$appimagetool_bin"
    fi
  else
    echo "note: appimagetool is not installed; skipping the AppImage build"
  fi
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
