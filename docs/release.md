# Release process

A release is a Git tag `v<major>.<minor>.<patch>` whose version matches the
root `VERSION` file and the Server package. `.github/workflows/release.yml` then
builds every artifact natively and publishes it with `checksums.txt` and its
detached signature `checksums.txt.sig`.

The current release line is **1.2.2**.

## Version sources

| Where | Value | Checked by |
| --- | --- | --- |
| `VERSION` | `1.2.2` | release CI `verify-version` |
| `server/pyproject.toml` | `1.2.2` | `verify-version` and `test_packaging.py` |
| `server/kitsune_server/__init__.py` | `1.2.2` | `verify-version` and `test_packaging.py` |
| Git tag | `v1.2.2` | `verify-version` |

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
git tag -a v1.2.2 -m "kitsune-whisper 1.2.2"
git push origin v1.2.2
```

Pushing a tag is the release action: it starts `release.yml` and
`publish-server-image.yml`. Do not move or force-push a published tag; publish a
new patch version instead.

## What CI builds

`release.yml` runs natively on Linux and Windows because the Client uses cgo.
The `verify-version` job runs first, `verify-docs` gates publication on the
installer/document drift check, and every build job depends on `verify-version`.

| Job | Artifact |
| --- | --- |
| `verify-docs` | none; fails the release when installer checks and `dependencies.md` disagree |
| `client-linux` | `kitsune-client_linux_amd64.tar.gz` (GTK4 + WebKitGTK 6.0) |
| `client-windows` | `kitsune-client_windows_amd64.zip` |
| `server-bundle` | `kitsune-server_linux_amd64.tar.gz` |
| `server-bundle-windows` | `kitsune-server_windows_amd64.tar.gz` |
| `release` | `install.sh`, `install.ps1`, `checksums.txt`, `checksums.txt.sig`, all of the above |

The installers consume the GTK4 + WebKitGTK 6.0 Client archive. A host without
those libraries still installs and runs headless, with Settings in the browser.

The two installers are the only installation artifacts. They download the
Client and Server archives from the same release and verify them against the
signed checksum file. No distribution package or desktop bundle is produced.

## Installer smoke jobs

PR CI proves the published artifact set still installs, not just that it builds:

- `install-smoke-linux` builds the Client and Server archives, signs
  `checksums.txt` with an ephemeral ECDSA P-256 key, and runs
  `scripts/ci-install-smoke.sh` inside a clean `ubuntu:24.04` container as a
  non-root user. The script asserts the installed tree, the Client and Server
  `--version` output, `--check`, an in-place reinstall, uninstall and purge, and
  that no `sudo`, package manager, `udev`, `systemctl enable/start`, or `loginctl`
  call happened. It also fails if anything under `/etc`, `/usr` or `/opt`
  changed.
- `install-smoke-windows` builds the Client zip and Server bundle, signs the
  checksums with .NET `ECDsa`, and runs `scripts/ci-install-smoke.ps1` with an
  isolated `LOCALAPPDATA`/`APPDATA`/`USERPROFILE`. It asserts the install tree,
  Apps & Features entry, user PATH entry, a disabled Server logon trigger, the
  installed binaries' versions, and the uninstall/purge boundaries.

Both jobs use throwaway keys and the `KITSUNE_RELEASE_PUBLIC_KEY` test seam.
They never touch the production `RELEASE_SIGNING_KEY`. A drift between the
identifiers in `install.sh` / `install.ps1` and the table in
[`dependencies.md`](dependencies.md#what-the-installers-check) fails the normal
`server` test job and the release's `verify-docs` job.

### What each archive contains

| Artifact | Contents |
| --- | --- |
| `kitsune-client_linux_amd64.tar.gz` | `kitsune-client`, `kitsune.example.yaml` |
| `kitsune-client_windows_amd64.zip` | `kitsune-client.exe`, `kitsune.example.yaml` |
| `kitsune-server_linux_amd64.tar.gz` | `runtime/`, `lib/`, `bootstrap.py`, `bundle.json` |
| `kitsune-server_windows_amd64.tar.gz` | `runtime/` (`python.exe`, `pythonw.exe`), `lib/`, `bootstrap.py`, `bundle.json` |

## Checksums and signature

`checksums.txt` is generated in the release job with `sha256sum` over every
`kitsune-*` archive plus `install.sh` and `install.ps1`. Both installers verify
the signature over `checksums.txt` before trusting any digest, and every archive
against that file before extraction. The published artifact set is unchanged: a
staged update gets `VERSION` from the verified release tag when the release does
not attach one.

The release job then signs `checksums.txt` with the offline release key and
uploads the detached signature next to it. A release without a signature is
never published.

### Signing

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
matches the committed public key, or when signing fails.

The job and the local helper (`scripts/sign-release-checksums.sh`) both verify
the new signature against the committed public key immediately after signing,
so a wrong or rotated secret cannot ship a signature that users cannot check.

### Verify with OpenSSL

```sh
curl -fsSLO https://github.com/FoxRed-cmd/kitsune-whisper/releases/download/v1.2.2/checksums.txt
curl -fsSLO https://github.com/FoxRed-cmd/kitsune-whisper/releases/download/v1.2.2/checksums.txt.sig
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
explicitly. It builds the Linux Client and the Server bundle (when
`KITSUNE_SERVER_RUNTIME`, `KITSUNE_SERVER_SITE_PACKAGES`, and
`KITSUNE_SERVER_PYTHON` point at a resolved runtime), then writes
`checksums.txt`. The builder can produce a Client-only or Server-only set when
asked; release CI uses its own selective jobs.

