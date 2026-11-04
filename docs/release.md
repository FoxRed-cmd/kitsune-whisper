# Release process

A release is a Git tag `v<major>.<minor>.<patch>` whose version matches the
root `VERSION` file and the Server package. `.github/workflows/release.yml` then
builds every artifact natively and publishes it with `checksums.txt` and its
detached signature `checksums.txt.sig`.

The current release line is **1.0.0**.

## Version sources

| Where | Value | Checked by |
| --- | --- | --- |
| `VERSION` | `1.0.0` | release CI `verify-version` |
| `server/pyproject.toml` | `1.0.0` | `verify-version` and `test_packaging.py` |
| `server/kitsune_server/__init__.py` | `1.0.0` | `verify-version` and `test_packaging.py` |
| Git tag | `v1.0.0` | `verify-version` |

Release builds embed the tag in the Client (`-X .../internal/cli.Version`) and
in the Server bundle manifest. Local builds read `VERSION`; `git describe`
remains only a fallback for an older checkout. Do not bump one source without
the others.

The Client refuses a Server with a different **major** or **minor** version and
allows a patch difference. A local `dev` build skips the comparison. This is why
the version must be the tag, not an arbitrary commit string.

## Tagging

```sh
# 1. Bump VERSION, server/pyproject.toml, and __init__.py together.
# 2. Run the checks locally (see below).
# 3. Commit, then tag and push the tag.
git tag -a v1.0.0 -m "kitsune-whisper 1.0.0"
git push origin v1.0.0
```

Pushing a tag is the release action: it starts `release.yml` and
`publish-server-image.yml`. Do not move or force-push a published tag; publish a
new patch version instead.

## What CI builds

`release.yml` runs natively on Linux and Windows because the Client uses cgo.
The `verify-version` job runs first and every build job depends on it.

| Job | Artifact |
| --- | --- |
| `client-linux` | `kitsune-client_linux_amd64.tar.gz` (GTK4 + WebKitGTK 6.0) |
| | `kitsune-client_linux_amd64-legacy.tar.gz` (GTK3 + WebKit2GTK 4.1) |
| `client-windows` | `kitsune-client_windows_amd64.zip` |
| `server-bundle` | `kitsune-server_linux_amd64.tar.gz` |
| `server-bundle-windows` | `kitsune-server_windows_amd64.tar.gz` |
| `release` | `install.sh`, `install.ps1`, `checksums.txt`, `checksums.txt.sig`, all of the above |

`checksums.txt` is generated in the release job with `sha256sum` over every
`kitsune-*` asset plus the two installer scripts. The installers and the Client's
update check verify a download against it before running anything. `install.sh`
is the Linux updater's verified asset; `install.ps1` is the pinned Windows
bootstrap. The release job then signs `checksums.txt` with the offline release
key and uploads the detached signature next to it.

## Signing

Every release carries `checksums.txt.sig`: a detached ECDSA P-256 / SHA-256
signature over `checksums.txt`. OpenSSL writes it as a DER-encoded
`ECDSA-Sig-Value` sequence, so .NET verifies it with
`DSASignatureFormat.Rfc3279DerSequence`. Signing the checksum file rather than
each archive still covers every asset, because the checksum file lists all of
them.

The public key is committed at
[`packaging/keys/kitsune-release-public.pem`](../packaging/keys/kitsune-release-public.pem).
The private key exists only as the repository secret `RELEASE_SIGNING_KEY` and
as an offline backup; it is never committed. The release job fails before
uploading anything when the secret is absent, when it is not the key that
matches the committed public key, or when signing fails. No unsigned release is
published.

The job and the local helper (`scripts/sign-release-checksums.sh`) both verify
the new signature against the committed public key immediately after signing,
so a wrong or rotated secret cannot ship a signature that users cannot check.

### Verify with OpenSSL

```sh
curl -fsSLO https://github.com/FoxRed-cmd/kitsune-whisper/releases/download/v1.0.0/checksums.txt
curl -fsSLO https://github.com/FoxRed-cmd/kitsune-whisper/releases/download/v1.0.0/checksums.txt.sig
curl -fsSLO https://raw.githubusercontent.com/FoxRed-cmd/kitsune-whisper/main/packaging/keys/kitsune-release-public.pem
openssl dgst -sha256 -verify kitsune-release-public.pem \
  -signature checksums.txt.sig checksums.txt
```

A valid signature prints `Verified OK`. OpenSSL's output is a DER sequence; do
not ask it for the IEEE P1363 form.

### Verify with .NET ECDsa on Windows

PowerShell 7 (the version `install.ps1` requires):

