# The health probe switches away from an unreachable Server profile

**Status:** accepted

[ADR 0009](0009-manual-server-switch.md) rejected per-utterance fallback: a
failed request is not retried on another Server profile, because that could
send the same audio to two machines, add a timeout to every phrase, and make
routing hard to predict. That rule still stands.

A background health probe may now change the active **Server profile** when the
active profile is unreachable and another profile answers. The switch is
persisted as the user's selection, applies immediately to the running Client,
and only affects the next Utterance. When no alternative answers, the active
profile stays selected and is marked unreachable on the Servers screen.

## Considered options

- **(a) Chosen — switch on an offline health result.** The next Utterance is
  routed to a Server that is known to answer, without retrying anything that
  already failed. The health probe already visits every profile and is the
  natural place to notice the outage.
- **(b) Retry the failed Utterance on another profile.** Still rejected for the
  reasons in ADR 0009: duplicate audio, duplicate injection, and unpredictable
  latency.
- **(c) Keep the manual-only switch.** Rejected because a configured spare
  Server should be useful without the user opening the Tray during an outage.
- **(d) Switch on any non-online result, including auth and certificate
  failures.** Rejected: those profiles are reachable, and moving away hides the
  specific repair action the Servers screen offers.

## Consequences

- An active profile that stops answering is replaced by the first profile in
  configuration order whose health probe succeeds. Returning the selection to
  a recovered earlier profile stays manual, unless the newly active profile
  also becomes unreachable.
- At startup an active Local Server keeps its existing short pairing retry
  window before an offline result may switch away, so a normal launch race does
  not change the selection. A Client-initiated start or restart holds the
  switch until the lifecycle action settles.
- The manual Refresh only updates health labels; it never changes the
  selection. The background probe is the only automatic switch path.
- An authentication, certificate, or version failure does not trigger a
  switch; the Servers screen still shows that state, and repair is offered for
  auth and certificate failures.
- Unavailable profiles are labelled in their row. The health detail is not
  repeated as a page-wide banner on every probe.
- The settings file's `active_server` is rewritten when the switch happens, so
  the choice survives a restart.
