#!/usr/bin/env bash
# Build a portable kitsune-whisper AppImage for Linux.
#
#   packaging/appimage/build-appimage.sh --client dist/kitsune-client \
#     --server-artifact dist/kitsune-server_linux_amd64.tar.gz \
#     --wayland-artifact dist/kitsune-wayland_linux_amd64.tar.gz \
#     --version v0.4.1 --output dist
#   packaging/appimage/build-appimage.sh --legacy ...   # GTK3/WebKit2GTK 4.1
#
# The AppImage carries the Client, the native Server bundle, the bundled
# ydotool/wtype helpers, a config template, and an AppRun that starts the
# bundled Server and ydotoold for the duration of the session. It uses the
# host's GTK4/WebKitGTK 6.0 (or GTK3/WebKit2GTK 4.1 with --legacy); those are
# declared in docs/install.md. Building needs appimagetool:
#
#   APPIMAGETOOL=/path/to/appimagetool packaging/appimage/build-appimage.sh ...
#
# appimagetool may itself be an AppImage; APPIMAGE_EXTRACT_AND_RUN=1 is set so
# it also runs on hosts without FUSE.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CLIENT="$REPO_ROOT/dist/kitsune-client"
VERSION="${KITSUNE_VERSION:-}"
OUTPUT_DIR="$REPO_ROOT/dist"
LEGACY=0
EXAMPLE="$REPO_ROOT/kitsune.example.yaml"
SERVER_ARTIFACT=""
WAYLAND_ARTIFACT=""
APPIMAGETOOL="${APPIMAGETOOL:-}"

