# Linux installer live check

A manual, repeatable check of the Linux installer (`install.sh`) on a clean
machine. It proves the scripts-only contract: per-user files, no elevation, no
packages, no udev rule, and nothing enabled or started.

Run it on a disposable machine or with a backup of your user configuration.
Unlike the old installers, `install.sh` writes nothing outside your home
directory and `/tmp`.

## What it proves

- The installer verifies `checksums.txt.sig` against the pinned release key
  before trusting any archive checksum, and aborts on a bad or missing
  signature.
- `--dry-run` and `--check` change nothing.
- No `sudo`, `pkexec`, package manager, udev, or `modules-load.d` command runs.
- No systemd unit is enabled or started; no autostart registration is written.
- `--client-only`, `--server-only`, and the default install exactly the
  selected tree; `--client-only` creates no Server unit or bundle.
- The Client binary, menu entry, and icons are placed under `$HOME`; the Server
  bundle is registered disabled and is not started.
- The dependency warnings name the missing identifier and point at the README;
  a missing dependency does not change the exit status.
- Re-running upgrades in place and keeps `kitsune.yaml`, models, and pairing;
  `--check` reports installed versus available; a lower version is refused
  without `--force`.
- `--uninstall` keeps configuration, models, and pairing; `--purge` removes
  every trace we created; neither touches user-installed packages or a udev
  rule the tester added.
- A clean install does not include `ydotool`, `wtype`, a `ydotoold` unit, or a
  `/dev/uinput` rule.

## Run

```sh
git checkout <the release tag or branch>
cd /path/to/kitsune-whisper
./install.sh --from dist          # or the curl one-liner
```

Start from a home directory with no previous install. The installer must not
prompt for a password. Once it finishes:

```sh
# Nothing should exist yet: the installer starts nothing.
systemctl --user is-active kitsune-client kitsune-server  # inactive / unknown
ls -l ~/.local/bin/kitsune-client
ls -l ~/.local/share/applications/kitsune-whisper.desktop \
      ~/.local/share/icons/hicolor/scalable/apps/io.github.FoxRed-cmd.kitsune-whisper.svg
cat ~/.local/state/kitsune-whisper/install.json
systemctl --user is-enabled kitsune-server.service        # disabled or not found
```

If the host has GTK4/WebKitGTK, ALSA, and X11 installed, no dependency warning
should appear. To see one, temporarily run the installer with a fake missing
library in the automated suite rather than damaging the host.

Launch **kitsune-whisper** from the application menu: Settings should appear
with the fox icon and the Tray. The first-run wizard should offer the model
download with progress; nothing should have been downloaded before that.

## Component and bookkeeping checks

```sh
# A client-only run must leave no Server tree.
./install.sh --client-only --from dist
test ! -e ~/.local/lib/kitsune-whisper/server
test ! -e ~/.local/bin/kitsune-server

# A server-only run must leave no Client binary or config.
./install.sh --server-only --from dist
test ! -e ~/.local/bin/kitsune-client

# Check mode reports the manifest, components, and available version.
./install.sh --check

# Dry run changes nothing.
./install.sh --dry-run --server-only --from dist
```

A downgrade must be refused unless forced:

```sh
./install.sh --from <older-artifacts>          # error: refusing to downgrade
./install.sh --from <older-artifacts> --force  # allowed
```

## Uninstall and purge

```sh
# Keep config, models, certificates, Devices, logs, and tokens.
./install.sh --uninstall
test -f ~/.config/kitsune-whisper/kitsune.yaml
test -d ~/.cache/kitsune-whisper/models

# Remove everything we created.
./install.sh --uninstall --purge
test ! -e ~/.config/kitsune-whisper
test ! -e ~/.local/state/kitsune-whisper
test ! -e ~/.local/lib/kitsune-whisper
```

Confirm that the purge did not remove a package the tester installed, and that
a `/dev/uinput` rule the tester added is still present (the removal is manual;
see [dependencies.md](../dependencies.md#removing-a-rule-you-added)).

## Dictation cycle

After the wizard downloads a model, press the hotkey and speak. On GNOME
Wayland the log should show the portal session or the external-trigger
fallback:

```text
hotkey: portal session /org/freedesktop/portal/desktop/session/... bound to CTRL+SHIFT+space / Escape
```

and a completed cycle:

```text
dictation cycle: started
dictation cycle: processing
dictation cycle: delivering
inject: pasted; previous clipboard restored
dictation cycle: finished
```

Injection uses the `wtype` or `ydotool` the tester installed and configured, not
a bundled helper. If the portal is denied, bind `kitsune-client toggle` in
**Settings → Keyboard → Custom Shortcuts** (or the compositor config); the
Client logs the fallback and keeps dictating.

## Recorded result

| Date | Release | Host | Result |
| --- | --- | --- | --- |
| — | 1.0.0 | — | **Pending** for the scripts-only installer |

The 1.0.0 evidence for the earlier package/helper installer is historical and
does not apply to this contract. Record a new run before the release; a failure
opens a ticket and is not marked passed.
