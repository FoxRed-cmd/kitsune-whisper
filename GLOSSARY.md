# kitsune-whisper

Local, LAN-hosted speech-to-text dictation: a client on the user's machine captures speech and injects the resulting text into the focused field, while a server runs Whisper and returns transcriptions.

## Language

## Participants

**Client**:
The local process on the user's machine that captures speech, requests transcription, and injects text.
_Avoid_: agent, app, frontend

**Server**:
The process that runs Whisper and returns transcriptions for received audio.
_Avoid_: backend, service, daemon

## Deployment and control

**Local Server**:
The Server that runs on the same machine as the Client.
_Avoid_: local daemon, embedded server, fallback server

**Remote Server**:
A Server reached over the network from another machine.
_Avoid_: external server, main server, cloud server

**Server profile**:
A named set of connection settings for one Server that the Client can route utterances to.
_Avoid_: endpoint, connection, backend, host

**Server switch**:
The user's manual choice of which Server profile the Client routes utterances to.
_Avoid_: fallback, failover, rotation

**Soft-stop**:
Stopping a Server's processing — unloading the model and refusing transcription — without terminating the process.
_Avoid_: pause, suspend, shutdown

**Model catalog**:
The set of Whisper model sizes the Server can download and run.
_Avoid_: model list, model store

**Pairing code**:
A short-lived one-time code that authorises one Device to use a Server profile.
_Avoid_: token, invite, PIN

**Auto-pair**:
Trusting the Client on the same machine as the Local Server without a Pairing
code. Marked per Server profile so the Client knows it may pair silently.
_Avoid_: auto-connect, local mode

**Device**:
A Client installation trusted by a Server profile, identified by a name and a stored Device token.
_Avoid_: client, machine, node

**Device token**:
The long-lived credential a Device presents to its Server.
_Avoid_: API key, password, session

**Revoke**:
Removing a Device's trust so its Device token stops working.
_Avoid_: unpair, logout, delete

**Admin API**:
The Server's authenticated interface through which the Client reads status, changes settings, manages models and Devices, and restarts or stops the Server.
_Avoid_: REST API, control API

**Tray**:
The desktop notification-area icon and its menu through which the user controls and monitors the Client.
_Avoid_: systray, tray icon, applet

**Settings window**:
The Client's embedded window in which the user views and changes Client and Server settings.
_Avoid_: preferences, config UI, admin panel

**Setup wizard**:
The Client's skippable first-run flow that configures the microphone, hotkey, hardware, model, and Server profile, and ends with one real Dictation cycle.
_Avoid_: onboarding, tutorial, tour

## Flow

**Utterance**:
A single recorded stretch of speech sent to the server as one request.
_Avoid_: clip, recording, buffer, sample

**Transcription**:
The unprocessed text produced from an utterance's audio.
_Avoid_: result, output, transcript

**Injection**:
Placing a transcription into the currently focused input field.
_Avoid_: paste, insertion, typing

**Dictation cycle**:
One complete trigger-to-injection cycle: start recording, stop, request, and inject a single utterance.
_Avoid_: session, job, task

## Text processing

**Refine**:
Removing disfluencies and explicit self-corrections from a transcription and adding punctuation and casing, without paraphrasing or dropping substantive content.
_Avoid_: clean, cleanup, polish, normalize

**Summarize**:
Compressing a transcription to its key points, dropping detail.
_Avoid_: condense, shorten, abstract

**Delivered text**:
The text injected after any requested processing, as opposed to the transcription.
_Avoid_: result, output

## Triggering

**External trigger**:
A dictation trigger delivered not by a global hotkey but by an external command over the client's local control socket, used where no global hotkey is available.
_Avoid_: manual trigger, CLI trigger

**Session backend**:
The mechanism the client uses for global hotkeys and text injection, chosen from the detected desktop session type (X11 or Wayland).
_Avoid_: platform, mode

## Feedback and failure

**Earcon**:
A short synthesized tone that signals client state.
_Avoid_: beep, sound, notification

**Recording indicator**:
The on-screen element that shows the current phase of the Dictation cycle and the Input level.
_Avoid_: overlay, HUD, OSD, widget, island

**Input level**:
How loudly the user is speaking into the microphone, shown live by the Recording indicator.
_Avoid_: volume, amplitude, VU meter

**Spool**:
Locally saved audio of an utterance whose transcription failed, kept so the speech is not lost.
_Avoid_: cache, queue, retry store
