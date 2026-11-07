# Updates stage a verified artifact set and install it through a hidden helper

**Status:** accepted

The previous update flow downloaded only the installer script, checked its
SHA-256, started it as a separate process, and considered the work done. Nothing
showed progress, the installer left a running Client and Server in place on
Linux, and the Server was stopped without being started again. The old Client
kept running its old binary, so the first launch after an update still reported
the old version.

An **Update** is now a complete, user-triggered action with an observable end
state. The Client checks GitHub Releases once about 60 seconds after startup
when `client.updates.check` is on, and on demand. When a newer installable
release exists, it downloads the full set needed by the Components installed on
this machine into a private cache and verifies every file against the release
checksums. This is the **Staged update**. The staged set is reused after a
Client restart, replaced when a newer release appears, and deleted after a
successful install. A manual check never downloads; the **Update to vX…**
action downloads when necessary and then installs.

The set is deliberately complete: the installer, `checksums.txt`,
`checksums.txt.sig`, `VERSION`, the Client archive, and the Server archive only
when the Local Server Component is installed. The Client verifies each SHA-256
as the file streams. The installer keeps verifying the detached signature over
`checksums.txt`, so the trust chain is unchanged. The installed Component set,
not the active Server profile, selects the archives: a Remote Server is never
touched, and a Client-only machine passes the installer's client-only flag.

The install order is fixed:

1. Re-verify every staged file against `checksums.txt`.
2. Record the installed Components and whether the Local Server is running.
3. Stop the Local Server through its **local** profile (the stable local
   profile or the auto-pairing loopback profile), never through the active
   profile, so a Remote Server is not stopped while it is active.
4. Wait for the current Dictation cycle to finish.
5. Write the pending marker to the result file.
6. Generate the platform helper inside the stage and start it hidden.
7. Only after the helper started, exit the Client with code `0`, releasing the
   single-instance lock.
8. The helper waits for the Client to exit and runs the installer from the
   stage: `--from` / `-From` plus `--version` / `-Version`, and
   `--client-only` / `-ClientOnly` when the Server is not installed.
9. The helper writes the result atomically, then starts the installed Client
   with `gui` and the same `--config` when one was given. It never uses
   `--hidden`.
10. The restarted Client shows the outcome. A successful update starts the
    Local Server again only when it ran before; a partial failure leaves it
    stopped.

The helper is a generated Bash or PowerShell script, not the Client binary.
This is required because the Windows installer force-stops `kitsune-client`
processes while replacing the binary; a helper running inside the Client would
be killed before it could write a result or relaunch anything. The helper also
inherits a 15-minute limit and always starts the Client again after an
installer failure or timeout, so a failed update cannot leave the user without
an application.

The result is persisted until the user dismisses it, so closing the window does
not lose the outcome; the next launch shows it again. A failure offers
**Details** (the last ~50 lines of the update log and its path) and **Retry**.
Retry clears the old result and checks, downloads, and installs again in one
action. The statuses are `success`, `partial`, `error`, `timeout`, and
`pending`; `pending` means the helper never wrote a final result.

**ADR 0013 remains in force.** The installers themselves still start and enable
nothing: they do not enable a unit, run a scheduled task, or register
autostart. The helper is what runs the installer, and the helper and the
restarted Client — not the installer — start the Components afterwards. This
keeps the distribution promise that a manual re-run of `install.sh` /
`install.ps1` installs files and stops, while an in-app Update still comes back
with a running Client.

## Considered options

- **(a) Chosen — stage a full verified set, then install it through a hidden
  helper.** The user sees download progress before the Client closes, the
  installer never needs the network, every file is verified twice, and a
  separate process survives the Windows force-stop long enough to report the
  result and relaunch the Client.
- **(b) Install from the Client process.** Rejected: the Windows installer
  terminates `kitsune-client`, so the Client cannot write a result or restart
  itself; on Linux a running Client also holds the old binary and the
  single-instance lock.
- **(c) Let the installer download the artifacts again.** Rejected: it
  duplicates the download, hides progress, makes the timeline unpredictable,
  and gives the Client no verified set to reuse after a restart.
- **(d) Run the installer in a visible terminal.** Rejected: an Update is a
  product action, not a manual shell task. A terminal window exposes
  implementation details, can be closed by the user mid-install, and looks like
  a failure on Windows.
- **(e) Install automatically after a check without the user's click.**
  Rejected: the setting controls network access and staging, not permission to
  change the installed version. An Update must be requested.

## Consequences

- The Client has an explicit update state machine: not checked, checking, up to
  date, available, downloading, ready, installing, and the persisted outcome.
  The Tray renders the same states.
- The update path depends on the platform's installer toolchain: `openssl`,
  `tar`, `sha256sum`, and `awk` on Linux, and `pwsh.exe` on Windows. Missing
  tools surface as a helper or installer error with a log, not as a silent
  failure.
- The staged set occupies the user cache until a successful install or a newer
  release replaces it. It is private to the user and is never installed from
  another user's directory.
- A partial failure intentionally leaves the Local Server stopped rather than
  running a Client and Server from different releases; the retry path is the
  way forward.
- The installers and the release format do not change. `VERSION` becomes part
  of the staged set so the Client can record what it installs, but the release
  workflow already publishes it for the installer.
