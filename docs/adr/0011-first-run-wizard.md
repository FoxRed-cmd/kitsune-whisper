# The first-run Setup wizard is a skippable shell screen with local progress state

**Status:** accepted

A fresh desktop install has a configuration template but no working
microphone, hotkey, hardware choice, model, or Server connection. The Client
already has a service for every one of those concerns: `dictation` lists
microphones and levels, validates and saves hotkeys, and applies changes;
`servers` manages profiles, pairing, health, models, configuration, and
restarts; `trust` reports CUDA availability in the authenticated `/status`
body. The missing piece is an ordered, resumable flow and a place to remember
its progress.

The wizard is a screen in the existing embedded Settings window, rendered from
Go with the same JSON intent protocol as the rest of Settings (ADR 0010). A new
headless `client/internal/wizard` service owns a pure state machine and a small
`wizard.json` progress file next to the effective `kitsune.yaml`. The file is
UI state, not configuration: writes are atomic with `0600`, it is never read by
the Server, and a corrupt file is backed up and replaced with a fresh wizard
instead of failing the Client.

The flow has six skippable setup steps — welcome and status, microphone with a
live level, hotkey with conflict detection and trigger mode, GPU/CPU detection
and choice, model recommendation and download, and Server setup with the Local
profile plus optional Remote pairing — followed by a final "try it now" step.
Progress resumes after a restart, the whole flow can be skipped, and Settings
always offers it again. It opens automatically on a fresh install (no progress
file yet) or when the Client knows no model is active, at most once per
process, so closing it is respected; `gui --hidden` activates the state without
forcing the window open.

Side effects go through the existing services. The hardware step reads
`trust.Status.GPU` from `/status` and patches `server.device` through the Admin
API, restarting the Server only when the patch says a restart is required. The
model step uses the existing catalog, download, cancel, activate, and defer
operations. The Server step calls `servers.EnsureLocal` for the loopback-only
Local profile and `AddRemote` for an optional Remote pairing. The final step
sends real `cycle.Start`/`cycle.Stop` triggers through the runtime; it confirms
the result with the user and explains exactly which prerequisite is missing
instead of faking a cycle. No new privileged Server API is introduced.

## Considered options

- **(a) Chosen — an embedded shell screen plus a sibling progress file.** Reuses
  the Settings window boundary and every existing service; tests stay headless.
- **(b) Store progress in the `client:` YAML section.** Rejected: the
  configuration schema is strict, the file is shared with the Server, and
  transient UI state is not configuration.
- **(c) A separate wizard window.** Rejected: more Wails surface and it breaks
  ADR 0010's one-window decision.
- **(d) Server-side wizard state.** Rejected: progress belongs to one Client
  and Device, not to a Server other Clients may share.
- **(e) A mock or dry-run transcription for the final step.** Rejected: it
  would not confirm the injection path, which is the most fragile part of the
  cycle.

## Consequences

- `wizard.json` is a new user-state file; uninstall and purge must remove it
  alongside configuration and models.
- Existing users upgrading get one skippable offer on their first launch
  afterwards, because the progress file does not exist yet. Installers can
  pre-create the file to suppress even that.
- The wizard is another consumer of `servers.View` and `dictation.View`; a
  future model or profile field should consider whether the wizard needs it.
- The wizard package imports no `dictation`, `runtime`, `shell`, `mic`, or
  `hotkey` package, so its tests run without cgo, a display, audio hardware, a
  GPU, a network peer, or a real model download.
