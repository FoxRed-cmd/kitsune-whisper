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

# The release key that signs checksums.txt. It is the same PEM committed at
# packaging/keys/kitsune-release-public.pem; pinning it here means a compromised
# download host cannot substitute both the checksums and their signature.
RELEASE_PUBLIC_KEY_PEM='-----BEGIN PUBLIC KEY-----
MFkwEwYHKoZIzj0CAQYIKoZIzj0DAQcDQgAEGh5umJxHjCLuQS4EVIkydw0zrLKU
f+HckG02s6mF/HzNPpGCLIZ6RBotAeC5KTfg6B0mmMnLopxnc1WKln/kYQ==
-----END PUBLIC KEY-----'
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
MANIFEST="$STATE_DIR/install.json"
SYSTEMD_DIR="$CONFIG_ROOT/systemd/user"
DESKTOP_FILE="$DATA_ROOT/applications/io.github.FoxRed-cmd.kitsune-whisper.desktop"
MENU_FILE="$DATA_ROOT/applications/kitsune-whisper.desktop"
ICON_SVG="$DATA_ROOT/icons/hicolor/scalable/apps/io.github.FoxRed-cmd.kitsune-whisper.svg"
ICON_PNG="$DATA_ROOT/icons/hicolor/256x256/apps/io.github.FoxRed-cmd.kitsune-whisper.png"

FROM_DIR="${KITSUNE_ARTIFACTS_DIR:-}"
CLIENT_ONLY=0
SERVER_ONLY=0
DRY_RUN=0
FORCE=0
FORCE_CONFIG=0
CHECK=0
INSECURE=0
AVAILABLE_VERSION=""
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
  --version VERSION      install VERSION instead of the latest release
  --force                allow installing a version older than the manifest
  --force-config         overwrite an existing kitsune.yaml
  --check                report installed versus available versions and
                         installed components; change nothing
  --dry-run              print the plan and change nothing
  --insecure             skip the release signature check; local development
                         only, never for a download you do not control
  --uninstall            remove binaries and desktop entries; keep data
  --uninstall --purge    also remove config, models, certificates, spool,
                         logs, Devices, and wizard progress
  --remove-models        with --uninstall: delete downloaded models
  --remove-server-data   with --uninstall: delete certificates, Devices,
                         the Server log, and its soft-stop marker
  -h, --help             show this help

The install manifest is written to
${XDG_STATE_HOME:-$HOME/.local/state}/kitsune-whisper/install.json.

