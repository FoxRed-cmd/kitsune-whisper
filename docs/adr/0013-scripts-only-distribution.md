# Distribution is two install scripts, one per platform; packages are removed

**Status:** accepted

kitsune-whisper has no installed base. The release pipeline nevertheless shipped
five installation mechanisms: a Debian package, an RPM, an AppImage, an Inno
Setup executable, and the two one-liner scripts. The packages duplicated what
the scripts did, encoded runtime dependencies in metadata that could not be kept
accurate, and carried behaviour the product should not have: a root-owned system
install, a `udev` rule and a `ydotoold` daemon shipped by us, and two services
enabled and started during installation. A user could not tell which artefact
to take, what would be written, or what would start running on the machine.

The distribution is therefore **exactly one install script per platform**:

- Linux: `curl -fsSL …/install.sh | bash`
- Windows: `irm …/install.ps1 | iex`

The published artefact set is `kitsune-client_linux_amd64.tar.gz`,
`kitsune-client_windows_amd64.zip`, `kitsune-server_linux_amd64.tar.gz`,
`kitsune-server_windows_amd64.tar.gz`, `install.sh`, `install.ps1`,
`checksums.txt`, and a detached signature over it. The Debian, RPM, AppImage and
Inno Setup pipelines, their CI jobs, their tests and their documentation are
removed; nothing in the documentation offers a package to install.

The installers are per-user and non-privileged. They write binaries, desktop
entries, shortcuts and a configuration template under the user's own profile,
never into system directories, and never call `sudo`, `pkexec` or any other
elevation. They install no `udev` rule, no `modules-load.d` entry, no Linux PATH
drop-in and no `ydotoold` unit. Dependencies are not installed either: the
installers check the runtime surface and warn, but the README (quick reference)
and a dedicated dependencies document (full matrix) own the exact commands and
packages per distribution and display server. Missing runtime libraries do not
fail an install whose binaries were written; the exit status stays zero.

`--client-only` and `--server-only` choose which **Components** are installed;
with neither flag both are. The Client derives the installed set from the Server
bundle and its supervisor registration instead of probing arbitrary paths, and
offers only Autostart modes whose Components are present. The installer writes
an install manifest with the version, the Components and the timestamp.
Re-running upgrades in place; a lower version is refused unless the user forces
it; a check mode reports installed versus available.

Nothing starts or is enabled at install time. The installers never enable or
start a unit, never run a scheduled task, and never register autostart. The
Local Server's supervisor registration is still created because both on-demand
start and later autostart need it, but it is created disabled: on Linux the unit
file is written and `daemon-reload` is run without `enable`; on Windows the task
is created with its logon trigger explicitly disabled, so it never fires by
itself while `schtasks /Run` still works. That disabled supervisor registration
is not an **Autostart registration**; it does not start anything at login.
Starting the Local Server is the Client's job: on launch, when a Local Server is
installed and the current mode permits it, the Client starts it. In Client only
mode the Client starts no Local Server at launch, because the user chose to run
only the Client. Autostart is a single off-by-default switch with three modes —
Client + Server, Client only, Server only. Turning it on
writes and enables the corresponding autostart entry; turning it off removes
that entry rather than leaving a disabled login entry behind. The Server's
supervisor registration may remain in place so that on-demand start still works,
but while autostart is off it is not an **Autostart registration**.

Nothing is downloaded at install time or when the Server starts. The first-run
wizard downloads the model on explicit request, and a Server without a model
still starts, reports no active model, and refuses transcription until one is
loaded.

**No bundled text-injection tools.** `ydotool`, `ydotoold` and `wtype` are no
longer shipped, extracted, linked or supervised. The Client keeps its existing
behaviour of shelling out to `wtype`, then `ydotool`, and to `wl-copy`/`wl-paste`
for the clipboard; X11 injection remains internal (XTEST) and needs no external
tool. The documentation explains which tool each display server needs, the two
supported ways to grant `/dev/uinput` access (a `uaccess` rule, or an
`input`-group rule plus group membership), the exact command to remove a rule
the user added, and keybinding examples for compositors without a global
shortcut portal. Bundling was rejected because a bundled `ydotoold` needs a
privileged device-permission change and a supervised daemon, while these tools
are already available from distributions or upstream and only need to be on
`PATH`.

**Integrity.** `checksums.txt` is detached-signed with ECDSA P-256 over SHA-256
digests. Linux verifies with `openssl` and Windows with the .NET `ECDsa`
implementation; both scripts pin the same public key, which is the only key
material committed. A missing or invalid signature aborts the install unless the
user passes an explicit insecure override intended for local development. The
private key lives in a CI secret with an offline backup; losing it means
shipping a new key in a new installer version.

This ADR supersedes the earlier Linux `.deb`/`.rpm` amendment in
[ADR 0007](0007-native-server-bundle.md) and the bundled-helpers amendment in
[ADR 0010](0010-embedded-settings-window.md). Those amendments stay in the
record as history; the decisions they changed now read as described here.
Removing distribution packages also removes the reason for a second Linux
desktop build, so the single GTK4 + WebKitGTK 6.0 build and the browser-settings
fallback are recorded in ADR 0010.

## Considered options

- **(a) Chosen — two scripts, per-user, nothing installed beyond our files.**
  One path per platform, no elevation, no package-manager matrix, and the user
  can read exactly what changes before running it.
- **(b) Keep packages alongside the scripts.** Rejected: two support surfaces,
  duplicated install logic, dependency metadata that drifts from the supported
  distributions, and a root-owned install that contradicts the per-user model.
- **(c) Packages only.** Rejected: requires elevation, cannot cover
  distributions without a packager, and has no Windows equivalent short of
  another Inno Setup executable.
- **(d) Bundle the injection helpers.** Rejected: forces a privileged `udev`
  change and a daemon we supervise, and `wtype` only works on wlroots anyway.
- **(e) Install dependencies at install time.** Rejected: encodes a distribution
  matrix in the installer, needs elevation, and goes stale faster than a
  documented command the user can adapt.

## Consequences

- The README and the dependencies document become part of the product: the
  installer only warns, so the commands and the per-session injection table must
  stay correct. A test asserts that the dependency identifiers the installer
  checks match the ones the dependencies document lists.
- There is no package-manager upgrade path; the install manifest and the
  installer's check, upgrade and forced-downgrade modes are the only bookkeeping.
- Uninstall removes our binaries, units, desktop entries and shortcuts while
  keeping configuration, models and pairing state; purge removes configuration,
  models, certificates, pairing state, logs and tokens. Neither removes a package
  the user installed, and removing a user-added `udev` rule stays manual.
- A missing GTK4/WebKitGTK 6.0 degrades Settings to the browser page; missing
  ALSA or X11 is reported before it turns into a loader error or a recording
  failure. Wayland injection without an external tool reaches the clipboard only.
- The Windows build remains unsigned and SmartScreen is documented rather than
  bypassed.
- Installer tests assert externally observable behaviour: the resulting tree per
  Component flag, the absence of privileged commands, `udev` writes, enabled
  units, started tasks and autostart registration, signature success and
  failure, dependency warnings, and the uninstall/purge boundary.
