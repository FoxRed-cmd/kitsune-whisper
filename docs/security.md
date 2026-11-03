# Security, trust, and pairing

The Server holds a microphone's worth of audio and decides which machines may
send it. This page is the operator's view of that boundary: TLS, Device tokens,
pairing, LAN exposure, and revocation. The configuration keys are in
[configuration.md](configuration.md); the commands are in
[install.md](install.md).

## Threat model

kitsune-whisper assumes a trusted LAN and untrusted neighbours on it. It does
not try to protect against a compromised user account on the Server host, a
malicious kernel, or an attacker who can read process memory. Multi-user
isolation on a shared machine is out of scope (see ADR 0008). The boundaries it
does enforce:

- **No open transcription service.** `/transcribe`, `/status`, and the Admin API
  require a Device token unless the operator explicitly sets
  `server.allow_anonymous: true`. A laptop install binds loopback only.
- **No cleartext on the LAN.** TLS is on by default and the Client pins the
  Server's certificate per profile. Audio and Device tokens never travel in
  clear text between paired machines.
- **No password to steal.** A Device token is a long-lived bearer credential
  stored in the OS keychain (with a `0600` file fallback). Revoking the Device
  invalidates it immediately.
- **No implicit routing.** Every utterance goes to the active Server profile.
  A failure is reported and spooled; the Client never retries another Server.

## TLS identity

On first start the Server generates a self-signed certificate and key under its
state directory:

| Platform | Default location |
| --- | --- |
| Linux | `~/.local/state/kitsune-whisper/tls/` |
| Windows | `%LOCALAPPDATA%\kitsune-whisper\tls\` |
| Docker | `/state/tls/` on the `kitsune-state` volume |

The SHA-256 fingerprint of the leaf certificate is logged at startup, returned
by every pairing response, and shown by `kitsune-server pair`. It is public
identity data, not a secret.

The Client pins the fingerprint in the Server profile. An HTTPS profile without
a pin is refused **before** the Device token is sent. A certificate that changed
outside the normal uninstall/reinstall path produces a distinct "certificate
changed, pair again" error and a **Pair again** action in Settings; the Client
does not silently trust the new certificate.

To regenerate the identity deliberately, delete the `tls/` directory and pair
every Device again. Deleting a Docker volume does the same. Treat that as a
security event: old tokens still exist on the old certificate unless the Device
store was removed too.

`server.tls.enabled: false` is an explicit insecure opt-out for a laboratory or
a reverse proxy that terminates TLS. Pairing over plain HTTP accepts any peer;
do not use it on a LAN you do not control.

## Device tokens

Every pairing returns a token with the `ktn_` prefix. The Server stores only its
hash, plus the Device record (name, creation time, last seen, last address). The
Client stores the plaintext token:

- in Windows Credential Manager on Windows;
- in the Secret Service (GNOME Keyring, KWallet, KeePassXC) on Linux;
- in a `0600` file under the user config directory when no keychain is
  available (`~/.config/kitsune-whisper/tokens/` by default).

Tokens never enter `kitsune.yaml`, a status snapshot, a log line, or an error
message. Diagnostics redact them. Removing a profile forgets its token; revoking
a Device on the Server makes the token unusable even if a copy remains on disk.

## Pairing

### Local Server (same machine)

The Local Server profile is created automatically with `auto_pair: true`. On
startup the Client calls the loopback-only `POST /pair/local` endpoint, pins the
certificate, and stores the token. A fresh install can dictate without a code.

`/pair/local` refuses any peer that is not a genuine loopback address. A Docker
port forward makes the peer the bridge gateway, so a Docker Server cannot use
local auto-pairing; use a Pairing code instead.

### Remote Server (another machine)

1. On the Server host, run `kitsune-server pair`, or use **Settings → Servers
   and Devices → Generate code**. In Docker:
   `docker exec <container> kitsune-server pair`.
2. The command prints the config file it read, an 8-character code, its expiry,
   and the certificate fingerprint.
3. On the Client, choose **Add Remote Server**, enter `https://<server>:8765`,
   the code, and a Device name, then press **Pair**.
