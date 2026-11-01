# A Server trusts Devices through a pairing code and long-lived Device tokens

**Status:** accepted

The Client and Server now talk across the LAN, and the Admin API can switch
models, download weights, restart the Server, and revoke access. A single
static token copied by hand gives no per-device revocation, and an admin
password would have to be entered on every new machine unless a refresh-token
flow is built anyway. The user asked for an explicit allow-list of devices and
for a session that survives reboots.

Trust is rooted in **local access**. A Local Server pairs the Client on the same
machine automatically: same user on the same host is enough. A Remote Server
issues a one-time **Pairing code** (valid for 10 minutes) from its client UI, or
from `kitsune-server pair` inside a container. The Client sends the address and
the code and receives a long-lived **Device token**; the Server stores only the
token hash and a **Device** record (name, last seen) that can be **Revoked**.
There is no account and no password. Transport is TLS with a self-signed
certificate generated at first Server start; the fingerprint is shown together
with the pairing code and pinned by the Client. Tokens are kept in the OS
keychain (Windows Credential Manager / DPAPI, Linux Secret Service) with a
`0600` file fallback.

## Considered options

- **(a) Chosen — pairing code, per-device token, TLS pinning.** No password to
  remember, one-time physical trust, revocable devices, and a long-lived session.
- **(b) Admin account with password.** Rejected as the only mechanism: it adds a
  credential to remember and a reset path, and still needs a long-lived refresh
  token to satisfy "do not log in on every boot".
- **(c) One shared static token.** Rejected: no device list, no revocation, and
  the whole trust boundary leaks if one config file leaks.
- **(d) mTLS client certificates.** Rejected: powerful, but certificate
  issuance and renewal are far more work than the threat model of a home LAN.

## Consequences

- Both the Admin API and `/transcribe` require a Device token; a Server can
  explicitly opt out with `allow_anonymous` for trusted setups.
- Revocation is the recovery path; there is no password reset. Revoking the last
  Device is done on the Server host.
- Regenerating the Server certificate breaks pinning, so every Device must pair
  again.
- Multi-user machines are not fully isolated in v1: any local user can reach the
  loopback Admin API, though they still need a Device token. A Unix socket or
  named pipe with peer-credential checks is the future fix.
- Docker Servers pair through `kitsune-server pair` inside the container, since
  the published port cannot prove loopback origin.
- Mixed versions are unsupported: a new Client rejects an old Server with an
  upgrade message, and an old Client cannot authenticate to a new Server.