Environment: KITSUNE_VERSION, KITSUNE_ARTIFACTS_DIR
Requires: tar, sha256sum, awk, openssl (openssl is not needed with --insecure)
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --client-only) CLIENT_ONLY=1 ;;
    --server-only) SERVER_ONLY=1 ;;
    --force) FORCE=1 ;;
    --force-config) FORCE_CONFIG=1 ;;
    --check) CHECK=1 ;;
    --dry-run) DRY_RUN=1 ;;
    --insecure) INSECURE=1 ;;
    --uninstall) UNINSTALL=1 ;;
    --purge) PURGE=1 ;;
    --remove-models) REMOVE_MODELS=1 ;;
    --remove-server-data) REMOVE_SERVER_DATA=1 ;;
    --version)
      shift
      [[ $# -gt 0 ]] || die "--version needs a version"
      VERSION="$1"
      ;;
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

if [[ "$CHECK" == 1 && "$UNINSTALL" == 1 ]]; then
  die "--check and --uninstall are mutually exclusive"
fi

if [[ "$VERSION" != "latest" && ! "$VERSION" =~ ^v?[0-9A-Za-z][0-9A-Za-z._+-]*$ ]]; then
  die "invalid version: $VERSION"
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
    if [[ ! -f "$FROM_DIR/$asset" ]]; then
      if [[ "$asset" == *.sig ]]; then
        die "missing release signature: $FROM_DIR/$asset"
      fi
      die "missing release artifact: $FROM_DIR/$asset"
    fi
    cp "$FROM_DIR/$asset" "$TMP/$asset"
  else
    local base
    if [[ "$VERSION" == "latest" ]]; then
      base="https://github.com/$REPO/releases/latest/download"
    else
      base="https://github.com/$REPO/releases/download/$(release_tag)"
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

# release_public_key_file exposes the pinned key as a file for openssl. The
# KITSUNE_RELEASE_PUBLIC_KEY override exists only so the installer tests can
# sign their fixture artifacts with a throwaway key; production pins the PEM
# above. The override accepts either a path or the PEM text itself.
release_public_key_file() {
  local override="${KITSUNE_RELEASE_PUBLIC_KEY:-}"
  local key_file="$TMP/release-public-key.pem"
  if [[ -n "$override" ]]; then
    if [[ -f "$override" ]]; then
      cp "$override" "$key_file"
    else
      printf '%s\n' "$override" > "$key_file"
    fi
  else
    printf '%s\n' "$RELEASE_PUBLIC_KEY_PEM" > "$key_file"
  fi
  printf '%s' "$key_file"
}

verify_release_signature() {
  local key_file
  key_file="$(release_public_key_file)"
  if ! openssl dgst -sha256 -verify "$key_file" \
      -signature "$TMP/checksums.txt.sig" "$TMP/checksums.txt" >/dev/null 2>&1; then
    die "release signature verification failed: checksums.txt.sig does not match the pinned release key"
  fi
  info "Verified the release signature for checksums.txt"
}

# -- versions and the install manifest ------------------------------------

release_tag() {
  if [[ "$VERSION" == v* ]]; then
    printf '%s' "$VERSION"
  else
    printf 'v%s' "$VERSION"
  fi
}

version_number() {
  local value="$1"
  printf '%s' "${value#v}"
}

version_less() {
  local first
  [[ "$1" != "$2" ]] || return 1
  first="$(printf '%s\n%s\n' "$1" "$2" | sort -V | head -n 1)"
  [[ "$first" == "$1" ]]
}

json_escape() {
  local value="$1"
  value="${value//\\/\\\\}"
  value="${value//\"/\\\"}"
  value="${value//$'\n'/\\n}"
  value="${value//$'\r'/\\r}"
  value="${value//$'\t'/\\t}"
  printf '%s' "$value"
}

# local_artifact_version reads the version an offline artifact directory
# carries: a VERSION file, or the Server bundle's own manifest.
local_artifact_version() {
  local archive="$FROM_DIR/kitsune-server_linux_${ARCH}.tar.gz"
  if [[ -f "$FROM_DIR/VERSION" ]]; then
    tr -d '[:space:]' < "$FROM_DIR/VERSION" | head -n 1
    return 0
  fi
  if [[ -f "$archive" ]] && command -v tar >/dev/null 2>&1; then
    tar -xOf "$archive" bundle.json 2>/dev/null \
      | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
      | head -n 1 || true
  fi
}

# resolve_latest_version asks the GitHub release API for the tag, then falls
# back to the redirect target of /releases/latest when the API is rate-limited.
resolve_latest_version() {
  local tag="" effective=""
  command -v curl >/dev/null 2>&1 || return 0
  tag="$(curl -fsSL --max-time 15 "https://api.github.com/repos/$REPO/releases/latest" 2>/dev/null \
    | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
    | head -n 1 || true)"
  if [[ -z "$tag" ]]; then
    effective="$(curl -fsSL --max-time 15 -o /dev/null -w '%{url_effective}' \
      "https://github.com/$REPO/releases/latest" 2>/dev/null || true)"
    if [[ "$effective" == *"/tag/"* ]]; then
      tag="${effective##*/tag/}"
    fi
  fi
  version_number "$tag"
}

resolve_available_version() {
  local candidate=""
  if [[ "$VERSION" != "latest" ]]; then
    candidate="$(version_number "$VERSION")"
  elif [[ -n "$FROM_DIR" ]]; then
    candidate="$(local_artifact_version || true)"
  else
    candidate="$(resolve_latest_version || true)"
  fi
  AVAILABLE_VERSION="$(printf '%s' "$candidate" | tr -d '[:space:]')"
}

explain_unknown_version() {
  if [[ -n "$AVAILABLE_VERSION" ]]; then
    return 0
  fi
  if [[ -n "$FROM_DIR" ]]; then
    warn "cannot determine the artifact version in $FROM_DIR; the downgrade guard is disabled"
  else
    warn "could not resolve the latest release version; the downgrade guard is disabled"
  fi
}

manifest_version() {
  [[ -f "$MANIFEST" ]] || return 0
  sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$MANIFEST" \
    | head -n 1
}

manifest_components() {
  [[ -f "$MANIFEST" ]] || return 0
  sed -n 's/.*"components"[[:space:]]*:[[:space:]]*\[\([^]]*\)\].*/\1/p' "$MANIFEST" \
    | tr -d '"' | tr ',' ' ' | tr -s ' ' | sed 's/^ *//; s/ *$//'
}

detect_components() {
  local found=""
  if [[ -x "$BIN_DIR/kitsune-client" ]]; then
    found="client"
  fi
  if [[ -x "$SERVER_LAUNCHER" ]]; then
    found="${found:+$found, }server"
  fi
  printf '%s' "$found"
}

guard_downgrade() {
  local installed
  installed="$(manifest_version)"
  [[ -n "$installed" && "$installed" != "unknown" ]] || return 0
  [[ -n "$AVAILABLE_VERSION" ]] || return 0
  if version_less "$AVAILABLE_VERSION" "$installed"; then
    if [[ "$FORCE" == 1 ]]; then
      info "Downgrading from $installed to $AVAILABLE_VERSION (--force)"
    else
      die "refusing to downgrade from $installed to $AVAILABLE_VERSION; pass --force to install it anyway"
    fi
  fi
}

write_manifest() {
  local json_components="" installed_at tmp_manifest
  if [[ -x "$BIN_DIR/kitsune-client" ]]; then
    json_components='"client"'
  fi
  if [[ -x "$SERVER_LAUNCHER" ]]; then
    json_components="${json_components:+$json_components, }\"server\""
  fi
  [[ -n "$json_components" ]] || return 0

  installed_at="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  install -d -m 0700 "$STATE_DIR"
  tmp_manifest="$MANIFEST.tmp"
  printf '{\n  "version": "%s",\n  "components": [%s],\n  "installed_at": "%s"\n}\n' \
    "$(json_escape "${AVAILABLE_VERSION:-unknown}")" "$json_components" "$installed_at" \
    > "$tmp_manifest"
  chmod 0600 "$tmp_manifest"
  mv "$tmp_manifest" "$MANIFEST"
  info "Wrote the install manifest to $MANIFEST"
}

check_status() {
  local installed components
  resolve_available_version
  installed="$(manifest_version)"
  components="$(manifest_components)"
  if [[ -z "$installed" ]]; then
    if [[ -x "$BIN_DIR/kitsune-client" || -x "$SERVER_LAUNCHER" ]]; then
      installed="unknown (no install manifest)"
    else
      installed="not installed"
    fi
  fi
  if [[ -z "$components" ]]; then
    components="$(detect_components)"
  fi
  [[ -n "$components" ]] || components="none"

  info "Manifest: $MANIFEST"
  info "Installed: $installed"
  info "Components: $components"
  if [[ -n "$AVAILABLE_VERSION" ]]; then
    info "Available: $AVAILABLE_VERSION"
  else
    info "Available: unknown"
  fi
  explain_unknown_version
  exit 0
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

uninstall_all() {
  ask_what_to_delete
  info "Removing kitsune-whisper..."
  if [[ -x "$BIN_DIR/kitsune-client" ]]; then
    "$BIN_DIR/kitsune-client" uninstall-desktop >/dev/null 2>&1 \
      || warn "could not remove the application-menu entry"
  fi
  if [[ -x "$SERVER_LAUNCHER" ]]; then
    "$SERVER_LAUNCHER" uninstall >/dev/null 2>&1 \
      || warn "could not remove the Server bundle"
  fi

  if [[ "$REMOVE_SERVER_DATA" == 1 && "$PURGE" == 0 ]]; then
    reset_local_pairing
  fi
  rm -f "$BIN_DIR/kitsune-client" "$SERVER_LAUNCHER" "$DESKTOP_FILE"
  rm -f "$MENU_FILE" "$ICON_SVG" "$ICON_PNG"
  rm -f "$SYSTEMD_DIR/kitsune-server.service"
  rm -rf "$SERVER_ROOT"
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

if [[ "$CHECK" == 1 ]]; then
  check_status
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
  info "  Manifest: $MANIFEST"
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
if [[ "$INSECURE" != 1 ]]; then
  need openssl
fi
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

# The signature covers checksums.txt, so verify it before trusting any digest
# and before fetching or extracting an archive.
fetch_asset checksums.txt
if [[ "$INSECURE" == 1 ]]; then
  warn "--insecure: skipping the release signature check"
else
  fetch_asset checksums.txt.sig
  verify_release_signature
fi

# Resolve the version now, before any archive is unpacked, so a downgrade is
# refused before it can replace a working install.
resolve_available_version
explain_unknown_version
guard_downgrade

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

write_manifest

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
if [[ -n "$AVAILABLE_VERSION" ]]; then
  info "  Version: $AVAILABLE_VERSION"
fi
info "  Manifest: $MANIFEST"
info "  Nothing was started or enabled; launch kitsune-whisper from the application menu."
