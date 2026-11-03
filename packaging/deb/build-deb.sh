#!/usr/bin/env bash
# Build a self-contained kitsune-whisper Debian package.
#
#   packaging/deb/build-deb.sh --client dist/kitsune-client \
#     --server-artifact dist/kitsune-server_linux_amd64.tar.gz \
#     --wayland-artifact dist/kitsune-wayland_linux_amd64.tar.gz \
#     --version v1.0.0 --output dist
#   packaging/deb/build-deb.sh --legacy ...   # GTK3/WebKit2GTK 4.1 variant
#
# The package installs the Client, the native Server bundle, the bundled
# ydotool/wtype helpers, the /dev/uinput uaccess rule, three systemd user units,
# and the application-menu entry with its fox icon. `kitsune-whisper-setup`
# seeds a per-user config and starts the units now; they also start at the next
# login. Uninstall with apt; `kitsune-whisper-installer --uninstall --purge`
# removes the per-user data and the udev rule.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
CLIENT="$REPO_ROOT/dist/kitsune-client"
VERSION="${KITSUNE_VERSION:-}"
OUTPUT_DIR="$REPO_ROOT/dist"
LEGACY=0
INSTALLER="$REPO_ROOT/install.sh"
EXAMPLE="$REPO_ROOT/kitsune.example.yaml"
SERVER_ARTIFACT=""
WAYLAND_ARTIFACT=""

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
    -h|--help)
      sed -n '2,14p' "$0" | sed 's/^# \{0,1\}//'
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
[[ -f "$REPO_ROOT/client/internal/brand/icons/app.svg" ]] \
  || { echo "error: application icon not found" >&2; exit 1; }
if [[ -n "$SERVER_ARTIFACT" ]]; then
  [[ -f "$SERVER_ARTIFACT" ]] || { echo "error: server artifact not found: $SERVER_ARTIFACT" >&2; exit 1; }
fi
if [[ -n "$WAYLAND_ARTIFACT" ]]; then
  [[ -f "$WAYLAND_ARTIFACT" ]] || { echo "error: wayland artifact not found: $WAYLAND_ARTIFACT" >&2; exit 1; }
fi

if [[ -z "$VERSION" ]]; then
  VERSION="$(cat "$REPO_ROOT/VERSION" 2>/dev/null || git -C "$REPO_ROOT" describe --tags --always 2>/dev/null || echo 0.1.0)"
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
  DEPENDS="libgtk-3-0, libwebkit2gtk-4.1-0"
  CONFLICTS="kitsune-whisper"
  SUMMARY="GTK3 legacy build of the kitsune-whisper dictation suite"
else
  PACKAGE="kitsune-whisper"
  DEPENDS="libgtk-4-1, libwebkitgtk-6.0-4"
  CONFLICTS="kitsune-whisper-legacy"
  SUMMARY="Private, LAN-local speech-to-text dictation client and Server"
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/kitsune-deb.XXXXXX")"
trap 'rm -rf "$work"' EXIT
stage="$work/stage"
APP_ID="io.github.FoxRed-cmd.kitsune-whisper"
ICON_SVG="$REPO_ROOT/client/internal/brand/icons/app.svg"

mkdir -p "$stage/DEBIAN" "$stage/usr/bin" "$stage/usr/lib/udev/rules.d" \
  "$stage/usr/lib/systemd/user" "$stage/usr/lib/systemd/user-preset" \
  "$stage/usr/lib/modules-load.d" \
  "$stage/usr/share/applications" "$stage/usr/share/doc/$PACKAGE" \
  "$stage/usr/share/icons/hicolor/scalable/apps" \
  "$stage/usr/share/kitsune-whisper"

install -m 0755 "$CLIENT" "$stage/usr/bin/kitsune-client"
install -m 0755 "$INSTALLER" "$stage/usr/bin/kitsune-whisper-installer"
install -m 0644 "$EXAMPLE" "$stage/usr/share/kitsune-whisper/kitsune.example.yaml"
install -m 0644 "$ICON_SVG" "$stage/usr/share/icons/hicolor/scalable/apps/$APP_ID.svg"

if [[ -n "$SERVER_ARTIFACT" ]]; then
  mkdir -p "$stage/usr/lib/kitsune-whisper/server"
  tar -xzf "$SERVER_ARTIFACT" -C "$stage/usr/lib/kitsune-whisper/server"
  cat > "$stage/usr/bin/kitsune-server" <<'EOF'
