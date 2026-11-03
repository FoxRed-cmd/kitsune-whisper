#!/usr/bin/env bash
# kitsune-whisper Linux installer.
#
#   curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash
#
# Installs the Client (Tray plus Settings window), the native per-user Server
# bundle, and the bundled Wayland helpers (ydotool/ydotoold/wtype). It asks for
# sudo at most once during install: that one step installs GTK/WebKitGTK and the
# udev uaccess rule for /dev/uinput, then never runs privileged again. Purging
# later asks once more to remove that rule.
#
# The Client and Server are systemd *user* units. Re-run the installer to
# upgrade in place; kitsune.yaml, models, Devices, and certificates are kept.
#
#   install.sh [options]
#     --no-server             skip the native Server bundle
#     --no-client             skip the Client binary (server/helpers only)
#     --no-wayland            skip the bundled Wayland helpers
#     --no-elevate            never run sudo; install the degraded fallback
#     --gui gtk4|gtk3|auto    desktop build (default: auto)
#     --cuda                  download the NVIDIA CUDA runtime (~550 MB)
#     --from DIR              install from release artifacts in DIR
#     --force-config          overwrite an existing kitsune.yaml
#     --dry-run               print the plan and change nothing
#     --uninstall             remove binaries, units, and helpers; keep data
#     --uninstall --purge     also remove config, models, certificates, spool,
#                             logs, Devices, wizard progress, and the udev rule
#     --remove-models         with --uninstall: delete downloaded models
#     --remove-server-data    with --uninstall: delete certificates, Devices,
#                             the Server log, and its soft-stop marker
#     -h, --help
#
# Environment: KITSUNE_VERSION, KITSUNE_ARTIFACTS_DIR,
# KITSUNE_GUI, KITSUNE_PACKAGE_MANAGER (apt|dnf|pacman|zypper).
set -euo pipefail

REPO="FoxRed-cmd/kitsune-whisper"
VERSION="${KITSUNE_VERSION:-latest}"
BIN_DIR="$HOME/.local/bin"
LOCAL_LIB="$HOME/.local/lib/kitsune-whisper"
WAYLAND_DIR="$LOCAL_LIB/wayland"
WAYLAND_BIN="$WAYLAND_DIR/bin"
SERVER_ROOT="$LOCAL_LIB/server"
SERVER_LAUNCHER="$HOME/.local/bin/kitsune-server"
CONFIG_ROOT="${XDG_CONFIG_HOME:-$HOME/.config}"
DATA_ROOT="${XDG_DATA_HOME:-$HOME/.local/share}"
STATE_ROOT="${XDG_STATE_HOME:-$HOME/.local/state}"
CACHE_ROOT="${XDG_CACHE_HOME:-$HOME/.cache}"
CONFIG_DIR="$CONFIG_ROOT/kitsune-whisper"
CONFIG="$CONFIG_DIR/kitsune.yaml"
CACHE_DIR="$CACHE_ROOT/kitsune-whisper"
STATE_DIR="$STATE_ROOT/kitsune-whisper"
SYSTEMD_DIR="$CONFIG_ROOT/systemd/user"
DESKTOP_FILE="$DATA_ROOT/applications/io.github.FoxRed-cmd.kitsune-whisper.desktop"
MENU_FILE="$DATA_ROOT/applications/kitsune-whisper.desktop"
ICON_SVG="$DATA_ROOT/icons/hicolor/scalable/apps/io.github.FoxRed-cmd.kitsune-whisper.svg"
ICON_PNG="$DATA_ROOT/icons/hicolor/256x256/apps/io.github.FoxRed-cmd.kitsune-whisper.png"
UDEV_RULE="/etc/udev/rules.d/70-kitsune-whisper-uinput.rules"
UDEV_RULE_SYSTEM="/usr/lib/udev/rules.d/70-kitsune-whisper-uinput.rules"
MODULES_FILE="/etc/modules-load.d/kitsune-whisper.conf"
MODULES_FILE_SYSTEM="/usr/lib/modules-load.d/kitsune-whisper.conf"
WAYLAND_UNIT="$SYSTEMD_DIR/kitsune-ydotoold.service"
CLIENT_DROPIN="$SYSTEMD_DIR/kitsune-client.service.d/10-wayland.conf"

