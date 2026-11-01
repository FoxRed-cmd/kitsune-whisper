#!/usr/bin/env bash
# Build the Wayland injection helpers the Linux installer bundles:
#   ydotool + ydotoold (uinput; works on every compositor)
#   wtype            (wlroots virtual-keyboard)
#
# Release CI runs this on Ubuntu; a developer can run it where cmake, meson,
# ninja, libwayland-dev, libxkbcommon-dev, and wayland-protocols are installed.
#
#   scripts/build-wayland-tools.sh --output dist
#
# The output archive is kitsune-wayland_linux_amd64.tar.gz with bin/ at its root,
# matching what install.sh expects.
set -euo pipefail

OUTPUT_DIR="dist"
YDOTOOL_VERSION="${YDOTOOL_VERSION:-v1.0.4}"
WTYPE_VERSION="${WTYPE_VERSION:-v0.4}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --output)
      shift
      [[ $# -gt 0 ]] || { echo "error: --output needs a directory" >&2; exit 2; }
      OUTPUT_DIR="$1"
      ;;
    -h|--help)
      sed -n '2,10p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      exit 2
      ;;
  esac
  shift
done

need() { command -v "$1" >/dev/null 2>&1 || { echo "error: $1 is required" >&2; exit 1; }; }
need git
need cmake
need meson
need ninja

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "error: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

work="$(mktemp -d "${TMPDIR:-/tmp}/kitsune-wayland.XXXXXX")"
trap 'rm -rf "$work"' EXIT
stage="$work/root"
mkdir -p "$stage/bin"

echo "Building ydotool $YDOTOOL_VERSION..."
git clone --depth 1 --branch "$YDOTOOL_VERSION" https://github.com/ReimuNotMoe/ydotool.git "$work/ydotool"
cmake -S "$work/ydotool" -B "$work/ydotool/build" -DCMAKE_BUILD_TYPE=Release
cmake --build "$work/ydotool/build" --parallel
install -m 0755 "$work/ydotool/build/ydotool" "$stage/bin/ydotool"
install -m 0755 "$work/ydotool/build/ydotoold" "$stage/bin/ydotoold"

echo "Building wtype $WTYPE_VERSION..."
git clone --depth 1 --branch "$WTYPE_VERSION" https://github.com/atx/wtype.git "$work/wtype"
meson setup "$work/wtype/build" "$work/wtype" --buildtype=release
meson compile -C "$work/wtype/build"
install -m 0755 "$work/wtype/build/wtype" "$stage/bin/wtype"

for tool in ydotool ydotoold wtype; do
  [[ -x "$stage/bin/$tool" ]] || { echo "error: $tool was not built" >&2; exit 1; }
done

mkdir -p "$OUTPUT_DIR"
asset="$OUTPUT_DIR/kitsune-wayland_linux_${ARCH}.tar.gz"
tar -C "$stage" -czf "$asset" bin
echo "Wrote $asset"