#!/bin/sh
# Run the packaged Server bundle with its own pinned runtime. The bundle lives
# under /usr/lib; config, state, and models stay per user. apt owns this bundle,
# so the user-level install/uninstall commands are not available here.
export KITSUNE_SERVER_ROOT=/usr/lib/kitsune-whisper/server
case "${1:-}" in
  install|uninstall)
    echo "this Server bundle is owned by the kitsune-whisper package; use apt to upgrade or remove it" >&2
    exit 2
    ;;
esac
exec /usr/lib/kitsune-whisper/server/runtime/bin/python3 -s /usr/lib/kitsune-whisper/server/bootstrap.py "$@"
EOF
  chmod 0755 "$stage/usr/bin/kitsune-server"
  cat > "$stage/usr/lib/systemd/user/kitsune-server.service" <<'EOF'
[Unit]
Description=kitsune-whisper transcription Server
Documentation=https://github.com/FoxRed-cmd/kitsune-whisper
After=network.target

[Service]
Type=simple
ExecStart=/usr/lib/kitsune-whisper/server/runtime/bin/python3 -s /usr/lib/kitsune-whisper/server/bootstrap.py start
Restart=on-failure
RestartSec=2
# The Server serves the network, not the desktop: it deliberately has no
# graphical-session dependency and runs in the user manager from login.

[Install]
WantedBy=default.target
EOF
fi

if [[ -n "$WAYLAND_ARTIFACT" ]]; then
  mkdir -p "$stage/usr/lib/kitsune-whisper/wayland"
  tar -xzf "$WAYLAND_ARTIFACT" -C "$stage/usr/lib/kitsune-whisper/wayland"
  cat > "$stage/usr/lib/systemd/user/kitsune-ydotoold.service" <<'EOF'
[Unit]
Description=kitsune-whisper ydotool daemon
Documentation=https://github.com/FoxRed-cmd/kitsune-whisper
PartOf=graphical-session.target
After=graphical-session.target

[Service]
Type=simple
ExecStart=/usr/lib/kitsune-whisper/wayland/bin/ydotoold
Restart=on-failure
RestartSec=2

[Install]
WantedBy=graphical-session.target
EOF
fi

cat > "$stage/usr/bin/kitsune-whisper-setup" <<'EOF'
#!/bin/sh
# Seed the per-user config and start the user services now. The package already
# installed the Client, Server, helpers, and the system udev rule; everything
# here runs as the user.
set -eu
config_dir="${XDG_CONFIG_HOME:-$HOME/.config}/kitsune-whisper"
config="$config_dir/kitsune.yaml"
if [ ! -f "$config" ]; then
  install -d -m 0700 "$config_dir"
  install -m 0600 /usr/share/kitsune-whisper/kitsune.example.yaml "$config"
  echo "Wrote config template to $config"
fi
systemctl --user daemon-reload >/dev/null 2>&1 || true
systemctl --user start kitsune-server.service >/dev/null 2>&1 || true
systemctl --user start kitsune-ydotoold.service >/dev/null 2>&1 || true
systemctl --user start kitsune-client.service >/dev/null 2>&1 || true
echo "kitsune-whisper user services started (they also start at next login)"
EOF
chmod 0755 "$stage/usr/bin/kitsune-whisper-setup"

cat > "$stage/usr/lib/udev/rules.d/70-kitsune-whisper-uinput.rules" <<'EOF'
# kitsune-whisper: give the active desktop session access to /dev/uinput so
# the bundled ydotoold can inject text without a group change or a re-login.
KERNEL=="uinput", SUBSYSTEM=="misc", TAG+="uaccess", OPTIONS+="static_node=uinput"
EOF

cat > "$stage/usr/lib/modules-load.d/kitsune-whisper.conf" <<'EOF'
uinput
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
Environment="PATH=/usr/lib/kitsune-whisper/wayland/bin:/usr/local/bin:/usr/bin:/bin"
Restart=on-failure
RestartSec=2

[Install]
WantedBy=graphical-session.target
EOF

cat > "$stage/usr/lib/systemd/user-preset/80-kitsune-whisper.preset" <<'EOF'
enable kitsune-client.service
enable kitsune-server.service
enable kitsune-ydotoold.service
EOF

cat > "$stage/usr/share/applications/$APP_ID.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=kitsune-whisper
Comment=Private, LAN-local speech-to-text dictation
Exec=/usr/bin/kitsune-client gui
Icon=$APP_ID
Terminal=false
NoDisplay=true
EOF

