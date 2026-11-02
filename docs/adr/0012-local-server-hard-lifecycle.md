# The Client hard-stops a Local Server and soft-stops a Remote Server

**Status:** accepted

ADR 0007 made Soft-stop the uniform "stop" for native and Docker Servers so one
Admin API works everywhere. That leaves a native Local Server process resident
after Stop, holding runtime memory, and makes Start reload a model inside an
already running daemon. The user asked for the local machine's daemon to behave
like an ordinary background service: Stop terminates it, Start launches it, and
Restart replaces the process, while a Remote Server keeps the soft-stop that a
Client can safely perform over the network.

The Client therefore chooses the lifecycle by **Server profile**. A profile is
local when it points at a loopback address and is the Local Server profile
(`auto_pair`, or the stable `local` id). For that profile:

- Stop calls `POST /admin/hard-stop`; the Server clears any persisted soft-stop,
  answers `202`, and exits with code `0`, so `Restart=on-failure` and Task
  Scheduler leave it stopped. If the process is already down, the Client runs the
  OS supervisor's stop instead.
- Start runs the OS supervisor (`systemctl --user start` or `schtasks /Run`)
  when the process is down; when the process is already up it clears a soft-stop
  through `POST /admin/start`.
- Restart calls `POST /admin/hard-restart`, which clears the soft-stop and
  exits with code `75`, so the supervisor replaces the process with a
  model-loaded one. A restart requires an installed supervisor because the
  Client cannot bring an unsupervised process back.

A Remote Server uses the existing `/admin/stop`, `/admin/start`, and
`/admin/restart` soft lifecycle unchanged.

## Considered options

- **(a) Chosen — hard lifecycle for the Local Server, soft for Remote.** The
  Client has OS access only to the local machine, and the user gets the expected
  "stop the daemon" behaviour where it can actually work.
- **(b) Keep soft-stop everywhere.** Rejected: it does not release the daemon's
  runtime memory and cannot be described as stopping the process.
- **(c) Hard endpoints callable from the LAN.** Rejected: a paired remote Client
  must not be able to terminate or restart the Server host. The endpoints are
  authenticated *and* loopback-only.

## Consequences

- `/admin/hard-stop` and `/admin/hard-restart` are local-only Admin API surface;
  a non-loopback peer receives `403 local_only`.
- The Client falls back to the OS supervisor when a Server predates the hard
  endpoints (`404`) or the connection drops, so a mixed-version install still
  stops.
- Start and Restart need the Server's supervisor registration. Without it the
  Client reports an actionable error instead of leaving the daemon down.
- Login autostart stays a separate Startup switch. Stopping the Server does not
  disable its unit or task, so it may return at the next login unless the user
  turns Server autostart off.
- Docker deployments are Remote Server profiles and keep the soft-stop flag on
  the state volume.
