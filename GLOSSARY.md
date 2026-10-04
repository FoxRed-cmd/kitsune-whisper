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

## Flow

**Utterance**:
A single recorded stretch of speech sent to the server as one request.
_Avoid_: clip, recording, buffer, sample

**Transcription**:
The text the server returns for an utterance.
_Avoid_: result, output, transcript

**Injection**:
Placing a transcription into the currently focused input field.
_Avoid_: paste, insertion, typing

**Dictation cycle**:
One complete trigger-to-injection cycle: start recording, stop, request, and inject a single utterance.
_Avoid_: session, job, task

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

**Spool**:
Locally saved audio of an utterance whose transcription failed, kept so the speech is not lost.
_Avoid_: cache, queue, retry store