GUI_CHOICE="${KITSUNE_GUI:-auto}"
FROM_DIR="${KITSUNE_ARTIFACTS_DIR:-}"
PM_OVERRIDE="${KITSUNE_PACKAGE_MANAGER:-}"
NO_SERVER=0
NO_CLIENT=0
NO_WAYLAND=0
NO_ELEVATE=0
CUDA=0
DRY_RUN=0
FORCE_CONFIG=0
UNINSTALL=0
PURGE=0
REMOVE_MODELS=0
REMOVE_SERVER_DATA=0
ELEVATED=0
SERVER_OK=1
TMP=""

info() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() { printf 'error: %s\n' "$*" >&2; exit 1; }

cleanup() {
  if [[ -n "$TMP" && -d "$TMP" ]]; then
    rm -rf "$TMP"
  fi
}
trap cleanup EXIT

usage() { sed -n '2,30p' "$0" | sed 's/^# \{0,1\}//'; }

while [[ $# -gt 0 ]]; do
  case "$1" in
    --no-server) NO_SERVER=1 ;;
    --no-client) NO_CLIENT=1 ;;
    --no-wayland) NO_WAYLAND=1 ;;
    --no-elevate) NO_ELEVATE=1 ;;
    --cuda) CUDA=1 ;;
    --force-config) FORCE_CONFIG=1 ;;
    --dry-run) DRY_RUN=1 ;;
    --uninstall) UNINSTALL=1 ;;
    --purge) PURGE=1 ;;
    --remove-models) REMOVE_MODELS=1 ;;
    --remove-server-data) REMOVE_SERVER_DATA=1 ;;
    --from)
      shift
      [[ $# -gt 0 ]] || die "--from needs a directory"
      FROM_DIR="$1"
      ;;
    --gui)
      shift
      [[ $# -gt 0 ]] || die "--gui needs gtk4, gtk3, or auto"
      GUI_CHOICE="$1"
      ;;
    -h|--help) usage; exit 0 ;;
    *)
      die "unknown argument: $1"
      ;;
  esac
  shift
done

case "$GUI_CHOICE" in
  gtk4|gtk3|auto) ;;
  *) die "--gui must be gtk4, gtk3, or auto" ;;
esac

if [[ "$PURGE" == 1 && "$UNINSTALL" != 1 ]]; then
  die "--purge is only valid together with --uninstall"
fi
if [[ "$REMOVE_MODELS" == 1 || "$REMOVE_SERVER_DATA" == 1 ]] && [[ "$UNINSTALL" != 1 ]]; then
  die "--remove-models and --remove-server-data are only valid together with --uninstall"
fi

if [[ "$CUDA" == 1 && "$NO_SERVER" == 1 ]]; then
  die "--cuda needs the native Server bundle; omit --no-server"
fi

need() {
  command -v "$1" >/dev/null 2>&1 || die "$1 is required"
}

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  *)
    die "unsupported architecture $(uname -m); only linux/amd64 release artifacts are published"
    ;;
esac

if [[ -n "$FROM_DIR" ]]; then
  [[ -d "$FROM_DIR" ]] || die "artifact directory not found: $FROM_DIR"
  FROM_DIR="$(cd "$FROM_DIR" && pwd)"
fi

# -- detection -------------------------------------------------------------

ldcache() {
  if command -v ldconfig >/dev/null 2>&1; then
    ldconfig -p 2>/dev/null || true
  fi
}

has_lib() { ldcache | grep -F "$1" >/dev/null 2>&1; }

has_gtk4() { has_lib "libgtk-4.so.1" && has_lib "libwebkitgtk-6.0.so.4"; }
has_gtk3() { has_lib "libgtk-3.so.0" && has_lib "libwebkit2gtk-4.1.so.0"; }

detect_pm() {
  if [[ -n "$PM_OVERRIDE" ]]; then
    printf '%s\n' "$PM_OVERRIDE"
    return
  fi
  if command -v apt-get >/dev/null 2>&1; then
    printf 'apt\n'
  elif command -v dnf >/dev/null 2>&1; then
    printf 'dnf\n'
  elif command -v pacman >/dev/null 2>&1; then
    printf 'pacman\n'
  elif command -v zypper >/dev/null 2>&1; then
    printf 'zypper\n'
  else
    printf 'unknown\n'
  fi
}