```powershell
$key = [System.Security.Cryptography.ECDsa]::Create()
$key.ImportFromPem((Get-Content -Raw .\kitsune-release-public.pem))
$valid = $key.VerifyData(
    [System.IO.File]::ReadAllBytes('checksums.txt'),
    [System.IO.File]::ReadAllBytes('checksums.txt.sig'),
    [System.Security.Cryptography.HashAlgorithmName]::SHA256,
    [System.Security.Cryptography.DSASignatureFormat]::Rfc3279DerSequence)
if (-not $valid) { throw 'checksums.txt signature does not verify' }
```

`Rfc3279DerSequence` is required: the default
`IeeeP1363FixedFieldConcatenation` expects a raw 64-byte `r || s` pair and
rejects OpenSSL's DER signature.

### Private key storage and rotation

- Add or replace the secret from the private key file without printing it:
  `gh secret set RELEASE_SIGNING_KEY < /secure/offline/release-signing-key.pem`.
  Only maintainers with repository admin access can do this.
- Keep two encrypted offline backups of the key, separate from the CI account.
  Do not paste it into an issue, a chat, a commit, or a build log.
- To rotate, generate a new P-256 keypair, commit the new public key, update the
  secret, and cut a release. Each release installer pins the public key that was
  current when it was built, so an old installer keeps verifying old releases
  with the key in its own tag; moving forward requires the new release's
  installer.
- **Losing the key means no further releases can be signed with the pinned
  key.** There is no recovery. Ship a new public key with a new installer
  version (a minor release is enough), and tell users to fetch that installer
  over a channel they already trust. Never publish an unsigned release as a
  fallback.

Docker images are published separately by `publish-server-image.yml`:
`ghcr.io/foxred-cmd/kitsune-whisper-server:cpu` and `:gpu`, with
`vX.Y.Z-cpu` / `vX.Y.Z-gpu` tags on a release.

## Local build and dry run

The same artifact names and layout can be produced from a checkout:

```sh
scripts/build-linux-artifacts.sh --output dist
```

Without `--version` it reads the root `VERSION`; release CI passes the tag
explicitly. It builds the GTK4 and GTK3 Clients, the Server bundle (when
`KITSUNE_SERVER_RUNTIME`, `KITSUNE_SERVER_SITE_PACKAGES`, and
`KITSUNE_SERVER_PYTHON` point at a resolved runtime), then writes
`checksums.txt`. Use `--no-server` for a Client-only build.

A manual publish must sign the same way CI does and upload the signature with
the checksums:

```sh
scripts/sign-release-checksums.sh \
  --key /secure/offline/release-signing-key.pem --checksums dist/checksums.txt
gh release upload v1.0.0 dist/checksums.txt dist/checksums.txt.sig --clobber
```

Before tagging:

```sh
# Python
cd server && uv run ruff check . && uv run ruff format --check . \
  && uv run basedpyright && uv run pytest

# Go
cd client && gofmt -l . && go test ./... \
  && golangci-lint run --config ../.golangci.yml \
  && golangci-lint run --config ../.golangci.yml --build-tags "desktop purego"

# Live slices on a host with the model cached and a microphone optional
scripts/e2e-live.sh --profile local-cpu --report e2e-cpu.md
scripts/e2e-trust.sh --model small --report e2e-trust.md
```

## Release checklist

- [ ] `VERSION`, `server/pyproject.toml`, and `__init__.py` all say the new
      version.
- [ ] `verify-version` would accept the tag (`v` + `VERSION`).
- [ ] Server and Client test suites, lint, format, and typecheck pass.
- [ ] `scripts/e2e-live.sh` passes on a machine with a model: dictation, silence,
      unsupported media, unreachable Server.
- [ ] `scripts/e2e-trust.sh` passes: local pairing, remote pairing, revocation,
      manual switch, soft-stop/start, supervised restart, Spool recovery.
- [ ] The manual matrix in
      [verification/manual-matrix.md](verification/manual-matrix.md) is executed
      for GNOME Wayland, Windows, and a real CUDA host.
- [ ] The artifacts install and dictate on clean Windows and Linux machines.
- [ ] `checksums.txt` lists every published asset and both installer scripts.
- [ ] `checksums.txt.sig` verifies against the committed public key with
      OpenSSL, and the release is not published if it does not.
- [ ] Documentation is current: install, configuration, pairing/revocation, LAN
      exposure, models, updates, and troubleshooting.
- [ ] Docker smoke (optional, opt-in) passes for a packaging change.

## Rollback

- **Client or Server:** reinstall the previous release. The installer keeps
  `kitsune.yaml`, models, Devices, and TLS, and `migrate-config` is additive.
- **Docker:** pin the previous `vX.Y.Z-cpu` / `vX.Y.Z-gpu` image tag.
- **A bad tag:** do not move it. Ship a patch release. Delete a draft release
  only if nothing was published.