while [[ $# -gt 0 ]]; do
  case "$1" in
    --client)
      shift
      [[ $# -gt 0 ]] || { echo "error: --client needs a path" >&2; exit 2; }
      CLIENT="$1"
      ;;
    --version)
      shift
      [[ $# -gt 0 ]] || { echo "error: --version needs a value" >&2; exit 2; }
      VERSION="$1"
      ;;
    --output)
      shift
      [[ $# -gt 0 ]] || { echo "error: --output needs a directory" >&2; exit 2; }
      OUTPUT_DIR="$1"
      ;;
    --legacy) LEGACY=1 ;;
    --example)
      shift
      [[ $# -gt 0 ]] || { echo "error: --example needs a path" >&2; exit 2; }
      EXAMPLE="$1"
      ;;
    --server-artifact)
      shift
      [[ $# -gt 0 ]] || { echo "error: --server-artifact needs a path" >&2; exit 2; }
      SERVER_ARTIFACT="$1"
      ;;
    --wayland-artifact)
      shift
      [[ $# -gt 0 ]] || { echo "error: --wayland-artifact needs a path" >&2; exit 2; }
      WAYLAND_ARTIFACT="$1"
      ;;
    --appimagetool)
      shift
      [[ $# -gt 0 ]] || { echo "error: --appimagetool needs a path" >&2; exit 2; }
      APPIMAGETOOL="$1"
      ;;
    -h|--help)
      sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      exit 2
      ;;
  esac
  shift
done

if [[ -z "$APPIMAGETOOL" ]]; then
  APPIMAGETOOL="$(command -v appimagetool 2>/dev/null || true)"
fi
[[ -n "$APPIMAGETOOL" && -x "$APPIMAGETOOL" ]] \
  || { echo "error: appimagetool is required (set APPIMAGETOOL or --appimagetool)" >&2; exit 1; }

[[ -f "$CLIENT" ]] || { echo "error: client binary not found: $CLIENT" >&2; exit 1; }
[[ -f "$EXAMPLE" ]] || { echo "error: example config not found: $EXAMPLE" >&2; exit 1; }
[[ -f "$REPO_ROOT/LICENSE" ]] || { echo "error: LICENSE not found" >&2; exit 1; }
[[ -f "$REPO_ROOT/client/internal/brand/icons/app.svg" ]] \
  || { echo "error: application icon not found" >&2; exit 1; }
if [[ -n "$SERVER_ARTIFACT" ]]; then
  [[ -f "$SERVER_ARTIFACT" ]] || { echo "error: server artifact not found: $SERVER_ARTIFACT" >&2; exit 1; }
fi
if [[ -n "$WAYLAND_ARTIFACT" ]]; then
  [[ -f "$WAYLAND_ARTIFACT" ]] || { echo "error: wayland artifact not found: $WAYLAND_ARTIFACT" >&2; exit 1; }
fi

if [[ -z "$VERSION" ]]; then
  VERSION="$(git -C "$REPO_ROOT" describe --tags --always 2>/dev/null || echo 0.1.0)"
fi
VERSION="${VERSION#v}"
[[ -n "$VERSION" ]] || VERSION="0.1.0"

case "$(uname -m)" in
  x86_64|amd64) APPIMAGE_ARCH=x86_64 ;;
  aarch64|arm64) APPIMAGE_ARCH=aarch64 ;;
  *) echo "error: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

if [[ "$LEGACY" == 1 ]]; then
  PACKAGE="kitsune-whisper-legacy"
  SUMMARY="GTK3 legacy build of the kitsune-whisper dictation suite"
else
  PACKAGE="kitsune-whisper"
  SUMMARY="Private, LAN-local speech-to-text dictation client and Server"
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/kitsune-appimage.XXXXXX")"
trap 'rm -rf "$work"' EXIT
appdir="$work/$PACKAGE.AppDir"
APP_ID="io.github.FoxRed-cmd.kitsune-whisper"
ICON_SVG="$REPO_ROOT/client/internal/brand/icons/app.svg"

mkdir -p "$appdir/usr/bin" \
  "$appdir/usr/share/icons/hicolor/scalable/apps" \
  "$appdir/usr/share/licenses/$PACKAGE" \
  "$appdir/usr/share/kitsune-whisper"

install -m 0755 "$CLIENT" "$appdir/usr/bin/kitsune-client"
install -m 0644 "$EXAMPLE" "$appdir/usr/share/kitsune-whisper/kitsune.example.yaml"
install -m 0644 "$ICON_SVG" "$appdir/usr/share/icons/hicolor/scalable/apps/$APP_ID.svg"
install -m 0644 "$REPO_ROOT/LICENSE" "$appdir/usr/share/licenses/$PACKAGE/LICENSE"

# AppImage requires the icon at the AppDir root under the desktop entry's name.
install -m 0644 "$ICON_SVG" "$appdir/$PACKAGE.svg"
ln -s "$PACKAGE.svg" "$appdir/.DirIcon"

if [[ -n "$SERVER_ARTIFACT" ]]; then
  mkdir -p "$appdir/usr/lib/kitsune-whisper/server"
  tar -xzf "$SERVER_ARTIFACT" -C "$appdir/usr/lib/kitsune-whisper/server"
fi
if [[ -n "$WAYLAND_ARTIFACT" ]]; then
  mkdir -p "$appdir/usr/lib/kitsune-whisper/wayland"
  tar -xzf "$WAYLAND_ARTIFACT" -C "$appdir/usr/lib/kitsune-whisper/wayland"
fi

# Desktop integration reads the AppDir root entry; the version and AppRun
# target are filled in there. AppRun also prepends the AppDir data directory so
# the icon is available in the session.
cat > "$appdir/$PACKAGE.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=kitsune-whisper
GenericName=Dictation
Comment=Private, LAN-local speech-to-text dictation
Exec=AppRun
Icon=$PACKAGE
Terminal=false
Categories=AudioVideo;Audio;
Keywords=dictation;speech-to-text;transcription;voice;
StartupNotify=true
StartupWMClass=$APP_ID
X-AppImage-Version=$VERSION
EOF

cat > "$appdir/AppRun" <<'EOF'
#!/usr/bin/env bash
# kitsune-whisper AppImage entry point.
#
#   ./kitsune-whisper-*.AppImage             start the Tray/Settings GUI
#   ./kitsune-whisper-*.AppImage gui --hidden
#   ./kitsune-whisper-*.AppImage --server status
#   ./kitsune-whisper-*.AppImage --help
#
# Set KITSUNE_APPIMAGE_NO_SERVER=1 to use a Server that is already running
# (Docker, another host, or an installed systemd unit).
set -euo pipefail

appdir="${APPDIR:-$(cd "$(dirname "$(readlink -f "$0")")" && pwd)}"
server="$appdir/usr/lib/kitsune-whisper/server"
wayland="$appdir/usr/lib/kitsune-whisper/wayland"
client="$appdir/usr/bin/kitsune-client"
legacy=@@LEGACY@@

unit_active() {
  command -v systemctl >/dev/null 2>&1 \
    && systemctl --user is-active --quiet "$1" 2>/dev/null
}

toolkit_check() {
  command -v ldconfig >/dev/null 2>&1 || return 0
  local gtk webkit dnf_packages apt_packages
  if [ "$legacy" = 1 ]; then
    gtk=libgtk-3.so.0
    webkit=libwebkit2gtk-4.1.so.0
    dnf_packages="gtk3 webkit2gtk4.1"
    apt_packages="libgtk-3-0 libwebkit2gtk-4.1-0"
  else
    gtk=libgtk-4.so.1
    webkit=libwebkitgtk-6.0.so.4
    dnf_packages="gtk4 webkitgtk6.0"
    apt_packages="libgtk-4-1 libwebkitgtk-6.0-4"
  fi
  local libs
  libs="$(ldconfig -p 2>/dev/null || true)"
  if ! grep -qF "$gtk" <<<"$libs" || ! grep -qF "$webkit" <<<"$libs"; then
    echo "kitsune-whisper needs the desktop toolkit that this build uses:" >&2
    echo "  Fedora: sudo dnf install $dnf_packages" >&2
    echo "  Ubuntu: sudo apt install $apt_packages" >&2
    echo "The headless roles (run, record, transcribe-file) work without it." >&2
    exit 1
  fi
}

unit_active() {
  command -v systemctl >/dev/null 2>&1 \
    && systemctl --user is-active --quiet "$1" 2>/dev/null
}

usage() {
  sed -n '3,10p' "$0" | sed 's/^# \{0,1\}//'
}

case "${1:-}" in
  -h|--help)
    usage
    exit 0
    ;;
  --server)
    shift
    [ -x "$server/runtime/bin/python3" ] || {
      echo "error: this AppImage does not contain the native Server bundle" >&2
      exit 1
    }
    export KITSUNE_SERVER_ROOT="$server"
    exec "$server/runtime/bin/python3" -s "$server/bootstrap.py" "$@"
    ;;
esac

install -d -m 0700 "${XDG_CONFIG_HOME:-$HOME/.config}/kitsune-whisper"
config="${XDG_CONFIG_HOME:-$HOME/.config}/kitsune-whisper/kitsune.yaml"
if [ ! -f "$config" ] && [ -f "$appdir/usr/share/kitsune-whisper/kitsune.example.yaml" ]; then
  install -m 0600 "$appdir/usr/share/kitsune-whisper/kitsune.example.yaml" "$config"
  echo "Wrote config template to $config"
fi

export PATH="$wayland/bin:$PATH"
export XDG_DATA_DIRS="$appdir/usr/share:${XDG_DATA_DIRS:-/usr/local/share:/usr/share}"

case "${1:-gui}" in
  gui) toolkit_check ;;
esac

pids=()
cleanup() {
  local pid
  for pid in "${pids[@]}"; do
    kill "$pid" 2>/dev/null || true
  done
  wait 2>/dev/null || true
}
on_signal() {
  cleanup
  exit 130
}
trap cleanup EXIT
trap on_signal INT TERM

if [ -x "$wayland/bin/ydotoold" ] && [ -w /dev/uinput ] \
  && ! unit_active kitsune-ydotoold.service; then
  "$wayland/bin/ydotoold" &
  pids+=($!)
fi

if [ "${KITSUNE_APPIMAGE_NO_SERVER:-0}" != 1 ] \
  && [ -x "$server/runtime/bin/python3" ] \
  && ! unit_active kitsune-server.service; then
  KITSUNE_SERVER_ROOT="$server" \
    "$server/runtime/bin/python3" -s "$server/bootstrap.py" start &
  pids+=($!)
fi

if [ "$#" -eq 0 ]; then
  set -- gui
fi

# AppImage has no systemd unit to keep the Local Server alive, so the bundled
# Server runs for this session and stops when the Client exits.
"$client" "$@"
EOF
sed -i "s/@@LEGACY@@/$LEGACY/" "$appdir/AppRun"
chmod 0755 "$appdir/AppRun"

mkdir -p "$OUTPUT_DIR"
appimage="$OUTPUT_DIR/$PACKAGE-$VERSION-$APPIMAGE_ARCH.AppImage"

APPIMAGE_EXTRACT_AND_RUN=1 ARCH="$APPIMAGE_ARCH" \
  "$APPIMAGETOOL" --no-appstream "$appdir" "$appimage"

chmod 0755 "$appimage"
echo "Wrote $appimage"