# os_release_value reads a key from os-release. KITSUNE_OS_RELEASE lets the
# installer tests exercise old and new distributions without touching the host.
os_release_value() {
  local key="$1" file="${KITSUNE_OS_RELEASE:-/etc/os-release}"
  [[ -f "$file" ]] || return 0
  sed -n "s/^${key}=//p" "$file" | head -n1 | tr -d '"'
}

# distro_legacy says whether this distribution predates the WebKitGTK 6.0
# stack, even when no GTK library is installed yet. Ubuntu 24.04, Debian 12,
# Fedora 40, and RHEL 10 are the first releases with the default build.
distro_legacy() {
  local id major like codename
  id="$(os_release_value ID)"
  like="$(os_release_value ID_LIKE)"
  major="$(os_release_value VERSION_ID)"
  major="${major%%.*}"
  [[ "$major" =~ ^[0-9]+$ ]] || return 1
  # An Ubuntu derivative's own version rarely tracks its base release (KDE
  # neon 6.0 and elementary 8 are Ubuntu 24.04). Prefer the base codename.
  codename="$(os_release_value UBUNTU_CODENAME)"
  if [[ -z "$codename" && "$id" == ubuntu ]]; then
    codename="$(os_release_value VERSION_CODENAME)"
  fi
  if [[ -n "$codename" && "$id" != ubuntu ]]; then
    case "$codename" in
      jammy|focal|bionic|xenial|trusty) return 0 ;;
      *) return 1 ;;
    esac
  fi
  case "$id" in
    ubuntu) (( major < 24 )) ;;
    debian) (( major < 12 )) ;;
    fedora) (( major < 40 )) ;;
    rhel|centos|rocky|almalinux) (( major < 10 )) ;;
    *)
      case "$like" in
        *ubuntu*) (( major < 24 )) ;;
        *debian*) (( major < 12 )) ;;
        *fedora*) (( major < 40 )) ;;
        *rhel*) (( major < 10 )) ;;
        *) return 1 ;;
      esac
      ;;
  esac
}

select_gui() {
  case "$GUI_CHOICE" in
    gtk4) printf 'gtk4\n' ;;
    gtk3) printf 'gtk3\n' ;;
    auto)
      # GTK4/WebKitGTK 6.0 is the default even when only GTK3 happens to be
      # installed; the one sudo step can add the modern stack. Only a
      # distribution that predates WebKitGTK 6.0 gets the legacy build.
      if has_gtk4; then
        printf 'gtk4\n'
      elif distro_legacy; then
        printf 'gtk3\n'
      else
        printf 'gtk4\n'
      fi
      ;;
  esac
}

packages_for() {
  local pm="$1" gui="$2"
  case "$pm:$gui" in
    apt:gtk4) printf 'libgtk-4-1 libwebkitgtk-6.0-4\n' ;;
    apt:gtk3) printf 'libgtk-3-0 libwebkit2gtk-4.1-0\n' ;;
    dnf:gtk4) printf 'gtk4 webkitgtk6.0\n' ;;
    dnf:gtk3) printf 'gtk3 webkit2gtk4.1\n' ;;
    pacman:gtk4) printf 'gtk4 webkitgtk-6.0\n' ;;
    pacman:gtk3) printf 'gtk3 webkit2gtk-4.1\n' ;;
    zypper:gtk4) printf 'gtk4 libwebkitgtk-6_0-4\n' ;;
    zypper:gtk3) printf 'gtk3 libwebkit2gtk-4_1-0\n' ;;
    unknown:*) printf '\n' ;;
    *) printf '\n' ;;
  esac
}

uinput_ready() { [[ -r /dev/uinput && -w /dev/uinput ]]; }

gui_ready_for() {
  case "$1" in
    gtk4) has_gtk4 ;;
    gtk3) has_gtk3 ;;
    *) return 1 ;;
  esac
}

# -- artifact fetching -----------------------------------------------------

fetch_asset() {
  local asset="$1"
  if [[ -n "$FROM_DIR" ]]; then
    [[ -f "$FROM_DIR/$asset" ]] || die "missing release artifact: $FROM_DIR/$asset"
    cp "$FROM_DIR/$asset" "$TMP/$asset"
  else
    local base
    if [[ "$VERSION" == "latest" ]]; then
      base="https://github.com/$REPO/releases/latest/download"
    else
      base="https://github.com/$REPO/releases/download/$VERSION"
    fi
    info "Downloading $asset ($VERSION)..."
    curl -fsSL "$base/$asset" -o "$TMP/$asset" \
      || die "could not download $asset from $base"
  fi
}

