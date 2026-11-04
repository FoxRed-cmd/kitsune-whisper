#!/usr/bin/env bash
# Prove that the Linux release artifacts install with the checked-out installer.
#
# The script builds an isolated HOME/XDG tree, puts a fake systemctl, loginctl
# and every forbidden privilege helper on PATH, then runs install.sh against a
# local artifact directory signed with a throwaway key. It asserts the installed
# tree, the binaries' --version output, --check, reinstall, uninstall and purge.
#
# Run it inside a disposable ubuntu:24.04 container:
#
#   scripts/ci-install-smoke.sh --container --artifacts dist \
#     --public-key dist/ci-public-key.pem
#
# Or run the same assertions directly against an isolated HOME on this host
# (useful when a container image cannot be pulled):
#
#   scripts/ci-install-smoke.sh --artifacts dist \
#     --public-key dist/ci-public-key.pem --skip-system-check
#
# The CI job (install-smoke-linux in .github/workflows/ci.yml) builds and signs
# the artifacts with an ephemeral ECDSA P-256 key; the production key is never
# used here.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ARTIFACTS=""
VERSION=""
INSTALLER="$REPO_ROOT/install.sh"
PUBLIC_KEY=""
CONTAINER=0
SKIP_SYSTEM_CHECK=0
KEEP=0
ENGINE="${KITSUNE_CONTAINER_ENGINE:-}"

usage() {
  sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'
}

