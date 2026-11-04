#!/usr/bin/env bash
# kitsune-whisper Linux installer.
#
#   curl -fsSL https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/install.sh | bash
#
# Installs the Client (Tray plus Settings window), the native per-user Server
# bundle, or both under your home directory, and stops there. Nothing is
# elevated, started, enabled, or registered to run at login. Dependencies are
# listed in the README; the installer only checks and warns.
#
# Re-run the installer to upgrade in place; kitsune.yaml, models, Devices, and
# certificates are kept. Use --uninstall to remove what was installed.
set -euo pipefail

REPO="FoxRed-cmd/kitsune-whisper"
VERSION="${KITSUNE_VERSION:-latest}"
BIN_DIR="$HOME/.local/bin"
LOCAL_LIB="$HOME/.local/lib/kitsune-whisper"
SERVER_ROOT="$LOCAL_LIB/server"
SERVER_LAUNCHER="$HOME/.local/bin/kitsune-server"
CONFIG_ROOT="${XDG_CONFIG_HOME:-$HOME/.config}"
DATA_ROOT="${XDG_DATA_HOME:-$HOME/.local/share}"
APP_DATA="$DATA_ROOT/io.github.FoxRed-cmd.kitsune-whisper"
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

FROM_DIR="${KITSUNE_ARTIFACTS_DIR:-}"
CLIENT_ONLY=0
SERVER_ONLY=0
DRY_RUN=0
FORCE_CONFIG=0
UNINSTALL=0
PURGE=0
REMOVE_MODELS=0
REMOVE_SERVER_DATA=0
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

# The usage text is embedded so --help works under `curl ... | bash`, where $0
# is the interpreter and the script is not a readable file.
usage() {
  cat <<'EOF'
kitsune-whisper Linux installer.

Installs the Client, the per-user Server bundle, or both under your home
directory. Nothing is elevated, started, enabled, or registered for autostart.

Usage: install.sh [options]

  --client-only          install only the Client
  --server-only          install only the Server bundle
                         (neither flag installs both)

  --from DIR             install from release artifacts in DIR
  --force-config         overwrite an existing kitsune.yaml
  --dry-run              print the plan and change nothing
  --uninstall            remove binaries, bundle, registrations, menu and
                         icons; keep config, models, pairing and logs
  --uninstall --purge    also remove config, models, cache, state, logs,
                         spool, wizard progress, and keyring tokens
  --remove-models        with --uninstall: delete downloaded models
  --remove-server-data   with --uninstall: delete certificates, Devices,
                         logs, and soft-stop state
  -h, --help             show this help

Environment: KITSUNE_VERSION, KITSUNE_ARTIFACTS_DIR
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --client-only) CLIENT_ONLY=1 ;;
    --server-only) SERVER_ONLY=1 ;;
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
    -h|--help) usage; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
  shift
done

if [[ "$CLIENT_ONLY" == 1 && "$SERVER_ONLY" == 1 ]]; then
  die "--client-only and --server-only are mutually exclusive"
fi

INSTALL_CLIENT=1
INSTALL_SERVER=1
if [[ "$CLIENT_ONLY" == 1 ]]; then
  INSTALL_SERVER=0
fi
if [[ "$SERVER_ONLY" == 1 ]]; then
  INSTALL_CLIENT=0
fi

if [[ "$PURGE" == 1 && "$UNINSTALL" != 1 ]]; then
  die "--purge is only valid together with --uninstall"
fi
if [[ "$REMOVE_MODELS" == 1 || "$REMOVE_SERVER_DATA" == 1 ]] && [[ "$UNINSTALL" != 1 ]]; then
  die "--remove-models and --remove-server-data are only valid together with --uninstall"
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
      --no-start \
      --artifact "$TMP/$asset" \
      --sha256 "$(awk -v name="$asset" '$2 == name { print $1; exit }' "$TMP/checksums.txt")" \
      "${config_args[@]}"; then
    SERVER_OK=0
    warn "the Server bundle could not be installed"
  fi
}