verify_asset() {
  local asset="$1"
  local expected actual
  expected="$(awk -v name="$asset" '$2 == name { print $1; exit }' "$TMP/checksums.txt")"
  [[ -n "$expected" ]] || die "no checksum for $asset in checksums.txt"
  actual="$(sha256sum "$TMP/$asset" | awk '{print $1}')"
  if [[ "$actual" != "$expected" ]]; then
    die "checksum mismatch for $asset (expected $expected, got $actual)"
  fi
}

# -- elevation -------------------------------------------------------------

elevate_script() {
  local pm="$1" packages="$2"
  {
    cat <<'EOF'
set -eu
echo "kitsune-whisper: installing desktop and uinput prerequisites"
EOF
    case "$pm" in
      apt)
        cat <<'EOF'
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y --no-install-recommends
EOF
        printf '%s\n' $packages
        ;;
      dnf)
        printf 'dnf install -y'
        for package in $packages; do printf ' %s' "$package"; done
        printf '\n'
        ;;
      pacman)
        printf 'pacman -Sy --noconfirm --needed'
        for package in $packages; do printf ' %s' "$package"; done
        printf '\n'
        ;;
      zypper)
        printf 'zypper --non-interactive install'
        for package in $packages; do printf ' %s' "$package"; done
        printf '\n'
        ;;
      *)
        echo 'echo "unsupported package manager; install the GTK/WebKitGTK packages manually" >&2'
        ;;
    esac
    cat <<EOF
install -d -m 0755 /etc/udev/rules.d /etc/modules-load.d
cat > "$UDEV_RULE" <<'RULE'
# kitsune-whisper: give the active desktop session access to /dev/uinput so
# the bundled ydotoold can inject text without a group change or a re-login.
KERNEL=="uinput", SUBSYSTEM=="misc", TAG+="uaccess", OPTIONS+="static_node=uinput"
RULE
printf 'uinput\n' > "$MODULES_FILE"
modprobe uinput 2>/dev/null || true
udevadm control --reload-rules
udevadm trigger --name-match=uinput 2>/dev/null || true
EOF
  }
}

# -- install steps ---------------------------------------------------------

ensure_config() {
  local template="$1"
  install -d -m 0700 "$CONFIG_DIR"
  if [[ ! -f "$CONFIG" || "$FORCE_CONFIG" == 1 ]]; then
    if [[ -n "$template" && -f "$template" ]]; then
      cp "$template" "$CONFIG"
      chmod 0600 "$CONFIG"
      info "Wrote config template to $CONFIG"
    else
      warn "no kitsune.example.yaml available; leaving $CONFIG absent"
    fi
  else
    info "Keeping existing $CONFIG"
  fi
}

install_client() {
  local asset="$1"
  local dir="$TMP/client"
  mkdir -p "$dir"
  tar -xzf "$TMP/$asset" -C "$dir"
  [[ -f "$dir/kitsune-client" ]] || die "$asset does not contain kitsune-client"
  install -d -m 0755 "$BIN_DIR"
  install -m 0755 "$dir/kitsune-client" "$BIN_DIR/kitsune-client"
  info "Installed $BIN_DIR/kitsune-client"
  ensure_config "$dir/kitsune.example.yaml"
}

install_wayland_helpers() {
  local asset="kitsune-wayland_linux_${ARCH}.tar.gz"
  if [[ ! -f "$TMP/$asset" ]]; then
    warn "no $asset in the release; using Wayland helpers already on PATH"
    return 0
  fi
  rm -rf "$WAYLAND_DIR"
  install -d -m 0755 "$WAYLAND_BIN"
  tar -xzf "$TMP/$asset" -C "$WAYLAND_DIR"
  local tool
  for tool in ydotool ydotoold wtype; do
    if [[ -f "$WAYLAND_BIN/$tool" ]]; then
      chmod 0755 "$WAYLAND_BIN/$tool"
      ln -sf "$WAYLAND_BIN/$tool" "$BIN_DIR/$tool"
    fi
  done
  info "Installed bundled Wayland helpers in $WAYLAND_BIN"

  install -d -m 0755 "$SYSTEMD_DIR"
  cat > "$WAYLAND_UNIT" <<EOF
[Unit]
Description=kitsune-whisper ydotool daemon
Documentation=https://github.com/$REPO
PartOf=graphical-session.target
After=graphical-session.target

[Service]
Type=simple
ExecStart="$WAYLAND_BIN/ydotoold"
Restart=on-failure
RestartSec=2

[Install]
WantedBy=graphical-session.target
EOF

  install -d -m 0755 "$(dirname "$CLIENT_DROPIN")"
  cat > "$CLIENT_DROPIN" <<EOF
[Service]
Environment="PATH=$WAYLAND_BIN:/usr/local/bin:/usr/bin:/bin"
EOF
}

