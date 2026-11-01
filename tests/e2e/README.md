# Hosted E2E groundwork

This is a work in progress. The server job is a fixture probe, not a passing UI
journey. It runs a `devtrust` server from the checked-out commit, generates the
existing synthetic media outside the checkout, signs in through the development
hook, and scans the libraries before starting an optional child command. Only
the generated media is cached. State, session material and server logs remain in
an ephemeral private directory and are deleted after the child exits.

Two server targets are needed:

* **Per-run:** one ephemeral server on the same GitHub runner as each client.
  Use it for setup, local sign-in, profile selection, Home, browsing, search,
  playback, restrictions, settings and remote-control navigation. Web uses
  localhost; iOS and tvOS simulators use the same macOS host. Android reaches the
  host at `10.0.2.2`, the emulator's alias for host loopback. The dev sign-in
  hook checks the actual socket peer; the hosted Android pilot must verify that
  this connection satisfies the check before relying on it.
* **Persistent remote:** a separate test server on the approved `portico-runner`
  Contabo host, provisioned by the ops lane and independent of the demo. Use it
  for Portico Services sign-in and claiming, `portico.direct` and certificates,
  Enhanced and Standard remote access, and outside-network connections. These
  tests require the environment and test-account handoff; no credentials belong
  in code, caches, screenshots, video or logs.

Neither target is a replacement for the other. Never connect a client on one
GitHub runner to the disposable server on another. Never expose `devtrust` on
the persistent server. All test failures remain non-required CI signals.

## Per-run fixture and identity plan

Every disposable server uses local accounts only and makes no Portico Services
requests. Each run needs fresh owner and member credentials and a restricted
profile with a random PIN and age limit, delivered through the test harness,
never entered into a device or browser. The development hook seeds and signs in
the owner. The harness provisions a member and a PIN profile via the local API;
that path still needs hosted verification. Credentials, tokens and state must
not be cached or uploaded.

The fixture needs small generated films (including editions and missing artwork),
shows with seasons and specials, several artists and albums including a
multi-disc release, chaptered audiobooks, books and comics. Include text and
image subtitles, long and non-Latin titles, HDR-tagged media and multiple audio
tracks. Generate legal short, low-resolution samples and local metadata sidecars;
never depend on remote metadata. Keep the full generated library to a few hundred
MB at most and key its cache by all generator inputs and versions. The inherited
generator is provisional: it contains a 180-second 720p load fixture, lacks
some requested media, and cannot generate PGS subtitles with ordinary FFmpeg.

Cache a pre-scanned library snapshot only after proving it can be restored on
each runner OS and path without carrying usable credentials, sessions, keys or
cross-run identities. Provision fresh accounts after restoring the sanitized
snapshot; verify the restored media paths and scan readiness rather than assuming
they survive relocation. Until that exists, scanning on each run is a temporary
limitation, not fulfillment of the snapshot requirement.
