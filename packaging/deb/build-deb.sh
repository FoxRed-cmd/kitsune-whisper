#!/usr/bin/env bash
# Build the kitsune-whisper Debian package from a release Client binary.
#
#   packaging/deb/build-deb.sh --client dist/kitsune-client \
#     --version 0.4.1 --output dist
#   packaging/deb/build-deb.sh --legacy ...
#
# The package installs the Client, the udev uaccess rule for /dev/uinput, a
# systemd user unit, and the preset that enables it at login. The native Server
# bundle and the bundled Wayland helpers are per-user payloads: after
# `apt install`, the user runs `kitsune-whisper-setup` (no sudo) to place them
# in their home and register their user units.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CLIENT="$REPO_ROOT/dist/kitsune-client"
VERSION="${KITSUNE_VERSION:-}"
OUTPUT_DIR="$REPO_ROOT/dist"
LEGACY=0
INSTALLER="$REPO_ROOT/install.sh"
EXAMPLE="$REPO_ROOT/kitsune.example.yaml"

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
    --installer)
      shift
      [[ $# -gt 0 ]] || { echo "error: --installer needs a path" >&2; exit 2; }
      INSTALLER="$1"
      ;;
    --example)
      shift
      [[ $# -gt 0 ]] || { echo "error: --example needs a path" >&2; exit 2; }
      EXAMPLE="$1"
      ;;
    -h|--help)
      sed -n '2,12p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      exit 2
      ;;
  esac
  shift
done

[[ -f "$CLIENT" ]] || { echo "error: client binary not found: $CLIENT" >&2; exit 1; }
[[ -f "$INSTALLER" ]] || { echo "error: installer not found: $INSTALLER" >&2; exit 1; }
[[ -f "$EXAMPLE" ]] || { echo "error: example config not found: $EXAMPLE" >&2; exit 1; }

if [[ -z "$VERSION" ]]; then
  VERSION="$(git -C "$REPO_ROOT" describe --tags --always 2>/dev/null || echo 0.1.0)"
fi
TAG_VERSION="$VERSION"
if [[ "$TAG_VERSION" != v* ]]; then
  TAG_VERSION="v${TAG_VERSION#v}"
fi
VERSION="${VERSION#v}"
[[ -n "$VERSION" ]] || VERSION="0.1.0"

case "$(uname -m)" in
  x86_64|amd64) DEB_ARCH=amd64 ;;
  aarch64|arm64) DEB_ARCH=arm64 ;;
  *) echo "error: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

if [[ "$LEGACY" == 1 ]]; then
  PACKAGE="kitsune-whisper-legacy"
  DEPENDS="libgtk-3-0, libwebkit2gtk-4.1-0, curl"
  SUMMARY="GTK3 legacy build of the kitsune-whisper dictation client"
else
  PACKAGE="kitsune-whisper"
  DEPENDS="libgtk-4-1, libwebkitgtk-6.0-4, curl"
  SUMMARY="Private, LAN-local speech-to-text dictation client"
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/kitsune-deb.XXXXXX")"
trap 'rm -rf "$work"' EXIT
stage="$work/stage"
mkdir -p "$stage/DEBIAN" "$stage/usr/bin" "$stage/usr/lib/udev/rules.d" \
  "$stage/usr/lib/systemd/user" "$stage/usr/lib/systemd/user-preset" \
  "$stage/usr/share/applications" "$stage/usr/share/doc/$PACKAGE" \
  "$stage/usr/share/kitsune-whisper"

install -m 0755 "$CLIENT" "$stage/usr/bin/kitsune-client"
install -m 0755 "$INSTALLER" "$stage/usr/bin/kitsune-whisper-installer"
install -m 0644 "$EXAMPLE" "$stage/usr/share/kitsune-whisper/kitsune.example.yaml"

cat > "$stage/usr/bin/kitsune-whisper-setup" <<EOF
#!/bin/sh
# Finish the per-user install after the Debian package supplied the Client and
# the system dependencies. Runs as the user: the Server bundle and the Wayland
# helpers live under \$HOME and run in the user's systemd manager.
set -eu
export KITSUNE_CONFIG_TEMPLATE=/usr/share/kitsune-whisper/kitsune.example.yaml
export KITSUNE_VERSION=$TAG_VERSION
exec /usr/bin/kitsune-whisper-installer --no-client --no-elevate "\$@"
EOF
chmod 0755 "$stage/usr/bin/kitsune-whisper-setup"