A manual publish must sign the same way CI does and upload the signature with
the checksums:

```sh
scripts/sign-release-checksums.sh \
  --key /secure/offline/release-signing-key.pem --checksums dist/checksums.txt
gh release upload v1.2.2 dist/checksums.txt dist/checksums.txt.sig --clobber
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

The Python suite includes the drift guard that keeps the dependency
identifiers and glibc floors in
[`dependencies.md`](dependencies.md#what-the-installers-check) exactly equal to
the `DEP_*`, `$Dep*`, and `GLIBC_*_FLOOR` values in `install.sh` and
`install.ps1`.

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
- [ ] A clean Linux install runs `install.sh` with no `sudo`, no package-manager
      call, no udev rule, and no enabled or started unit; `--dry-run` and
      `--check` change nothing.
- [ ] A clean Windows install runs `install.ps1` without administrator rights,
      creates no startable logon task, and appears in Apps & Features.
- [ ] The installers place exactly the selected Components; the Client starts
      an installed Local Server on launch; autostart is off until switched on.
- [ ] The first-run wizard downloads the model with progress; the Server starts
      without a model and downloads nothing on its own.
- [ ] `checksums.txt` lists every published asset and both installer scripts.
- [ ] `checksums.txt.sig` verifies against the committed public key with
      OpenSSL, and the release is not published if it does not.
- [ ] Uninstall keeps configuration, models, and pairing state; purge removes
      every trace we created and no package the user installed; a user-added
      udev rule is documented as a manual removal.
- [ ] `docs/dependencies.md` lists every package and injection tool for the
      supported distributions and both platforms.
- [ ] Documentation is current: install, dependencies, configuration,
      pairing/revocation, LAN exposure, models, updates, and troubleshooting.
- [ ] Docker smoke (optional, opt-in) passes for a packaging change.

## Rollback

- **Client or Server:** reinstall the previous release. The installer keeps
  `kitsune.yaml`, models, Devices, and TLS, and `migrate-config` is additive.
  On Linux a lower version needs `--force`; the Windows installer does not
  block it.
- **Docker:** pin the previous `vX.Y.Z-cpu` / `vX.Y.Z-gpu` image tag.
- **A bad tag:** do not move it. Ship a patch release. Delete a draft release
  only if nothing was published.
