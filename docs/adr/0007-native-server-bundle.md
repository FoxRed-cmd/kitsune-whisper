# The Server ships as a native per-user bundle; Docker is the dedicated-host alternative

**Status:** accepted

The Server has only ever shipped as a Docker image (`cpu` and `gpu` profiles).
Docker Desktop on Windows needs WSL2, a reboot, and (in organizations) a
licence; `nvidia-container-toolkit` on Linux is a separate privileged install.
Neither is acceptable for "one command installs the Client and Server on the
laptop". At the same time the Client already installs per-user without
elevation, and every Server dependency (`ctranslate2`, `av`, `onnxruntime`,
`numpy`) publishes `linux/amd64` and `win_amd64` wheels.

The primary install is therefore a **native per-user bundle**: a pinned Python
runtime plus the Server package under the user's lib directory, run as an
OS-supervised background process. The documentation offers an optional CUDA path
on top of that runtime; the installer itself downloads nothing. Whisper weights
download separately through the Model catalog. This replaces the single static
`small` default from ADR 0003 with a per-device recommendation in that catalog;
GPU is still recommended and CPU still supported. The Docker images stay as the
supported deployment for a dedicated host (for example the GPU desktop) and
expose the same API, auth, and config as the native Server.

> **The earlier Linux `.deb`/`.rpm` amendment is superseded by
> [ADR 0013](0013-scripts-only-distribution.md):** distribution packages are
> removed, so the per-user bundle is the only native install on both platforms;
> configuration, state and models stay per user.

## Considered options

- **(a) Chosen — native bundle by default, Docker kept.** No elevation on
  Windows, no WSL2, no container runtime for the laptop; the GPU host can still
  use the existing images.
- **(b) Docker mandatory everywhere.** Rejected: the installer would have to
  install and configure Docker Desktop / `nvidia-container-toolkit`, which is
  heavier and more fragile than the app itself.
- **(c) Two equal choices at install time.** Rejected: doubles the install
  matrix and the test surface for no user benefit; Docker remains available
  without being the default.

## Consequences

- The installer payload grows to roughly 200 MB; CUDA wheels (~0.7 GB) and
  models are downloaded on demand with checksum verification.
- The Server needs a supervisor on every platform: a systemd user unit or a
  per-user Task Scheduler task, with restart-on-failure. Restart is implemented
  as a supervised self-exit, and "stop" is a Soft-stop (unload the model,
  refuse transcription) so it also works inside a container.
  **Amended by [ADR 0012](0012-local-server-hard-lifecycle.md):** the Client
  hard-stops a Local Server profile and keeps the Soft-stop for a Remote Server.
- Packaging tests move from compose-only assertions to bundle layout plus the
  existing compose checks.
- Linux gets CUDA through the `nvidia` wheels and the existing
  `ensure_cuda_libraries`; the host still needs a driver.
- macOS is out of scope, but a standalone runtime bundle does not block it.
