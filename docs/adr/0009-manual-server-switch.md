# The Client switches Server profiles manually; there is no automatic fallback

**Status:** accepted

The original plan was automatic fallback: send to the Remote Server, and on
failure retry the same utterance on the Local Server. Automatic routing hides
which machine received the audio, needs a circuit breaker and duplicate-response
handling, and can silently add a long timeout to every phrase. The user asked
for the simpler contract: on failure, report the error and let the user switch
the active **Server profile** in the Tray or Settings.

Every utterance therefore goes to exactly one Server: the active profile. On a
connection error, a 5xx, or a timeout the Client reports that the Server
could not transcribe, offers a shortcut to Settings, and saves the audio to the
Spool. It never retries another profile on its own and never injects twice.

## Considered options

- **(a) Chosen — manual Server switch.** Predictable latency, one request per
  utterance, no surprise about which host saw the audio.
- **(b) Automatic ordered fallback with a circuit breaker.** Rejected for v1 as
  more client state and more ways to fail silently; it can return later as an
  opt-in because profiles are already ordered and the Spool preserves audio.
- **(c) Latency-based routing.** Rejected: unstable, and it would send audio to
  a machine the user did not choose.

## Consequences

- A Remote Server outage costs one failed utterance plus a manual switch, even
  though the Local Server is running.
- When autostart includes the Local Server, it starts at login with the model
  loaded (subject to the idle unload timeout), so switching to it is instant
  when it is warm.
- Health and status are still shown in the Tray and Settings; they inform the
  user but never change routing.