install_server() {
  local asset="kitsune-server_linux_${ARCH}.tar.gz"
  local dir="$TMP/server"
  mkdir -p "$dir"
  tar -xzf "$TMP/$asset" -C "$dir"
  local runtime="$dir/runtime/bin/python3"
  [[ -x "$runtime" ]] || die "$asset has no runnable runtime/bin/python3"
  [[ -f "$dir/bootstrap.py" ]] || die "$asset has no bootstrap.py"

  info "Installing the native Server bundle..."
  local config_args=()
  if [[ -f "$CONFIG" ]]; then
    config_args=(--config "$CONFIG")
  fi
  if ! "$runtime" -s "$dir/bootstrap.py" install \
      --artifact "$TMP/$asset" \
      --sha256 "$(awk -v name="$asset" '$2 == name { print $1; exit }' "$TMP/checksums.txt")" \
      "${config_args[@]}"; then
    SERVER_OK=0
    warn "the Server bundle could not be installed; the Client stays usable with a Remote Server"
  fi
}

install_cuda_runtime() {
  [[ "$CUDA" == 1 ]] || return 0
  if [[ "$SERVER_OK" != 1 || ! -x "$SERVER_ROOT/runtime/bin/python3" ]]; then
    warn "CUDA was requested, but the Server bundle is not installed; keeping CPU"
    return 0
  fi
  if ! command -v nvidia-smi >/dev/null 2>&1 || ! nvidia-smi -L >/dev/null 2>&1; then
    warn "CUDA was requested, but no NVIDIA GPU was detected; keeping CPU"
    return 0
  fi
  if [[ -f "$SERVER_ROOT/lib/nvidia/cublas/lib/libcublas.so.12" ]]; then
    info "The CUDA runtime is already installed"
    return 0
  fi

  if ! "$SERVER_ROOT/runtime/bin/python3" -m pip --version >/dev/null 2>&1; then
    warn "the Server runtime has no pip; install nvidia-cublas-cu12 manually"
    return 0
  fi

  info "Downloading the CUDA runtime (nvidia-cublas-cu12, ~550 MB)..."
  if ! "$SERVER_ROOT/runtime/bin/python3" -m pip install \
      --disable-pip-version-check --no-warn-script-location --no-input --upgrade \
      --target "$SERVER_ROOT/lib" nvidia-cublas-cu12; then
    warn "the CUDA runtime could not be installed; the Server will use CPU"
    return 0
  fi
  if [[ ! -f "$SERVER_ROOT/lib/nvidia/cublas/lib/libcublas.so.12" ]]; then
    warn "the CUDA runtime installed without libcublas.so.12; the Server will use CPU"
    return 0
  fi
  if command -v systemctl >/dev/null 2>&1; then
    systemctl --user restart kitsune-server.service >/dev/null 2>&1 \
      || warn "could not restart kitsune-server.service after installing CUDA"
  fi
  info "Installed the CUDA runtime; the Server will use the GPU when it restarts"
}

start_services() {
  if ! command -v systemctl >/dev/null 2>&1; then
    warn "systemctl is not available; start kitsune-client and kitsune-server from your desktop session"
    return 0
  fi
  systemctl --user daemon-reload >/dev/null 2>&1 || true
  systemctl --user restart kitsune-client.service >/dev/null 2>&1 \
    || systemctl --user start kitsune-client.service >/dev/null 2>&1 \
    || warn "could not start kitsune-client.service; run it inside your desktop session"

  if [[ "$NO_WAYLAND" == 0 && -x "$WAYLAND_BIN/ydotoold" ]]; then
    if [[ "$ELEVATED" == 1 ]] || uinput_ready; then
      systemctl --user enable --now kitsune-ydotoold.service >/dev/null 2>&1 \
        || warn "could not start kitsune-ydotoold.service"
    else
      systemctl --user enable kitsune-ydotoold.service >/dev/null 2>&1 \
        || warn "could not enable kitsune-ydotoold.service"
      warn "ydotoold will start at your next graphical login (no active /dev/uinput access here)"
    fi
  fi
}

