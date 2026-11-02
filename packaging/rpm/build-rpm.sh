#!/usr/bin/env bash
# Build a self-contained kitsune-whisper RPM for Fedora (and other rpm distros).
#
#   packaging/rpm/build-rpm.sh --client dist/kitsune-client \
#     --server-artifact dist/kitsune-server_linux_amd64.tar.gz \
#     --wayland-artifact dist/kitsune-wayland_linux_amd64.tar.gz \
#     --version v0.4.1 --output dist
#   packaging/rpm/build-rpm.sh --legacy ...   # GTK3/WebKit2GTK 4.1 variant
#
# The package mirrors the .deb payload: the Client, the native Server bundle,
# the bundled ydotool/wtype helpers, the /dev/uinput uaccess rule, three systemd
# user units, and the application-menu entry with its fox icon.
# `kitsune-whisper-setup` seeds a per-user config and starts the units now; they
# also start at the next login. Remove it with dnf;
# `kitsune-whisper-installer --uninstall --purge` removes the per-user data.
#
# Requires rpmbuild (Fedora: dnf install rpm-build; Debian/Ubuntu: apt install rpm).
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
      sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'
      exit 0
      ;;
    *)
      echo "error: unknown argument: $1" >&2
      exit 2
      ;;
  esac
  shift
done

command -v rpmbuild >/dev/null 2>&1 \
  || { echo "error: rpmbuild is required (Fedora: dnf install rpm-build; Debian/Ubuntu: apt install rpm)" >&2; exit 1; }

[[ -f "$CLIENT" ]] || { echo "error: client binary not found: $CLIENT" >&2; exit 1; }
[[ -f "$INSTALLER" ]] || { echo "error: installer not found: $INSTALLER" >&2; exit 1; }
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

# RPM forbids '-' in Version and Release. A git describe such as
# 0.4.1-3-gabc123 becomes Version 0.4.1, Release 3.gabc123.
if [[ "$VERSION" == *-* ]]; then
  RPM_VERSION="${VERSION%%-*}"
  RPM_RELEASE="${VERSION#*-}"
else
  RPM_VERSION="$VERSION"
  RPM_RELEASE=1
fi
RPM_VERSION="$(printf '%s' "$RPM_VERSION" | sed 's/[^A-Za-z0-9._+~^]/./g; s/^[^A-Za-z0-9]*//')"
RPM_RELEASE="$(printf '%s' "$RPM_RELEASE" | sed 's/[^A-Za-z0-9._+~^]/./g')"
[[ -n "$RPM_VERSION" ]] || RPM_VERSION="0.1.0"
[[ -n "$RPM_RELEASE" ]] || RPM_RELEASE=1

case "$(uname -m)" in
  x86_64|amd64) RPM_ARCH=x86_64 ;;
  aarch64|arm64) RPM_ARCH=aarch64 ;;
  *) echo "error: unsupported architecture $(uname -m)" >&2; exit 1 ;;
esac

if [[ "$LEGACY" == 1 ]]; then
  PACKAGE="kitsune-whisper-legacy"
  REQUIRES=$'Requires:       gtk3\nRequires:       webkit2gtk4.1'
  CONFLICTS="kitsune-whisper"
  SUMMARY="GTK3 legacy build of the kitsune-whisper dictation suite"
else
  PACKAGE="kitsune-whisper"
  REQUIRES=$'Requires:       gtk4\nRequires:       webkitgtk6.0'
  CONFLICTS="kitsune-whisper-legacy"
  SUMMARY="Private, LAN-local speech-to-text dictation client and Server"
fi

# Only enable units the package actually ships.
UNITS="kitsune-client.service"
if [[ -n "$SERVER_ARTIFACT" ]]; then
  UNITS+=" kitsune-server.service"
fi
if [[ -n "$WAYLAND_ARTIFACT" ]]; then
  UNITS+=" kitsune-ydotoold.service"
fi

work="$(mktemp -d "${TMPDIR:-/tmp}/kitsune-rpm.XXXXXX")"
trap 'rm -rf "$work"' EXIT
topdir="$work/rpmbuild"
stage="$work/stage"
APP_ID="io.github.FoxRed-cmd.kitsune-whisper"
ICON_SVG="$REPO_ROOT/client/internal/brand/icons/app.svg"