install_desktop_entry() {
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

# forget_keyring_tokens removes the Device tokens the Client stored in the
# desktop keyring. A purge must not leave working credentials behind, and the
# tokens are keyed by profile id, so every matching entry is cleared. A system
# without secret-tool or a locked keyring leaves the fallback token files, which
# live in CONFIG_DIR and are removed with it.
forget_keyring_tokens() {
  command -v secret-tool >/dev/null 2>&1 || return 0
  local users user
  users="$(secret-tool search --all service kitsune-whisper 2>&1 \
    | awk -F' = ' '/^attribute.username = /{print $2}')"
  [[ -n "$users" ]] || return 0
  while IFS= read -r user; do
    [[ -n "$user" ]] || continue
    secret-tool clear service kitsune-whisper username "$user" >/dev/null 2>&1 || true
  done <<< "$users"
}

# reset_local_pairing forgets the Local Server certificate pin and Device
# token. Removing Server data creates a new certificate, and the kept Client
# configuration would otherwise pin the old fingerprint and report "certificate
# changed" after reinstall.
reset_local_pairing() {
  [[ -x "$BIN_DIR/kitsune-client" ]] || return 0
  "$BIN_DIR/kitsune-client" reset-local-pairing >/dev/null 2>&1 \
    || warn "could not reset the Local Server pairing; re-pair it after reinstall"
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

# remove_client_registration stops and forgets the login registration. The
# unit and its hidden portal entry are removed even if the Client binary is
# already gone, so no unit is left pointing at a removed executable.
remove_client_registration() {
  if [[ -x "$BIN_DIR/kitsune-client" ]]; then
    "$BIN_DIR/kitsune-client" uninstall-autostart >/dev/null 2>&1 \
      || warn "could not remove the Client autostart registration"
  fi
  rm -f "$SYSTEMD_DIR/kitsune-client.service" "$DESKTOP_FILE"
}

# remove_menu_entry removes the visible application-menu entry and its icons.
remove_menu_entry() {
  if [[ -x "$BIN_DIR/kitsune-client" ]]; then
    "$BIN_DIR/kitsune-client" uninstall-desktop >/dev/null 2>&1 \
      || warn "could not remove the application-menu entry"
  fi
  rm -f "$MENU_FILE" "$ICON_SVG" "$ICON_PNG"
}

# remove_server_registration stops and forgets the Server supervisor. The
# launcher's own uninstall runs the supervisor cleanup; the unit and cached
# task files are removed as a fallback when the launcher is already gone.
remove_server_registration() {
  if [[ -x "$SERVER_LAUNCHER" ]]; then
    "$SERVER_LAUNCHER" uninstall >/dev/null 2>&1 \
      || warn "could not remove the Server bundle"
  fi
  rm -f "$SYSTEMD_DIR/kitsune-server.service" "$CONFIG_DIR/kitsune-server-task.xml"
  rm -rf "$SERVER_ROOT"
}

uninstall_all() {
  ask_what_to_delete
  info "Removing kitsune-whisper..."
  remove_client_registration
  remove_menu_entry
  remove_server_registration

  if [[ "$REMOVE_SERVER_DATA" == 1 && "$PURGE" == 0 ]]; then
    reset_local_pairing
  fi
  rm -f "$BIN_DIR/kitsune-client" "$SERVER_LAUNCHER"
  rmdir "$LOCAL_LIB" 2>/dev/null || true

  if [[ "$REMOVE_MODELS" == 1 ]]; then
    rm -rf "$CACHE_DIR/models"
    info "Removed downloaded models"
  fi
  if [[ "$REMOVE_SERVER_DATA" == 1 ]]; then
    rm -rf "$STATE_DIR"
    info "Removed Server certificates, Devices, logs, and soft-stop state"
  fi

  if [[ "$PURGE" == 1 ]]; then
    forget_keyring_tokens
    rm -rf "$CONFIG_DIR" "$CACHE_DIR" "$STATE_DIR" "$LOCAL_LIB" "$APP_DATA"
    if [[ -n "${XDG_RUNTIME_DIR:-}" ]]; then
      rm -f "$XDG_RUNTIME_DIR/kitsune-whisper.sock" "$XDG_RUNTIME_DIR/kitsune-whisper.lock"
    fi
    info "kitsune-whisper purged (config, models, certificates, Devices, spool, logs, wizard)"
  else
    info "kitsune-whisper uninstalled (configuration, spool, wizard progress, and Device tokens kept)"
    [[ "$REMOVE_MODELS" == 1 ]] || info "Downloaded models were kept; pass --remove-models to delete them"
    [[ "$REMOVE_SERVER_DATA" == 1 ]] || info "Server certificates, Devices, and logs were kept; pass --remove-server-data to delete them"
  fi
}

# -- main ------------------------------------------------------------------

if [[ "$UNINSTALL" == 1 ]]; then
  uninstall_all
  exit 0
fi

if [[ "$DRY_RUN" == 1 ]]; then
  info "kitsune-whisper install plan"
  if [[ "$INSTALL_CLIENT" == 1 ]]; then
    info "  Client:  $BIN_DIR/kitsune-client"
  else
    info "  Client:  skipped (--server-only)"
  fi
  if [[ "$INSTALL_SERVER" == 1 ]]; then
    info "  Server:  $SERVER_LAUNCHER (installed, not started)"
  else
    info "  Server:  skipped (--client-only)"
  fi
  info "  Config:  $CONFIG"
  if [[ -n "$FROM_DIR" ]]; then
    info "  Source:  $FROM_DIR"
  else
    info "  Source:  GitHub release $VERSION"
  fi
  info "  Privilege escalation: none; autostart registration: none"
  exit 0
fi

need tar
need sha256sum
need awk
if [[ -z "$FROM_DIR" ]]; then
  need curl
fi

TMP="$(mktemp -d "${TMPDIR:-/tmp}/kitsune-install.XXXXXX")"
chmod 0700 "$TMP"

ASSETS=()
if [[ "$INSTALL_CLIENT" == 1 ]]; then
  ASSETS+=("kitsune-client_linux_${ARCH}.tar.gz")
fi
if [[ "$INSTALL_SERVER" == 1 ]]; then
  ASSETS+=("kitsune-server_linux_${ARCH}.tar.gz")
fi

fetch_asset checksums.txt
for asset in "${ASSETS[@]}"; do
  fetch_asset "$asset"
done
for asset in "${ASSETS[@]}"; do
  verify_asset "$asset"
done

if [[ "$INSTALL_CLIENT" == 1 ]]; then
  install_client "kitsune-client_linux_${ARCH}.tar.gz"
fi

if [[ -x "$BIN_DIR/kitsune-client" && -f "$CONFIG" ]]; then
  if "$BIN_DIR/kitsune-client" migrate-config --config "$CONFIG"; then
    info "Checked $CONFIG for new settings"
  else
    warn "could not migrate $CONFIG; the Client still reads it with defaults"
  fi
fi

if [[ "$INSTALL_SERVER" == 1 ]]; then
  install_server
fi

if [[ -x "$BIN_DIR/kitsune-client" ]]; then
  install_desktop_entry
fi

if [[ "$SERVER_OK" == 0 ]]; then
  warn "the selected components were not all installed"
  exit 1
fi

info "kitsune-whisper installed."
if [[ "$INSTALL_CLIENT" == 1 ]]; then
  info "  Client: $BIN_DIR/kitsune-client"
fi
if [[ "$INSTALL_SERVER" == 1 ]]; then
  info "  Server: $SERVER_LAUNCHER (registered, not started)"
fi
info "  Nothing was started or enabled; launch kitsune-whisper from the application menu."