degraded_note() {
  warn "Continuing without elevation."
  if gui_ready_for "$SELECTED_GUI"; then
    warn "The desktop shell is available; Wayland injection stays clipboard-only"
    warn "(the transcription is copied; press Ctrl+V). Re-run the installer with"
    warn "sudo access to enable the /dev/uinput rule."
  else
    warn "Settings open in your browser and Wayland injection stays clipboard-only"
    warn "(the transcription is copied; press Ctrl+V). Re-run the installer with sudo"
    warn "access to enable the embedded window and ydotool."
  fi
}

# -- uninstall -------------------------------------------------------------

confirm() {
  local answer
  # Read the answer from the controlling terminal, not stdin: the documented
  # `curl ... | bash -s -- --uninstall` path uses stdin for the script itself.
  if [[ ! -r /dev/tty ]]; then
    return 1
  fi
  printf '%s [y/N] ' "$1" >&2
  read -r answer < /dev/tty || return 1
  [[ "$answer" =~ ^[Yy]$ ]]
}

ask_what_to_delete() {
  if [[ "$PURGE" == 1 ]]; then
    return 0
  fi
  # A script run without a terminal (a test, CI, or a log-only invocation)
  # keeps user data unless the caller named the flags explicitly.
  if [[ ! -t 2 ]]; then
    return 0
  fi
  if [[ "$REMOVE_MODELS" == 0 ]]; then
    if confirm "Delete downloaded Whisper models (several GB)?"; then
      REMOVE_MODELS=1
    fi
  fi
  if [[ "$REMOVE_SERVER_DATA" == 0 ]]; then
    if confirm "Delete Server data (certificates, Devices, logs, soft-stop state)?"; then
      REMOVE_SERVER_DATA=1
    fi
  fi
}

uninstall_all() {
  ask_what_to_delete
  info "Removing kitsune-whisper..."
  if [[ -x "$BIN_DIR/kitsune-client" ]]; then
    "$BIN_DIR/kitsune-client" uninstall-autostart >/dev/null 2>&1 \
      || warn "could not remove the Client autostart registration"
    "$BIN_DIR/kitsune-client" uninstall-desktop >/dev/null 2>&1 \
      || warn "could not remove the application-menu entry"
  fi
  if [[ -x "$SERVER_LAUNCHER" ]]; then
    "$SERVER_LAUNCHER" uninstall >/dev/null 2>&1 \
      || warn "could not remove the Server bundle"
  fi
  if command -v systemctl >/dev/null 2>&1; then
    systemctl --user disable --now kitsune-ydotoold.service >/dev/null 2>&1 || true
    systemctl --user disable --now kitsune-client.service >/dev/null 2>&1 || true
  fi

  rm -f "$WAYLAND_UNIT" "$CLIENT_DROPIN" "$BIN_DIR/kitsune-client" "$SERVER_LAUNCHER" "$DESKTOP_FILE"
  rm -f "$MENU_FILE" "$ICON_SVG" "$ICON_PNG"
  rm -f "$SYSTEMD_DIR/kitsune-server.service"
  rmdir "$(dirname "$CLIENT_DROPIN")" 2>/dev/null || true
  rm -rf "$WAYLAND_DIR" "$SERVER_ROOT"
  rm -f "$BIN_DIR/ydotool" "$BIN_DIR/ydotoold" "$BIN_DIR/wtype"

  if [[ "$REMOVE_MODELS" == 1 ]]; then
    rm -rf "$CACHE_DIR/models"
    info "Removed downloaded models"
  fi
  if [[ "$REMOVE_SERVER_DATA" == 1 ]]; then
    rm -rf "$STATE_DIR"
    info "Removed Server certificates, Devices, logs, and soft-stop state"
  fi

  if [[ "$PURGE" == 1 ]]; then
    rm -rf "$CONFIG_DIR" "$CACHE_DIR" "$STATE_DIR" "$LOCAL_LIB"
    if [[ "$NO_ELEVATE" == 0 ]] && command -v sudo >/dev/null 2>&1; then
      if [[ -f "$UDEV_RULE" || -f "$MODULES_FILE" ]]; then
        if sudo sh -c "rm -f '$UDEV_RULE' '$MODULES_FILE' && { udevadm control --reload-rules >/dev/null 2>&1 || true; }"; then
          info "Removed the udev uinput rule"
        else
          warn "could not remove $UDEV_RULE; remove it manually"
        fi
      elif [[ -f "$UDEV_RULE_SYSTEM" || -f "$MODULES_FILE_SYSTEM" ]]; then
        info "The udev rule is owned by the installed package; remove the package to delete it"
      fi
    fi
    info "kitsune-whisper purged (config, models, certificates, Devices, spool, logs, wizard)"
  else
    info "kitsune-whisper uninstalled (configuration, spool, wizard progress, and Device tokens kept)"
    [[ "$REMOVE_MODELS" == 1 ]] || info "Downloaded models were kept; pass --remove-models to delete them"
    [[ "$REMOVE_SERVER_DATA" == 1 ]] || info "Server certificates, Devices, and logs were kept; pass --remove-server-data to delete them"
  fi

  if command -v systemctl >/dev/null 2>&1; then
    systemctl --user daemon-reload >/dev/null 2>&1 || true
  fi
}

