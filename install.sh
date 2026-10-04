#!/usr/bin/env bash
# kitsune-whisper Client installer (Linux).
#
#   curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash
#
# Installs ~/.local/bin/kitsune-client, writes a config template to
# ~/.config/kitsune-whisper/kitsune.yaml (only when absent), and registers a
# systemd user unit bound to graphical-session.target plus the portal .desktop
# file. Re-run it to upgrade; the config is left untouched. Run with --uninstall
# to remove the client, or --uninstall --purge to also drop config, spool, and
# logs.
set -euo pipefail

REPO="FoxRed-cmd/kitsune-whisper"
VERSION="${KITSUNE_VERSION:-latest}"
PREFIX="${KITSUNE_PREFIX:-$HOME/.local}"
BIN_DIR="$PREFIX/bin"
CONFIG_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/kitsune-whisper"
CACHE_DIR="${XDG_CACHE_HOME:-$HOME/.cache}/kitsune-whisper"
DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}"
BINARY="$BIN_DIR/kitsune-client"
CONFIG="$CONFIG_DIR/kitsune.yaml"

uninstall=0
purge=0
force_config=0
for arg in "$@"; do
  case "$arg" in
    --uninstall) uninstall=1 ;;
    --purge) purge=1 ;;
    --force-config) force_config=1 ;;
    -h|--help)
      sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "unknown argument: $arg" >&2
      exit 2
      ;;
  esac
done

if [[ "$uninstall" == 1 ]]; then
  if [[ -x "$BINARY" ]]; then
    "$BINARY" uninstall-autostart || echo "warning: could not remove autostart registration" >&2
  fi
  rm -f "$BINARY"
  rm -f "$DATA_DIR/applications/io.github.FoxRed-cmd.kitsune-whisper.desktop"
  if [[ "$purge" == 1 ]]; then
    rm -rf "$CONFIG_DIR" "$CACHE_DIR"
    echo "kitsune-client uninstalled (config, spool, and logs purged)"
  else
    echo "kitsune-client uninstalled (config, spool, and logs kept)"
  fi
  exit 0
fi

need() { command -v "$1" >/dev/null 2>&1 || { echo "error: $1 is required" >&2; exit 1; }; }
need curl
need sha256sum
need tar

case "$(uname -m)" in
  x86_64|amd64) arch=amd64 ;;
  *)
    echo "error: unsupported architecture $(uname -m); only linux/amd64 is published" >&2
    exit 1
    ;;
esac
asset="kitsune-client_linux_${arch}.tar.gz"

if [[ "$VERSION" == "latest" ]]; then
  base="https://github.com/$REPO/releases/latest/download"
else
  base="https://github.com/$REPO/releases/download/$VERSION"
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $asset ($VERSION)..."
curl -fsSL "$base/$asset" -o "$tmp/$asset"
curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt"

expected="$(awk -v name="$asset" '$2 == name { print $1 }' "$tmp/checksums.txt")"
if [[ -z "$expected" ]]; then
  echo "error: no checksum for $asset in checksums.txt" >&2
  exit 1
fi
actual="$(sha256sum "$tmp/$asset" | awk '{print $1}')"
if [[ "$actual" != "$expected" ]]; then
  echo "error: checksum mismatch for $asset" >&2
  echo "  expected $expected" >&2
  echo "  actual   $actual" >&2
  exit 1
fi

tar -xzf "$tmp/$asset" -C "$tmp"
mkdir -p "$BIN_DIR"
install -m 0755 "$tmp/kitsune-client" "$BINARY"

mkdir -p "$CONFIG_DIR"
if [[ ! -f "$CONFIG" || "$force_config" == 1 ]]; then
  if [[ -f "$tmp/kitsune.example.yaml" ]]; then
    cp "$tmp/kitsune.example.yaml" "$CONFIG"
    echo "Wrote config template to $CONFIG"
  fi
fi

case ":$PATH:" in
  *":$BIN_DIR:"*) ;;
  *) echo "note: $BIN_DIR is not on your PATH; add it to invoke kitsune-client directly" ;;
esac

if "$BINARY" install-autostart; then
  echo "Registered the kitsune-client systemd user unit"
else
  echo "warning: installed the client but could not register autostart;" >&2
  echo "         re-run '$BINARY install-autostart' inside your desktop session" >&2
fi

echo "Installed $BINARY"