mkdir -p "$topdir"/{BUILD,BUILDROOT,RPMS,SOURCES,SPECS,SRPMS}
mkdir -p "$stage/usr/bin" "$stage/usr/lib/udev/rules.d" \
  "$stage/usr/lib/systemd/user" "$stage/usr/lib/systemd/user-preset" \
  "$stage/usr/lib/modules-load.d" \
  "$stage/usr/share/applications" "$stage/usr/share/doc/$PACKAGE" \
  "$stage/usr/share/icons/hicolor/scalable/apps" \
  "$stage/usr/share/licenses/$PACKAGE" \
  "$stage/usr/share/kitsune-whisper"

install -m 0755 "$CLIENT" "$stage/usr/bin/kitsune-client"
install -m 0755 "$INSTALLER" "$stage/usr/bin/kitsune-whisper-installer"
install -m 0644 "$EXAMPLE" "$stage/usr/share/kitsune-whisper/kitsune.example.yaml"
install -m 0644 "$ICON_SVG" "$stage/usr/share/icons/hicolor/scalable/apps/$APP_ID.svg"
install -m 0644 "$REPO_ROOT/LICENSE" "$stage/usr/share/licenses/$PACKAGE/LICENSE"

# These files are always part of the package.
file_list=$(
  cat <<EOF
%attr(0755,root,root) /usr/bin/kitsune-client
%attr(0755,root,root) /usr/bin/kitsune-whisper-installer
%attr(0755,root,root) /usr/bin/kitsune-whisper-setup
%dir /usr/share/kitsune-whisper
%attr(0644,root,root) /usr/share/kitsune-whisper/kitsune.example.yaml
%attr(0644,root,root) /usr/share/applications/$APP_ID.desktop
%attr(0644,root,root) /usr/share/applications/kitsune-whisper.desktop
%attr(0644,root,root) /usr/share/icons/hicolor/scalable/apps/$APP_ID.svg
%attr(0644,root,root) /usr/lib/udev/rules.d/70-kitsune-whisper-uinput.rules
%attr(0644,root,root) /usr/lib/modules-load.d/kitsune-whisper.conf
%attr(0644,root,root) /usr/lib/systemd/user/kitsune-client.service
%attr(0644,root,root) /usr/lib/systemd/user-preset/80-kitsune-whisper.preset
%dir /usr/share/doc/$PACKAGE
%dir /usr/share/licenses/$PACKAGE
%license /usr/share/licenses/$PACKAGE/LICENSE
/usr/share/doc/$PACKAGE/README.fedora
EOF
)

if [[ -n "$SERVER_ARTIFACT" || -n "$WAYLAND_ARTIFACT" ]]; then
  file_list+=$'\n%dir /usr/lib/kitsune-whisper'
fi

if [[ -n "$SERVER_ARTIFACT" ]]; then
  mkdir -p "$stage/usr/lib/kitsune-whisper/server"
  tar -xzf "$SERVER_ARTIFACT" -C "$stage/usr/lib/kitsune-whisper/server"
  cat > "$stage/usr/bin/kitsune-server" <<'EOF'
#!/bin/sh
# Run the packaged Server bundle with its own pinned runtime. The bundle lives
# under /usr/lib; config, state, and models stay per user. dnf owns this bundle,
# so the user-level install/uninstall commands are not available here.
export KITSUNE_SERVER_ROOT=/usr/lib/kitsune-whisper/server
case "${1:-}" in
  install|uninstall)
    echo "this Server bundle is owned by the kitsune-whisper package; use dnf to upgrade or remove it" >&2
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
  file_list+=$'\n/usr/lib/kitsune-whisper/server'
  file_list+=$'\n%attr(0755,root,root) /usr/bin/kitsune-server'
  file_list+=$'\n%attr(0644,root,root) /usr/lib/systemd/user/kitsune-server.service'
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
  file_list+=$'\n/usr/lib/kitsune-whisper/wayland'
  file_list+=$'\n%attr(0644,root,root) /usr/lib/systemd/user/kitsune-ydotoold.service'
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

for unit in $UNITS; do
  printf 'enable %s\n' "$unit"
done > "$stage/usr/lib/systemd/user-preset/80-kitsune-whisper.preset"

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

cat > "$stage/usr/share/doc/$PACKAGE/README.fedora" <<'EOF'
kitsune-whisper for Fedora
==========================

This package installs the Client, the native Server bundle, the bundled
ydotool/wtype helpers, the /dev/uinput uaccess rule, three systemd user units,
and the kitsune-whisper application-menu entry with its fox icon. dnf supplied
GTK and WebKitGTK.

The units are enabled for every user at login, and the post-install step starts
them once for the account that ran dnf when it has a live session. To seed a
config template and (re)start them as yourself, no sudo:

    kitsune-whisper-setup

The packaged Server bundle also provides the usual commands:

    kitsune-server status
    kitsune-server pair
    kitsune-server devices list