cat > "$stage/usr/lib/udev/rules.d/70-kitsune-whisper-uinput.rules" <<'EOF'
# kitsune-whisper: give the active desktop session access to /dev/uinput so
# the bundled ydotoold can inject text without a group change or a re-login.
KERNEL=="uinput", SUBSYSTEM=="misc", TAG+="uaccess", OPTIONS+="static_node=uinput"
EOF

cat > "$stage/usr/lib/systemd/user/kitsune-client.service" <<'EOF'
[Unit]
Description=kitsune-whisper dictation client
Documentation=https://github.com/FoxRed-cmd/kitsune-whisper
After=graphical-session.target
PartOf=graphical-session.target

[Service]
Type=simple
ExecStart=/usr/bin/kitsune-client gui --hidden
Restart=on-failure
RestartSec=2

[Install]
WantedBy=graphical-session.target
EOF

cat > "$stage/usr/lib/systemd/user-preset/80-kitsune-whisper.preset" <<'EOF'
enable kitsune-client.service
EOF

cat > "$stage/usr/share/applications/io.github.FoxRed-cmd.kitsune-whisper.desktop" <<'EOF'
[Desktop Entry]
Type=Application
Name=kitsune-whisper
Comment=Private, LAN-local speech-to-text dictation
Exec=/usr/bin/kitsune-client gui
Terminal=false
NoDisplay=true
EOF

cat > "$stage/usr/share/doc/$PACKAGE/README.Debian" <<EOF
kitsune-whisper for Debian/Ubuntu
=================================

This package installs the Client, the /dev/uinput uaccess rule, and the
systemd user unit. The package manager already supplied GTK and WebKitGTK.

Finish the per-user install as yourself (no sudo):

    kitsune-whisper-setup

That downloads the native Server bundle and the bundled Wayland helpers,
verifies their checksums, writes the Server and ydotoold user units, and starts
them. Re-run it to upgrade.

The service starts at your next login. Start it now with:

    systemctl --user daemon-reload
    systemctl --user enable --now kitsune-client.service

Uninstall the system package with apt; purge per-user data with:

    kitsune-whisper-installer --uninstall --purge

or remove the package and then that directory by hand.
EOF

cat > "$stage/DEBIAN/control" <<EOF
Package: $PACKAGE
Version: $VERSION
Section: sound
Priority: optional
Architecture: $DEB_ARCH
Depends: $DEPENDS
Recommends: systemd, udev
Maintainer: FoxRed-cmd <noreply@github.com>
Description: $SUMMARY
 The Client captures speech, sends it to a Server on the same machine or the
 LAN, and injects the transcription into the focused field. The embedded
 Settings window and Tray use GTK and WebKitGTK.
EOF

cat > "$stage/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = configure ]; then
  systemctl --global enable kitsune-client.service >/dev/null 2>&1 || true
  udevadm control --reload-rules >/dev/null 2>&1 || true
  udevadm trigger --name-match=uinput >/dev/null 2>&1 || true
fi
exit 0
EOF
chmod 0755 "$stage/DEBIAN/postinst"

cat > "$stage/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = remove ]; then
  systemctl --global disable kitsune-client.service >/dev/null 2>&1 || true
fi
exit 0
EOF
chmod 0755 "$stage/DEBIAN/prerm"

mkdir -p "$OUTPUT_DIR"
deb="$OUTPUT_DIR/${PACKAGE}_${VERSION}_${DEB_ARCH}.deb"

if command -v dpkg-deb >/dev/null 2>&1; then
  dpkg-deb --root-owner-group --build "$stage" "$deb" >/dev/null
else
  # dpkg-deb is not on every build host; the .deb format is an ar archive of
  # three members, so ar+tar is enough for a valid package.
  (cd "$stage" && tar --owner=0 --group=0 --numeric-owner -czf "$work/data.tar.gz" \
    --exclude=DEBIAN .)
  (cd "$stage/DEBIAN" && tar --owner=0 --group=0 --numeric-owner -czf "$work/control.tar.gz" .)
  printf '2.0\n' > "$work/debian-binary"
  (cd "$work" && ar rcs "$deb" debian-binary control.tar.gz data.tar.gz)
fi

echo "Wrote $deb"