4. The Client pins the certificate and stores the token; the new profile becomes
   active.

A Pairing code is single use, valid for 10 minutes, and rate-limited. Wrong,
expired, reused, and rate-limited codes return distinct errors
(`pairing_code_wrong`, `pairing_code_expired`, `pairing_code_reused`,
`pairing_rate_limited`). The unauthenticated pairing endpoint exists only for
this exchange; it does not expose status or transcription.

## Revocation

List and revoke Devices on the Server host:

```sh
kitsune-server devices list
kitsune-server devices revoke <ID_OR_NAME>
```

The next request from a revoked Device receives `401 unauthorized` with a
`WWW-Authenticate: Bearer` header, even while the Server keeps running: the
store notices the file changed and reloads it. In Docker run the same command
inside the container; the store lives on the `kitsune-state` volume.

Settings lists the same Devices and offers **Revoke**. The Client's own Device
is marked **This Device** and has no Revoke button while the Local Server
profile is active, because its loopback auto-pairing has no code to repeat.
Manage that Device from the Server host instead. Revoking the last Device always
requires access to the Server host.

Re-pairing after revocation is the same flow as a first pairing: generate a new
code and redeem it. The old token is not reactivated.

## LAN exposure

The Server binds `127.0.0.1` by default. Exposing it is a deliberate, separate
step because it turns a laptop install into a reachable service:

```sh
kitsune-server expose on --yes   # writes expose_to_network: true
kitsune-server expose off        # back to loopback
```

The change is written to the Server YAML and takes effect only after a restart;
the running listener is never rebound behind the user's back. Settings offers
the same switch with a confirmation, and the Admin API route is
`POST /admin/exposure` with `{"expose": true, "confirm": true}`.

Before exposing a Server:

- keep TLS on and leave `allow_anonymous` off;
- allow port 8765 only from the networks that need it, for example with
  `ufw allow from 192.168.1.0/24 to any port 8765 proto tcp`;
- pair only the machines you recognise and revoke the rest;
- remember that `/health` is intentionally unauthenticated, so a port scanner
  can confirm the Server is alive.

`/health` reports liveness and the release version only. It never reveals the
model, device, Devices, or configuration.

## Docker

The image uses the same code, API, TLS, Device store, and pairing flow as the
native bundle. Two container-specific points:

- Pairing codes come from `kitsune-server pair` **inside** the container, so an
  unauthenticated code endpoint is never exposed.
- The certificate, Device store, pairing codes, and soft-stop flag live on the
  `kitsune-state` volume. Recreating a container with the same volume keeps the
  identity and the Devices; `docker compose down -v` destroys them and forces
  every Client to pair again.

## Hardening checklist

- [ ] `server.expose_to_network` is `false` unless another machine really needs
      this Server.
- [ ] `server.tls.enabled` is `true` and every profile shows a fingerprint.
- [ ] `server.allow_anonymous` is `false`.
- [ ] A host firewall limits port 8765 to the LAN.
- [ ] `kitsune-server devices list` contains only machines you recognise.
- [ ] Retired or lost laptops are revoked, not merely removed from the Client.
- [ ] The Client and the Server are on compatible releases; a minor-version
      mismatch is reported as an upgrade error, not as an auth failure.
- [ ] Docker state volumes are backed up if losing Device trust would be costly.

## See also

- [Configuration reference](configuration.md) — `server.tls`,
  `server.allow_anonymous`, `server.expose_to_network`.
- [Troubleshooting](troubleshooting.md) — 401, revoked, certificate changed.
- [Docker parity smoke](verification/docker-smoke.md) — the automated pairing,
  revocation, soft-stop, and restart contract.