cat > "$stage/usr/share/applications/kitsune-whisper.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=kitsune-whisper
GenericName=Dictation
Comment=Private, LAN-local speech-to-text dictation
Exec=/usr/bin/kitsune-client gui
Icon=$APP_ID
Terminal=false
Categories=AudioVideo;Audio;
Keywords=dictation;speech-to-text;transcription;voice;
StartupNotify=true
StartupWMClass=$APP_ID
EOF

cat > "$stage/usr/share/doc/$PACKAGE/README.Debian" <<EOF
kitsune-whisper for Debian/Ubuntu
=================================

This package installs the Client, the native Server bundle, the bundled
ydotool/wtype helpers, the /dev/uinput uaccess rule, three systemd user units,
and the kitsune-whisper application-menu entry with its fox icon. The package
manager supplied GTK and WebKitGTK.

The units are enabled for every user at login, and the post-install step starts
them once for the account that ran apt when it has a live session. To seed a
config template and (re)start them as yourself, no sudo:

    kitsune-whisper-setup

The packaged Server bundle also provides the usual commands:

    kitsune-server status
    kitsune-server pair
    kitsune-server devices list

Upgrade with apt. Remove the system package with apt; per-user config, models,
certificates, Devices, spool, and logs are kept. Purge them with:

    kitsune-whisper-installer --uninstall --purge

That also removes the udev rule (it asks for sudo). Uninstall the package first.
EOF

cat > "$stage/DEBIAN/control" <<EOF
Package: $PACKAGE
Version: $VERSION
Section: sound
Priority: optional
Architecture: $DEB_ARCH
Depends: $DEPENDS
Conflicts: $CONFLICTS
Replaces: $CONFLICTS
Recommends: systemd, udev
Maintainer: FoxRed-cmd <noreply@github.com>
Description: $SUMMARY
 The Client captures speech, sends it to a Server on the same machine or the
 LAN, and injects the transcription into the focused field. This package also
 installs the native Server bundle and the bundled Wayland helpers.
EOF

cat > "$stage/DEBIAN/postinst" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = configure ]; then
  systemctl --global enable kitsune-client.service kitsune-server.service kitsune-ydotoold.service >/dev/null 2>&1 || true
  modprobe uinput >/dev/null 2>&1 || true
  udevadm control --reload-rules >/dev/null 2>&1 || true
  udevadm trigger --name-match=uinput >/dev/null 2>&1 || true
  if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database -q /usr/share/applications >/dev/null 2>&1 || true
  fi
  if command -v gtk-update-icon-cache >/dev/null 2>&1; then
    gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor >/dev/null 2>&1 || true
  fi
  # Start the user units now for the account that invoked apt, pkexec, or the
  # active seat. The units are also enabled globally for every user at login.
  start_user="${SUDO_USER:-}"
  if [ -z "$start_user" ] && [ -n "${PKEXEC_UID:-}" ]; then
    start_user=$(getent passwd "$PKEXEC_UID" 2>/dev/null | cut -d: -f1)
  fi
  if [ -z "$start_user" ]; then
    start_user=$(loginctl list-sessions --no-legend 2>/dev/null | awk '$4 ~ /^seat/ {print $3; exit}')
  fi
  if [ -n "$start_user" ] && [ "$start_user" != root ]; then
    uid=$(id -u "$start_user" 2>/dev/null || true)
    if [ -n "$uid" ] && [ -d "/run/user/$uid" ]; then
      runuser -u "$start_user" -- env XDG_RUNTIME_DIR="/run/user/$uid" \
        systemctl --user daemon-reload >/dev/null 2>&1 || true
      runuser -u "$start_user" -- env XDG_RUNTIME_DIR="/run/user/$uid" \
        systemctl --user start kitsune-server.service kitsune-ydotoold.service kitsune-client.service \
        >/dev/null 2>&1 || true
    fi
  fi
fi
exit 0
EOF
chmod 0755 "$stage/DEBIAN/postinst"

cat > "$stage/DEBIAN/prerm" <<'EOF'
#!/bin/sh
set -e
if [ "$1" = remove ]; then
  systemctl --global disable kitsune-client.service kitsune-server.service kitsune-ydotoold.service >/dev/null 2>&1 || true
fi
exit 0
EOF
chmod 0755 "$stage/DEBIAN/prerm"

mkdir -p "$OUTPUT_DIR"
# The ar fallback below runs inside a temporary directory, so the output path
# must be absolute for hosts without dpkg-deb (for example a Fedora build box).
OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd)"
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
