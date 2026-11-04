# Linux installer live check

A manual, repeatable check of the all-in-one Linux installer
(`install.sh`). Run it on a disposable machine or with a backup of your user
configuration; it installs user units and one system udev rule.

## What it proves

- The installer verifies `checksums.txt` before touching the system.
- It adds **kitsune-whisper** to the application menu with the fox icon, and
  the entry opens Settings.
- It asks for `sudo` at most once: to install the selected GTK/WebKitGTK stack,
  the `/dev/uinput` `uaccess` rule, and the `modules-load.d` entry.
- A second run detects those pieces and does not ask again.
- The `uaccess` ACL appears for the logged-in user without a group change or a
  re-login, and the bundled `ydotoold` user unit starts at
  `$XDG_RUNTIME_DIR/.ydotool_socket`.
- The bundled `wtype`/`ydotool` path is on the Client unit's `PATH`.
- `kitsune-client.service`, `kitsune-server.service`, and
  `kitsune-ydotoold.service` are active.
- The local Server answers `/health`, auto-pairs a loopback Client, and
  transcribes speech.
- A real Dictation cycle records from the microphone, transcribes on the local
  Server, and injects the text through the bundled `ydotool` on GNOME Wayland.
- Stopping the Client service returns in under a second.

## Run

```sh
git checkout <the release tag or branch>
cd /path/to/kitsune-whisper
./install.sh --from dist          # or the curl one-liner
```

The one privileged step should print the package list and the rule path. Once it
finishes:

```sh
systemctl --user is-active kitsune-client kitsune-server kitsune-ydotoold
getfacl /dev/uinput | grep "user:$USER"
ls -l "$XDG_RUNTIME_DIR/.ydotool_socket"
ls -l ~/.local/share/applications/kitsune-whisper.desktop \
      ~/.local/share/icons/hicolor/scalable/apps/io.github.FoxRed-cmd.kitsune-whisper.svg
ydotool type "installer check"     # focused text field, then remove the text
curl -sk https://127.0.0.1:8765/health
```

Open the application menu and launch **kitsune-whisper**: Settings should appear
with the fox icon in the window list and the Tray.

Re-running `./install.sh --from dist` should not prompt for a password.

## Dictation cycle

Use **Settings → Servers and Devices** (or the Setup wizard) to make the Local
Server profile active, then press the hotkey and speak. On GNOME 50+ the log
should show the portal session:

```
hotkey: portal session /org/freedesktop/portal/desktop/session/... bound to CTRL+SHIFT+space / Escape
```

and a completed cycle:

```
dictation cycle: started
dictation cycle: processing
dictation cycle: delivering
inject: pasted; previous clipboard restored
dictation cycle: finished
```

If the portal is denied, bind `kitsune-client toggle` in **Settings → Keyboard →
Custom Shortcuts** (or the compositor config); the Client logs the fallback and
keeps dictating.

## Recorded result

Host: Fedora 44, GNOME Shell 50.6, Wayland, 2026-11-01, release artifacts built
from the `feature/65-desktop-install` tree.

| Check | Result |
| --- | --- |
| One `sudo` prompt, re-run does not prompt | pass |
| `/dev/uinput` ACL for the user, no re-login | pass |
| `kitsune-ydotoold.service` socket in `$XDG_RUNTIME_DIR` | pass |
| Local Server `/health` and auto-paired `/transcribe` | pass |
| GTK4 Settings window/Tray process | pass |
| GlobalShortcuts portal session bound | pass (after the host-registry fix) |
| Speech → Server → `ydotool` injection into a focused GTK field | pass: “Hello world, this is Kitsune with the dictation test.” |
| `systemctl --user stop kitsune-client` | pass, 0 s |

The same run also exercised `transcribe-file`, which returned
“Hello world, this is Kitsune with the meditation test.” from an `espeak-ng`
WAV; the misheard word is a model accuracy note, not a pipeline failure.