# -- main ------------------------------------------------------------------

if [[ "$UNINSTALL" == 1 ]]; then
  uninstall_all
  exit 0
fi

need tar
need sha256sum
need awk
need grep
need sed
if [[ -z "$FROM_DIR" ]]; then
  need curl
fi

SELECTED_GUI="$(select_gui)"
PM="$(detect_pm)"
PACKAGES="$(packages_for "$PM" "$SELECTED_GUI")"

if [[ "$SELECTED_GUI" == "gtk3" ]]; then
  CLIENT_ASSET="kitsune-client_linux_${ARCH}-legacy.tar.gz"
else
  CLIENT_ASSET="kitsune-client_linux_${ARCH}.tar.gz"
fi

NEEDS_PACKAGES=0
if ! gui_ready_for "$SELECTED_GUI"; then
  NEEDS_PACKAGES=1
fi
NEEDS_RULE=0
if [[ ! -f "$UDEV_RULE" && ! -f "$UDEV_RULE_SYSTEM" ]] \
  || [[ ! -f "$MODULES_FILE" && ! -f "$MODULES_FILE_SYSTEM" ]]; then
  NEEDS_RULE=1
elif ! uinput_ready && [[ -n "${WAYLAND_DISPLAY:-}${DISPLAY:-}" ]]; then
  # The rule exists but this desktop session did not get the ACL (for example
  # after adding the rule outside a seat). Re-run the rule step instead of
  # silently asking the user to log out.
  NEEDS_RULE=1
fi
NEEDS_ELEVATION=0
if [[ "$NEEDS_PACKAGES" == 1 || "$NEEDS_RULE" == 1 ]]; then
  NEEDS_ELEVATION=1
fi

if [[ "$DRY_RUN" == 1 ]]; then
  info "kitsune-whisper install plan"
  info "  Client:       $([[ "$NO_CLIENT" == 0 ]] && printf '%s' "$BIN_DIR/kitsune-client ($SELECTED_GUI)" || printf 'disabled')"
  info "  Config:       $CONFIG"
  info "  Wayland:      $([[ "$NO_WAYLAND" == 0 ]] && printf '%s' "$WAYLAND_BIN" || printf 'disabled')"
  info "  Server:       $([[ "$NO_SERVER" == 0 ]] && printf '%s' "$SERVER_ROOT" || printf 'disabled')"
  info "  CUDA:         $([[ "$CUDA" == 1 ]] && printf 'install nvidia-cublas-cu12 (~550 MB)' || printf 'not requested')"
  info "  Elevation:    $([[ "$NEEDS_ELEVATION" == 1 ]] && printf 'one sudo step (%s: %s)' "$PM" "$PACKAGES" || printf 'not needed')"
  exit 0
fi

TMP="$(mktemp -d "${TMPDIR:-/tmp}/kitsune-install.XXXXXX")"
chmod 0700 "$TMP"

ASSETS=()
if [[ "$NO_CLIENT" == 0 ]]; then
  ASSETS+=("$CLIENT_ASSET")
