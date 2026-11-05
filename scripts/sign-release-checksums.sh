#!/usr/bin/env bash
# Sign a release's checksums.txt with the offline ECDSA P-256 release key.
#
#   scripts/sign-release-checksums.sh --key <private.pem> --checksums dist/checksums.txt
#
# Writes a detached DER-encoded ECDSA/SHA-256 signature next to the checksum
# file (dist/checksums.txt.sig by default) and verifies it against the committed
# public key before exiting. Release CI passes the key as a file created from
# the RELEASE_SIGNING_KEY secret; the key never appears on the command line.
#
# Options:
#   --key FILE          PEM private key (required)
#   --checksums FILE    checksum file to sign (default: dist/checksums.txt)
#   --signature FILE    output path (default: <checksums>.sig)
#   --public-key FILE   public key to verify against
#                       (default: packaging/keys/kitsune-release-public.pem)
#   -h, --help
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
KEY_FILE=""
CHECKSUMS="$REPO_ROOT/dist/checksums.txt"
SIGNATURE=""
PUBLIC_KEY="$REPO_ROOT/packaging/keys/kitsune-release-public.pem"

usage() {
  sed -n '2,19p' "$0" | sed 's/^# \{0,1\}//'
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --key)
      shift; [[ $# -gt 0 ]] || { echo "error: --key needs a file" >&2; exit 2; }
      KEY_FILE="$1" ;;
    --checksums)
      shift; [[ $# -gt 0 ]] || { echo "error: --checksums needs a file" >&2; exit 2; }
      CHECKSUMS="$1" ;;
    --signature)
      shift; [[ $# -gt 0 ]] || { echo "error: --signature needs a file" >&2; exit 2; }
      SIGNATURE="$1" ;;
    --public-key)
      shift; [[ $# -gt 0 ]] || { echo "error: --public-key needs a file" >&2; exit 2; }
      PUBLIC_KEY="$1" ;;
    -h|--help)
      usage
      exit 0 ;;
    *)
      echo "error: unknown argument: $1" >&2
      exit 2 ;;
  esac
  shift
done

[[ -n "$KEY_FILE" ]] || { echo "error: --key is required" >&2; exit 2; }
[[ -f "$KEY_FILE" ]] || { echo "error: private key not found: $KEY_FILE" >&2; exit 1; }
[[ -f "$CHECKSUMS" ]] || { echo "error: checksum file not found: $CHECKSUMS" >&2; exit 1; }
[[ -f "$PUBLIC_KEY" ]] || { echo "error: public key not found: $PUBLIC_KEY" >&2; exit 1; }

if [[ -z "$SIGNATURE" ]]; then
  SIGNATURE="$CHECKSUMS.sig"
fi

tmp_signature="$(mktemp "${TMPDIR:-/tmp}/kitsune-checksums.sig.XXXXXX")"
trap 'rm -f "$tmp_signature"' EXIT

# OpenSSL emits a DER ECDSA-Sig-Value here, which is the format .NET reads
# with DSASignatureFormat.Rfc3279DerSequence.
openssl dgst -sha256 -sign "$KEY_FILE" -out "$tmp_signature" "$CHECKSUMS"

# A secret for the wrong key (or a corrupted signature) must fail the release
# now, not after the assets are public.
if ! openssl dgst -sha256 -verify "$PUBLIC_KEY" -signature "$tmp_signature" "$CHECKSUMS" >/dev/null; then
  echo "error: the signature does not verify against $PUBLIC_KEY" >&2
  exit 1
fi

mv "$tmp_signature" "$SIGNATURE"
trap - EXIT
chmod 644 "$SIGNATURE"
echo "Signed $CHECKSUMS -> $SIGNATURE"
