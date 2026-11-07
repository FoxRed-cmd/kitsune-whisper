# Updates

An **Update** replaces the Components installed on this machine with a newer
release. Configuration, models, Devices, TLS certificates, and pairing state
are left in place. The Client checks GitHub Releases, stages a complete and
verified set of artifacts, and installs that staged set only when the user
presses **Update to vX…**. Nothing is installed automatically.

- [The update setting](#the-update-setting)
- [How an update proceeds](#how-an-update-proceeds)
- [What is staged](#what-is-staged)
- [Progress, cancel, and reuse](#progress-cancel-and-reuse)
- [How the install runs](#how-the-install-runs)
- [Results and retry](#results-and-retry)
- [Paths](#paths)
- [Requirements and risks](#requirements-and-risks)

## The update setting

The setting is `client.updates.check` in the `client:` section of
`kitsune.yaml`:

| Key | Type | Default | Environment |
| --- | --- | --- | --- |
| `client.updates.check` | bool | `true` | `KITSUNE_CLIENT_UPDATES__CHECK` |

It controls the **Automatic update check** and the background **Staged
update** that follows it. It is hot-applied: changing it in **Settings →
Updates** takes effect without restarting the Client.

- **On** — about 60 seconds after the Client starts, it checks GitHub Releases
  once. When a newer installable release exists, it stages the complete update
  in the background. There are no periodic checks while the Client keeps
  running.
- **Off** — the Client makes no automatic GitHub requests. This includes the
  startup check, the background download, and checks triggered by anything
  other than the user. A manual **Check for updates** in Settings and the same
  item in the Tray always work.
- Turning the setting **on** starts a check immediately rather than waiting for
  the startup delay. Turning it **off** cancels an in-flight automatic check or
  background download; a download the user started from the Updates screen is
  not cancelled.

A manual check only looks for a release. It does not download anything by
itself. The action button **Update to vX…** downloads the staged set when it is
not ready yet and then goes straight to the install step.

The Tray mirrors the same state. It shows a disabled checking or downloading
item while work is in flight, **Update to vX…** when a release is available, and
**Ready to update to vX…** once the set is staged. Choosing either update item
runs exactly the same install as the Settings button.

## How an update proceeds

1. **Check.** The Client reads the latest release from GitHub Releases and
   compares its tag with the running version.
2. **Stage.** When the release is newer and has an installer for this platform,
   the Client downloads the full artifact set into the user cache and verifies
   every file. This is the Staged update.
3. **Install on request.** Pressing **Update to vX…** re-verifies the staged
   files, stops the Local Server when it is running, finishes the current
   dictation, and starts a hidden helper.
4. **Quiet install.** The Client exits and the helper runs the installer from
   the stage with no terminal window. The helper writes the result and starts
   the Client again.
5. **Result.** The restarted Client opens Settings and shows whether the
   Client, and the Local Server when installed, were updated.

The install itself has no progress bar in the Client because the Client is
already closed. Download progress is the only progress UI; the result is shown
after the Client comes back.

### What is downloaded

The Client stages one complete set for the release tag:

| File | Purpose |
| --- | --- |
| `install.sh` or `install.ps1` | The release installer, run from the stage. |
| `checksums.txt` | SHA-256 of every release artifact. |
| `checksums.txt.sig` | Detached signature over `checksums.txt`. |
| `VERSION` | The release version. The Client writes it when the release does not attach it. |
| `kitsune-client_<platform>_<arch>.*` | The Client archive. |
| `kitsune-server_<platform>_<arch>.tar.gz` | The Local Server bundle, only when the Local Server Component is installed. |

The Client verifies each downloaded file against `checksums.txt` as it streams.
The signature over `checksums.txt` is checked by the installer, so the trust
chain is the same as a manual install. A checksum or signature failure aborts
the update before anything is installed.

The installed **Components** decide the set, not the active **Server profile**.
A machine with both Components stages and updates both. A Client-only machine
passes the installer's client-only flag and never installs a Server bundle. An
active **Remote Server** changes nothing: the Remote Server is never contacted
or updated, and a locally installed Local Server is still updated.

## Progress, cancel, and reuse

The download progress bar aggregates the whole set:

- a percentage and received/total bytes while every selected file has a known
  size;
- the name of the file currently being downloaded;
- an indeterminate bar when any size is unknown.

**Cancel download** stops the transfer and removes the partial files, so the
next attempt starts clean. A cancelled automatic download caused by turning the
setting off leaves the update as merely available.

A completed stage is reused after the Client restarts. A newer release replaces
the older stage, and a successful install clears the staged artifacts. The
stage lives in the user cache with private permissions; it is never installed
from another user's directory.

## How the install runs

Pressing **Update to vX…** performs these steps in order:

1. Re-read `checksums.txt` from the stage and verify every staged file again.
   The signature is still the installer's responsibility.
2. Read the installed Components and whether the Local Server is running.
3. Stop the Local Server through its **local** profile (the stable local
   profile, or the auto-pairing loopback profile). This happens even when a
   Remote Server profile is active, so the update can never stop a remote
   machine.
4. Let the current Dictation cycle finish. If it does not finish within two
   minutes, the Client logs that and continues so an update cannot hang.
5. Write the pending marker to the update result file.
6. Generate a small platform helper inside the stage and start it hidden, with
   no terminal.
7. Only after the helper started successfully, exit the Client with code `0`.
   The Client releases its single-instance lock first, so systemd and Task
   Scheduler do not restart it while the installer is replacing its files.
8. The helper waits up to five minutes for the old Client to exit, then runs
   the installer from the stage:
   - Linux: `bash install.sh --from <stage> --version <tag>`, adding
     `--client-only` when no Local Server is installed.
   - Windows: `pwsh.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass
     -File install.ps1 -From <stage> -Version <tag>`, adding `-ClientOnly` when
     no Local Server is installed.

   The helper writes the installer output to `update.log` and gives it 15
   minutes.
9. The helper writes the final result to `update.json` atomically, then starts
   the installed Client with `gui`. If the original run had `--config PATH`,
   the same flag is passed again. It never starts the Client with `--hidden`.
10. The restarted Client opens Settings and shows the outcome. On success it
    starts the Local Server again only when it was running before the update.
    A partial failure or a pending marker leaves the Local Server stopped
    rather than running an undefined combination; after an installer error or
    timeout, the normal Startup rules decide.

If the helper cannot be started, the Client does not exit. It stays open and
shows the error immediately, so a failed launch never leaves the user without
an application.

### Why a helper, not the Client

The Windows installer force-stops `kitsune-client` processes while it replaces
the binary. The helper therefore cannot be the Client binary: it is a generated
Bash or PowerShell script that survives after the Client exits. The Windows
installer also requires PowerShell 7, which is why the helper invokes
`pwsh.exe`. On both platforms the helper always starts the Client again, even
when the installer failed or timed out.

This does not change [ADR 0013](adr/0013-scripts-only-distribution.md): the
installers themselves still start and enable nothing. Starting the Client and
the Local Server after the install is the helper's and the Client's job.

## Results and retry

The helper persists one of these statuses:

| Status | Message | Meaning |
| --- | --- | --- |
| `success` | Updated to vX. | The installer exited `0`; the Client is on the new version. When the Local Server was selected it was updated too, and it is started again only if it ran before. |
| `partial` | Client updated to vX, but the local Server could not be updated. The local Server stays stopped. | The Client reports the new version but the installer failed and the Local Server was selected. The Local Server is deliberately left stopped. |
| `error` | Could not update to vX. | The installer failed, the Client was not updated, or the helper could not complete the install. |
| `timeout` | Could not update to vX: the installer timed out. | The helper killed the installer after 15 minutes and restarted the Client. |
| `pending` | The update did not complete. The Client was restored with the previous version. | The pending marker exists but no final result was written; the helper never finished. |

A success card also says whether the Local Server was updated and whether it
was started again or stays stopped. A failure card offers **Details** and
**Retry**.

- **Details** shows the last ~50 lines of the update log and the full log path.
  A missing or empty log still shows the path.
- **Retry** clears the old outcome, checks again, and downloads and installs
  again in one action. It is available after an error, timeout, or partial
  result, and after a failed download.
- **Dismiss** removes the result. Until it is dismissed, the result survives
  Client restarts and is shown on the next launch even if the window was closed
  before the user read it.

Background check and download failures never pop over the window. They stay in
the Updates card and the log. Only a manual check raises a message area, which
is appropriate because the user asked for it.

## Paths

| What | Linux | Windows |
| --- | --- | --- |
| Update result | `~/.config/kitsune-whisper/update.json` | `%APPDATA%\kitsune-whisper\update.json` |
| Update log | `~/.config/kitsune-whisper/update.log` | `%APPDATA%\kitsune-whisper\update.log` |
| Staged artifacts | `~/.cache/kitsune-whisper/updates/<tag>/` | `%LOCALAPPDATA%\kitsune-whisper\updates\<tag>\` |

`XDG_CONFIG_HOME` and `XDG_CACHE_HOME` move the Linux defaults. The staged
directory contains the artifacts above plus a `stage.json` manifest; it is
created `0700` and the artifacts are readable only by the user, with the
installer made executable. A custom `--config PATH` keeps
`update.json` and `update.log` next to that file instead of in the default
config directory. The helper passes the same `--config` to the relaunched
Client.

The update log is separate from the Client log so installer output cannot
rotate the user's diagnostics away.

## Requirements and risks

- **Linux** — the installer needs `openssl`, `tar`, `sha256sum`, and `awk` on
  `PATH`; the helper is a Bash script.
- **Windows** — the installer needs PowerShell 7 (`pwsh.exe`). The installer
  force-stops the Client, which is why the install runs in a separate helper
  and the Client exits before it starts.
- **Remote Server** — never updated by the Client. Update it on its own host,
  or pull a newer Docker image there.
- **Local Server** — stopped before its files are replaced. After a successful
  update it is restarted only when it ran before; a partial or pending result
  leaves it stopped.
- **Exit code** — the Client exits `0` before the installer runs so a service
  manager does not restart the old binary during the install.
- **Do not bypass verification.** `--insecure` / `-Insecure` skips the release
  signature and is only for locally signed development artifacts.

If an update fails, see [Troubleshooting → Updates](troubleshooting.md#updates).

## See also

- [Install, upgrade, and uninstall](install.md)
- [Configuration reference](configuration.md)
- [Troubleshooting](troubleshooting.md#updates)
- [Release process and artifacts](release.md)
- [ADR 0013: scripts-only distribution](adr/0013-scripts-only-distribution.md)
- [ADR 0015: staged updates and quiet install](adr/0015-staged-updates-and-quiet-install.md)