Upgrade with dnf. Remove the system package with dnf; per-user config, models,
certificates, Devices, spool, and logs are kept. Purge them with:

    kitsune-whisper-installer --uninstall --purge

That also removes the udev rule (it asks for sudo). Uninstall the package first.
EOF

cp -a "$stage" "$topdir/SOURCES/stage"

cat > "$topdir/SPECS/$PACKAGE.spec" <<EOF
%global debug_package %{nil}
%global __os_install_post %{nil}

Name:           $PACKAGE
Version:        $RPM_VERSION
Release:        $RPM_RELEASE
Summary:        $SUMMARY
License:        MIT
URL:            https://github.com/FoxRed-cmd/kitsune-whisper
BuildArch:      $RPM_ARCH
# The Server bundle ships a private Python runtime and native wheels under
# /usr/lib/kitsune-whisper, not in the standard libdirs. Automatic dependency
# generation would demand sonames that no distribution package provides.
AutoReqProv:    no
Requires:       /bin/sh
$REQUIRES
Conflicts:      $CONFLICTS

%description
The Client captures speech, sends it to a Server on the same machine or the
LAN, and injects the transcription into the focused field. This package also
installs the native Server bundle and the bundled Wayland helpers.

%install
cp -a %{_sourcedir}/stage/. %{buildroot}/

%files
$file_list

%post
set -e
systemctl --global enable $UNITS >/dev/null 2>&1 || true
modprobe uinput >/dev/null 2>&1 || true
udevadm control --reload-rules >/dev/null 2>&1 || true
udevadm trigger --name-match=uinput >/dev/null 2>&1 || true
if command -v update-desktop-database >/dev/null 2>&1; then
  update-desktop-database -q /usr/share/applications >/dev/null 2>&1 || true
fi
if command -v gtk-update-icon-cache >/dev/null 2>&1; then
  gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor >/dev/null 2>&1 || true
fi
# Start the user units now for the account that invoked dnf or pkexec. The
# units are also enabled globally for every user at login.
start_user="\${SUDO_USER:-}"
if [ -z "\$start_user" ] && [ -n "\${PKEXEC_UID:-}" ]; then
  start_user=\$(getent passwd "\$PKEXEC_UID" 2>/dev/null | cut -d: -f1)
fi
if [ -z "\$start_user" ]; then
  start_user=\$(loginctl list-sessions --no-legend 2>/dev/null | awk '\$4 ~ /^seat/ {print \$3; exit}')
fi
if [ -n "\$start_user" ] && [ "\$start_user" != root ]; then
  uid=\$(id -u "\$start_user" 2>/dev/null || true)
  if [ -n "\$uid" ] && [ -d "/run/user/\$uid" ]; then
    runuser -u "\$start_user" -- env XDG_RUNTIME_DIR="/run/user/\$uid" \\
      systemctl --user daemon-reload >/dev/null 2>&1 || true
    runuser -u "\$start_user" -- env XDG_RUNTIME_DIR="/run/user/\$uid" \\
      systemctl --user start $UNITS \\
      >/dev/null 2>&1 || true
  fi
fi
exit 0

%preun
set -e
if [ "\$1" -eq 0 ]; then
  systemctl --global disable $UNITS >/dev/null 2>&1 || true
fi
exit 0

%postun
set -e
if [ "\$1" -eq 0 ]; then
  udevadm control --reload-rules >/dev/null 2>&1 || true
  udevadm trigger --name-match=uinput >/dev/null 2>&1 || true
  if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database -q /usr/share/applications >/dev/null 2>&1 || true
  fi
  if command -v gtk-update-icon-cache >/dev/null 2>&1; then
    gtk-update-icon-cache -q -t -f /usr/share/icons/hicolor >/dev/null 2>&1 || true
  fi
fi
exit 0
EOF

rpmbuild -bb \
  --define "_topdir $topdir" \
  --define "_binary_payload w9.gzdio" \
  "$topdir/SPECS/$PACKAGE.spec" >/dev/null

built="$topdir/RPMS/$RPM_ARCH/$PACKAGE-$RPM_VERSION-$RPM_RELEASE.$RPM_ARCH.rpm"
[[ -f "$built" ]] || { echo "error: rpmbuild did not produce $built" >&2; exit 1; }

mkdir -p "$OUTPUT_DIR"
rpm="$OUTPUT_DIR/$PACKAGE-$RPM_VERSION-$RPM_RELEASE.$RPM_ARCH.rpm"
cp "$built" "$rpm"
echo "Wrote $rpm"