die() {
  printf 'install smoke: %s\n' "$*" >&2
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --artifacts)
      shift
      [[ $# -gt 0 ]] || die "--artifacts needs a directory"
      ARTIFACTS="$1"
      ;;
    --version)
      shift
      [[ $# -gt 0 ]] || die "--version needs a value"
      VERSION="$1"
      ;;
    --installer)
      shift
      [[ $# -gt 0 ]] || die "--installer needs a file"
      INSTALLER="$1"
      ;;
    --public-key)
      shift
      [[ $# -gt 0 ]] || die "--public-key needs a file"
      PUBLIC_KEY="$1"
      ;;
    --engine)
      shift
      [[ $# -gt 0 ]] || die "--engine needs a value"
      ENGINE="$1"
      ;;
    --container) CONTAINER=1 ;;
    --skip-system-check) SKIP_SYSTEM_CHECK=1 ;;
    --keep) KEEP=1 ;;
    -h | --help)
      usage
      exit 0
      ;;
    *) die "unknown argument: $1" ;;
  esac
  shift
done

[[ -n "$ARTIFACTS" ]] || ARTIFACTS="$REPO_ROOT/dist"
[[ -n "$VERSION" ]] || VERSION="$(tr -d '[:space:]' < "$REPO_ROOT/VERSION")"
[[ -d "$ARTIFACTS" ]] || die "artifact directory not found: $ARTIFACTS"
ARTIFACTS="$(cd "$ARTIFACTS" && pwd)"
[[ -f "$INSTALLER" ]] || die "installer not found: $INSTALLER"
INSTALLER="$(cd "$(dirname "$INSTALLER")" && pwd)/$(basename "$INSTALLER")"
[[ -n "$PUBLIC_KEY" ]] || PUBLIC_KEY="$ARTIFACTS/ci-public-key.pem"
[[ -f "$PUBLIC_KEY" ]] || die "public key not found: $PUBLIC_KEY"
PUBLIC_KEY="$(cd "$(dirname "$PUBLIC_KEY")" && pwd)/$(basename "$PUBLIC_KEY")"

for asset in \
  checksums.txt \
  checksums.txt.sig \
  "kitsune-client_linux_amd64.tar.gz" \
  "kitsune-server_linux_amd64.tar.gz"; do
  [[ -f "$ARTIFACTS/$asset" ]] || die "missing artifact: $ARTIFACTS/$asset"
done

pick_engine() {
  if [[ -n "$ENGINE" ]]; then
    command -v "$ENGINE" >/dev/null 2>&1 || die "container engine not found: $ENGINE"
    return 0
  fi
  if command -v podman >/dev/null 2>&1; then
    ENGINE=podman
  elif command -v docker >/dev/null 2>&1; then
    ENGINE=docker
  else
    die "neither podman nor docker is available"
  fi
}

run_in_container() {
  pick_engine
  local key_dir key_name
  key_dir="$(cd "$(dirname "$PUBLIC_KEY")" && pwd)"
  key_name="$(basename "$PUBLIC_KEY")"
  # The installer itself must not need root. The entrypoint runs as root only
  # to install the shell tools the base image omits (openssl, libX11), then
  # drops to uid 65534 before the installer runs. Nothing under /etc, /usr or
  # /opt changes after the marker, and the assertion proves it.
  "$ENGINE" run --rm \
    --volume "$REPO_ROOT:/repo:ro,z" \
    --volume "$ARTIFACTS:/artifacts:ro,z" \
    --volume "$key_dir:/smoke-key:ro,z" \
    --workdir /repo \
    ubuntu:24.04 \
    bash -c '
      set -euo pipefail
      export DEBIAN_FRONTEND=noninteractive
      apt-get update -qq
      apt-get install -y -qq --no-install-recommends openssl libx11-6 ca-certificates >/dev/null
      mkdir -p /home/smoke
      chown 65534:65534 /home/smoke
      exec setpriv --reuid=65534 --regid=65534 --clear-groups \
        env HOME=/home/smoke /repo/scripts/ci-install-smoke.sh \
        --artifacts /artifacts --version "$1" --installer /repo/install.sh \
        --public-key "/smoke-key/$2"
    ' bash "$VERSION" "$key_name"
}

if [[ "$CONTAINER" == 1 ]]; then
  run_in_container
  exit $?
fi

SMOKE_ROOT="$(mktemp -d "${TMPDIR:-/tmp}/kitsune-install-smoke.XXXXXX")"
SMOKE_HOME="$SMOKE_ROOT/home"
SMOKE_CONFIG="$SMOKE_ROOT/config"
SMOKE_CACHE="$SMOKE_ROOT/cache"
SMOKE_STATE="$SMOKE_ROOT/state"
SMOKE_DATA="$SMOKE_ROOT/data"
SMOKE_RUNTIME="$SMOKE_ROOT/runtime"
FAKE_BIN="$SMOKE_ROOT/bin"
SMOKE_LOG="$SMOKE_ROOT/smoke.log"
SYSTEMCTL_LOG="$SMOKE_ROOT/systemctl.log"
LOGINCTL_LOG="$SMOKE_ROOT/loginctl.log"
FORBIDDEN_LOG="$SMOKE_ROOT/forbidden.log"
MARKER="$SMOKE_ROOT/marker"

mkdir -p \
  "$SMOKE_HOME" "$SMOKE_CONFIG" "$SMOKE_CACHE" "$SMOKE_STATE" \
  "$SMOKE_DATA" "$SMOKE_RUNTIME" "$FAKE_BIN"

cleanup() {
  local status=$?
  if [[ $status -ne 0 ]]; then
    printf '\ninstall smoke: kept %s for inspection\n' "$SMOKE_ROOT" >&2
    for log in "$SMOKE_ROOT"/*.log; do
      [[ -f "$log" ]] || continue
      printf '\n--- %s ---\n' "$(basename "$log")" >&2
      cat "$log" >&2 || true
    done
  elif [[ "$KEEP" == 0 ]]; then
    rm -rf "$SMOKE_ROOT"
  else
    printf 'install smoke: kept %s\n' "$SMOKE_ROOT"
  fi
  exit "$status"
}
trap cleanup EXIT

note() {
  printf 'install smoke: %s\n' "$*"
}

fail() {
  printf 'install smoke: FAIL: %s\n' "$*" >&2
  exit 1
}

require_file() {
  [[ -f "$1" ]] || fail "missing file: $1"
}

require_exec() {
  [[ -x "$1" ]] || fail "missing executable: $1"
}

require_absent() {
  [[ ! -e "$1" ]] || fail "unexpected path: $1"
}

require_contains() {
  grep -Fq -- "$2" "$1" || fail "$1 does not contain: $2"
}

# -- fake commands ---------------------------------------------------------
#
# systemctl is allowed to reload the user manager and to disable a unit during
# uninstall. Asking it to enable, start or restart anything fails the smoke.
cat > "$FAKE_BIN/systemctl" <<'EOF'
#!/bin/sh
printf 'systemctl %s\n' "$*" >> "$SMOKE_SYSTEMCTL_LOG"
case " $* " in
  *" enable "* | *" start "* | *" restart "*)
    printf 'systemctl was asked to enable/start: %s\n' "$*" >&2
    exit 91
    ;;
esac
exit 0
EOF
cat > "$FAKE_BIN/loginctl" <<'EOF'
#!/bin/sh
printf 'loginctl %s\n' "$*" >> "$SMOKE_LOGINCTL_LOG"
exit 0
EOF
for name in sudo pkexec udevadm modprobe apt-get dnf pacman zypper apk; do
  cat > "$FAKE_BIN/$name" <<'EOF'
#!/bin/sh
printf '%s %s\n' "${0##*/}" "$*" >> "$SMOKE_FORBIDDEN_LOG"
printf 'forbidden command was invoked: %s\n' "${0##*/}" >&2
exit 90
EOF
done
chmod 0755 "$FAKE_BIN"/*

touch "$MARKER"

run_installer() {
  env \
    HOME="$SMOKE_HOME" \
    XDG_CONFIG_HOME="$SMOKE_CONFIG" \
    XDG_CACHE_HOME="$SMOKE_CACHE" \
    XDG_STATE_HOME="$SMOKE_STATE" \
    XDG_DATA_HOME="$SMOKE_DATA" \
    XDG_RUNTIME_DIR="$SMOKE_RUNTIME" \
    PATH="$FAKE_BIN:$PATH" \
    KITSUNE_RELEASE_PUBLIC_KEY="$PUBLIC_KEY" \
    SMOKE_SYSTEMCTL_LOG="$SYSTEMCTL_LOG" \
    SMOKE_LOGINCTL_LOG="$LOGINCTL_LOG" \
    SMOKE_FORBIDDEN_LOG="$FORBIDDEN_LOG" \
    bash "$INSTALLER" "$@"
}

CLIENT="$SMOKE_HOME/.local/bin/kitsune-client"
SERVER_LAUNCHER="$SMOKE_HOME/.local/bin/kitsune-server"
SERVER_ROOT="$SMOKE_HOME/.local/lib/kitsune-whisper/server"
CONFIG="$SMOKE_CONFIG/kitsune-whisper/kitsune.yaml"
MANIFEST="$SMOKE_STATE/kitsune-whisper/install.json"
DESKTOP_ENTRY="$SMOKE_DATA/applications/kitsune-whisper.desktop"
SVG_ICON="$SMOKE_DATA/icons/hicolor/scalable/apps/io.github.FoxRed-cmd.kitsune-whisper.svg"
PNG_ICON="$SMOKE_DATA/icons/hicolor/256x256/apps/io.github.FoxRed-cmd.kitsune-whisper.png"
SERVER_UNIT="$SMOKE_CONFIG/systemd/user/kitsune-server.service"
CLIENT_UNIT="$SMOKE_CONFIG/systemd/user/kitsune-client.service"
MODELS="$SMOKE_CACHE/kitsune-whisper/models"
PAIRING="$SMOKE_STATE/kitsune-whisper/pairing.json"
TLS_CERT="$SMOKE_STATE/kitsune-whisper/tls/server.crt"

assert_no_forbidden() {
  [[ ! -s "$FORBIDDEN_LOG" ]] || fail "a forbidden command ran: $(tr '\n' '; ' < "$FORBIDDEN_LOG")"
  [[ ! -s "$LOGINCTL_LOG" ]] || fail "loginctl was invoked: $(tr '\n' '; ' < "$LOGINCTL_LOG")"
  if grep -Eq '(^| )enable( |$)|(^| )start( |$)|(^| )restart( |$)' "$SYSTEMCTL_LOG"; then
    fail "systemctl was asked to enable/start/restart: $(tr '\n' '; ' < "$SYSTEMCTL_LOG")"
  fi
}

assert_tree() {
  require_exec "$CLIENT"
  require_exec "$SERVER_LAUNCHER"
  require_exec "$SERVER_ROOT/runtime/bin/python3"
  require_file "$SERVER_ROOT/launcher.py"
  require_file "$SERVER_ROOT/bootstrap.py"
  require_file "$SERVER_ROOT/bundle.json"
  require_file "$CONFIG"
  require_file "$MANIFEST"
  require_file "$DESKTOP_ENTRY"
  require_file "$SVG_ICON"
  require_file "$PNG_ICON"
  require_file "$SERVER_UNIT"
  require_absent "$CLIENT_UNIT"
  require_contains "$MANIFEST" '"version": "'"$VERSION"'"'
  require_contains "$MANIFEST" '"client"'
  require_contains "$MANIFEST" '"server"'
  require_contains "$DESKTOP_ENTRY" "Exec=$CLIENT gui"
}

assert_versions() {
  local client_version server_version
  client_version="$("$CLIENT" --version 2>&1)" ||
    fail "the installed Client could not print its version"
  [[ "$client_version" == "kitsune-client $VERSION" ]] ||
    fail "Client version is '$client_version', want 'kitsune-client $VERSION'"
  server_version="$("$SERVER_LAUNCHER" --version 2>&1)" ||
    fail "the installed Server launcher could not print its version"
  [[ "$server_version" == "kitsune-server $VERSION" ]] ||
    fail "Server version is '$server_version', want 'kitsune-server $VERSION'"
  note "installed binaries report $client_version and $server_version"
}

note "installing $VERSION from $ARTIFACTS"
if ! run_installer --from "$ARTIFACTS" --version "$VERSION" > "$SMOKE_ROOT/install.log" 2>&1; then
  cat "$SMOKE_ROOT/install.log" >&2
  fail "install.sh failed"
fi
require_contains "$SMOKE_ROOT/install.log" "Verified the release signature"
assert_tree
assert_versions
assert_no_forbidden

# --check is read-only and reports the installed components.
cp "$MANIFEST" "$SMOKE_ROOT/manifest.before-check"
if ! run_installer --check --from "$ARTIFACTS" --version "$VERSION" > "$SMOKE_ROOT/check.log" 2>&1; then
  cat "$SMOKE_ROOT/check.log" >&2
  fail "install.sh --check failed"
fi
require_contains "$SMOKE_ROOT/check.log" "Installed: $VERSION"
require_contains "$SMOKE_ROOT/check.log" "Components: client server"
require_contains "$SMOKE_ROOT/check.log" "Available: $VERSION"
cmp -s "$MANIFEST" "$SMOKE_ROOT/manifest.before-check" || fail "--check changed the install manifest"

# Re-running upgrades in place and keeps the user's configuration.
printf '\n# smoke-kept\n' >> "$CONFIG"
mkdir -p "$MODELS/tiny"
printf 'model\n' > "$MODELS/tiny/model.bin"
mkdir -p "$(dirname "$PAIRING")"
printf '{"device":"smoke"}\n' > "$PAIRING"
if ! run_installer --from "$ARTIFACTS" --version "$VERSION" > "$SMOKE_ROOT/reinstall.log" 2>&1; then
  cat "$SMOKE_ROOT/reinstall.log" >&2
  fail "reinstall failed"
fi
require_contains "$CONFIG" "# smoke-kept"
require_file "$MODELS/tiny/model.bin"
require_file "$PAIRING"
assert_tree

# Uninstall keeps config, models, pairing and Server data.
mkdir -p "$(dirname "$TLS_CERT")"
printf 'certificate\n' > "$TLS_CERT"
if ! run_installer --uninstall > "$SMOKE_ROOT/uninstall.log" 2>&1; then
  cat "$SMOKE_ROOT/uninstall.log" >&2
  fail "uninstall failed"
fi
require_absent "$CLIENT"
require_absent "$SERVER_LAUNCHER"
require_absent "$SERVER_ROOT"
require_absent "$MANIFEST"
require_absent "$DESKTOP_ENTRY"
require_absent "$SVG_ICON"
require_absent "$PNG_ICON"
require_absent "$SERVER_UNIT"
require_file "$CONFIG"
require_file "$MODELS/tiny/model.bin"
require_file "$PAIRING"
require_file "$TLS_CERT"
assert_no_forbidden

# Purge removes every trace, including the data uninstall kept.
if ! run_installer --from "$ARTIFACTS" --version "$VERSION" > "$SMOKE_ROOT/reinstall-for-purge.log" 2>&1; then
  cat "$SMOKE_ROOT/reinstall-for-purge.log" >&2
  fail "reinstall before purge failed"
fi
mkdir -p "$MODELS/tiny" "$(dirname "$PAIRING")" "$(dirname "$TLS_CERT")"
printf 'model\n' > "$MODELS/tiny/model.bin"
printf '{"device":"smoke"}\n' > "$PAIRING"
printf 'certificate\n' > "$TLS_CERT"
if ! run_installer --uninstall --purge > "$SMOKE_ROOT/purge.log" 2>&1; then
  cat "$SMOKE_ROOT/purge.log" >&2
  fail "purge failed"
fi
require_absent "$CLIENT"
require_absent "$SERVER_LAUNCHER"
require_absent "$SERVER_ROOT"
require_absent "$MANIFEST"
require_absent "$SMOKE_CONFIG/kitsune-whisper"
require_absent "$SMOKE_CACHE/kitsune-whisper"
require_absent "$SMOKE_STATE/kitsune-whisper"
require_absent "$SMOKE_HOME/.local/lib/kitsune-whisper"
require_absent "$SMOKE_DATA/io.github.FoxRed-cmd.kitsune-whisper"
assert_no_forbidden

if [[ "$SKIP_SYSTEM_CHECK" == 0 ]]; then
  changed="$(find /etc /usr /opt -xdev -newer "$MARKER" -print 2>/dev/null || true)"
  [[ -z "$changed" ]] || fail "the installer wrote outside the home directory: $(printf '%s' "$changed" | head -n 20 | tr '\n' ' ')"
fi

note "PASS: install, versions, check, reinstall, uninstall and purge behaved as documented"