fi
if [[ "$NO_WAYLAND" == 0 ]]; then
  ASSETS+=("kitsune-wayland_linux_${ARCH}.tar.gz")
fi
if [[ "$NO_SERVER" == 0 ]]; then
  ASSETS+=("kitsune-server_linux_${ARCH}.tar.gz")
fi

fetch_asset checksums.txt
for asset in "${ASSETS[@]}"; do
  fetch_asset "$asset"
done
for asset in "${ASSETS[@]}"; do
  verify_asset "$asset"
done

if [[ "$NEEDS_ELEVATION" == 1 ]]; then
  if [[ "$NO_ELEVATE" == 1 ]]; then
    ELEVATED=0
    degraded_note
  elif ! command -v sudo >/dev/null 2>&1; then
    ELEVATED=0
    warn "sudo is not installed, so the one system setup step cannot run"
    degraded_note
  else
    elevate_script "$PM" "$PACKAGES" > "$TMP/elevate.sh"
    info "One privileged step is required:"
    info "  - install GTK/WebKitGTK ($PACKAGES)"
    info "  - install the udev uaccess rule for /dev/uinput (Wayland text injection)"
    info "The Client and Server themselves run as your user; nothing else uses root."
    if sudo bash "$TMP/elevate.sh"; then
      ELEVATED=1
    else
      ELEVATED=0
      warn "the privileged setup step did not complete"
      degraded_note
    fi
  fi
fi

if [[ "$NO_CLIENT" == 0 ]]; then
  install_client "$CLIENT_ASSET"
else
  ensure_config "${KITSUNE_CONFIG_TEMPLATE:-}"
fi

if [[ -x "$BIN_DIR/kitsune-client" && -f "$CONFIG" ]]; then
  if "$BIN_DIR/kitsune-client" migrate-config --config "$CONFIG"; then
    info "Checked $CONFIG for new settings"
  else
    warn "could not migrate $CONFIG; the Client still reads it with defaults"
  fi
fi

if [[ "$NO_WAYLAND" == 0 ]]; then
  install_wayland_helpers
fi

if [[ "$NO_SERVER" == 0 ]]; then
  install_server
  install_cuda_runtime
fi

if [[ -x "$BIN_DIR/kitsune-client" ]]; then
  if "$BIN_DIR/kitsune-client" install-autostart >/dev/null 2>&1; then
    info "Registered kitsune-client.service (starts hidden in the Tray at login)"
  else
    warn "could not register the Client autostart from this shell"
    warn "run '$BIN_DIR/kitsune-client install-autostart' inside your desktop session"
  fi
  if "$BIN_DIR/kitsune-client" install-desktop >/dev/null 2>&1; then
    info "Added kitsune-whisper to the application menu"
  else
    warn "could not add the application-menu entry"
    warn "run '$BIN_DIR/kitsune-client install-desktop' inside your desktop session"
  fi
  # Refresh the per-user caches so a launcher that is already open sees the
  # entry and icon without waiting for its own rescan.
  if command -v update-desktop-database >/dev/null 2>&1; then
    update-desktop-database -q "$DATA_ROOT/applications" >/dev/null 2>&1 || true
  fi
  if command -v gtk-update-icon-cache >/dev/null 2>&1; then
    gtk-update-icon-cache -q -t -f "$DATA_ROOT/icons/hicolor" >/dev/null 2>&1 || true
  fi
fi

start_services

if [[ "$SELECTED_GUI" != "gtk4" && "$SELECTED_GUI" != "gtk3" ]]; then
  warn "no desktop toolkit was selected; the Client will run headless"
elif ! gui_ready_for "$SELECTED_GUI"; then
  warn "$SELECTED_GUI libraries are still missing; Settings will use the browser fallback"
fi

if [[ "$SERVER_OK" == 0 ]]; then
  warn "the Client is installed, but the local Server did not start"
  exit 1
fi

info "kitsune-whisper installed."
if [[ "$NO_CLIENT" == 0 ]]; then
  info "  Client: $BIN_DIR/kitsune-client"
else
  info "  Client: /usr/bin/kitsune-client (installed by the package)"
fi
if [[ "$NO_SERVER" == 0 ]]; then
  info "  Server: $SERVER_LAUNCHER (systemctl --user status kitsune-server)"
fi
info "  Press Ctrl+Shift+Space to dictate; open Settings from the application menu or the Tray."
