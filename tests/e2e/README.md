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
  host through its emulator gateway. The dev sign-in hook accepts only a
  loopback socket peer, so the Android connection needs a runner-local forwarding
  bridge that preserves that boundary; direct gateway access cannot use the hook.
* **Persistent remote:** a separate test server on the approved `portico-runner`
  Contabo host, provisioned by the ops lane and independent of the demo. Use it
  for Portico Services sign-in and claiming, `portico.direct` and certificates,
  Enhanced and Standard remote access, and outside-network connections. These
  tests require the environment and test-account handoff; no credentials belong
  in code, caches, screenshots, video or logs.

Neither target is a replacement for the other. Never connect a client on one
GitHub runner to the disposable server on another. Never expose `devtrust` on
the persistent server. All test failures remain non-required CI signals.
