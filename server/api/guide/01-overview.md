# 1. API conventions

## Address and contract

The API and bundled web app share the server's origin and listening port.
`GET /v1/system` is public and reports the server ID, name, version and setup
status. It does not publish a build fingerprint. The default port is 32500.
Use a hostname of its own; hosting under `/portico` or any other sub-path is
unsupported. HTTP is accepted from any address. Use TLS for untrusted networks.

There is no stable API yet. Current routes use `/v1`; former `/v2` playback and
legacy sign-in routes were removed. Changes during prerelease need
matching consumer updates. The registry contract is
[portico.openapi.json](../portico.openapi.json), generated from every production
API operation, including retained HTTP handlers and raw streams. It is the sole
specification; old YAML documents are removed.

## Route identity proof

`POST /v1/networking/identity-proof` accepts the existing JSON body containing
`baseUrl` (origin only) and `nonce` (32 random bytes, unpadded base64url). Send
`Accept: application/json; portico-route-proof=2` for a version `"2"` signed
payload; otherwise the server returns version `"1"` for existing clients. Older
servers ignore this preference and return version `"1"`. The envelope still
contains base64url `payload` and Ed25519 `signature` strings.

Version 2 adds optional `tlsCertificateFingerprints`: a unique list of SHA-256
digests of leaf certificate DER, encoded as 43-character unpadded base64url
strings (not SPKI or PEM hashes). These come only from the server's managed
active/previous certificates and owner-provided TLS files. With no serving
material the field is omitted, including for proxy-only TLS installations.
Clients accept at most eight fingerprints and reject unknown payload fields or
versions. All existing origin, nonce, key-derived server ID, signature and
30-second proof lifetime checks still apply.

A verified route identity alone cannot authorize an untrusted TLS certificate:
automatic native pinning also requires its exact fingerprint in this signed list
and, for an existing pin, the same server ID. Missing or unmatched bindings ask
the person to trust the certificate. A relay can forward a genuine proof but
cannot add its own certificate without invalidating the signature.

## Time and numeric values

Calendar timestamps normally use RFC 3339 UTC, for example
`2026-10-13T18:22:04Z`. Some surfaces require whole seconds (the TV guide); do
not assume a fixed millisecond suffix. Notification timestamps are integer
Unix milliseconds. Every duration, position, interval and wait on the wire is
whole milliseconds and named with `Ms` (`durationMs`, `progressMs`, `startMs`,
`retryAfterMs`, `waitMs`, settings such as `throttleBufferMs`), as are playback
session positions. Do not infer the wire shape from a Go field ending in
Seconds: custom JSON methods convert it. The HTTP `Retry-After` header stays in
whole seconds, as HTTP defines it. Subtitle cue documents and
social timelines use decimal microsecond strings. Use the unit named in the
field and preserve decimal strings. Revisions and IDs are opaque even when a
revision looks numeric; keep the exact JSON type returned by that route.

## Paging

Cursor reads return `nextCursor`. Treat absent or empty cursors as exhaustion;
a short page alone is not exhaustion. Send a cursor back unchanged, with the
same query and identity. Defaults, maxima and validation differ by operation;
read its contract. For example, title query pages default to 50 and allow 1–100;
needs-attention pages allow 1–50. Do not assume all pages allow 100.

A cursor rejected with `invalid_cursor` cannot be repaired by editing it.
`stale_continuation` means the read's permissions or projection changed: discard
the continuation and reread. Windowed content may instead carry a starting
position, total and revision. Send the revision that belongs to that window.
The title query has a separate 30-minute cursor that can finish a walk during
a scan; see [tools](08-for-tools.md).

## Errors and retries

Errors contain an `error` object with a machine-readable `code` and a plain
`message`. Existing handler errors carry `retryable`; registry errors also use
`retry` (`never`, `same_request`, `after_refresh`, `after_reauth`) and may carry `fields`,
`current`, `requestId` or a delay. Some older handlers put current state beside
`error`. Handle the actual response rather than assume every error has the same
optional fields.

Typical responses are 400 for invalid input, 401 for unusable credentials, 403
for scope or role refusal, 404 for unavailable/hidden resources, 409 or 412 for
a changed resource, 428 for a missing required precondition, 429 for a limit,
and 503 for temporary capacity or dependency trouble. Follow `Retry-After`
(seconds, or an HTTP date). Retry the same request only when instructed; reconcile
first where asked. Back off and keep already displayed content during failures.

## Revision preconditions

Read before editing, and send the returned resource revision. There are two
forms, chosen per route:

- Body `expectedRevision` on settings, metadata and many personal-state writes.
  A missing or null required field answers **428 `revision_required`**.
  A changed revision normally answers 409 with the route's conflict code.
- Header `If-Match` on playback sessions, queues and device capabilities. Use
  the returned ETag or quoted revision. Missing required headers answer
  **428 `revision_required`**; a stale header answers **412 `revision_mismatch`**
  with current state where the route supplies it.

An explicit zero is different from an omitted revision; only use zero where
that operation defines creation or an initial state that way. After a conflict,
read current state and retry quietly if the intended fields have not changed.
Do not overwrite a person's intervening edit. Metadata value revisions are
separate from background candidate revisions.

Conditional GETs on supporting routes accept `If-None-Match` and can answer
304. Reuse the stored response; do not parse a 304 as JSON. Binary artwork and
media have their own validators and grant checks.

## Idempotency and request bounds

Idempotency is per operation: some mutations use body `operationId` or
`idempotencyKey`; playback starts use `Idempotency-Key`. Retain a key until the
result is known and retry an identical payload with that key. Reusing it for a
different request is refused. Replay windows differ by resource. Playback starts
without the required key answer **428 `idempotency_key_required`**.

Typed JSON routes reject unknown and repeated query parameters and bound JSON
bodies. Retained HTTP handlers keep their existing decoding rules. Bounds are
published as `x-max-body-bytes`. A limit is not permission to send unbounded nested data. Session sign-in
has per-address, network and account budgets; API keys and tokens have their own
rate and concurrency budgets, documented in [keys and tokens](07-keys-tokens-and-scopes.md).

Regenerate the checked-in contract after changing a registry declaration:
run `GOFLAGS=-p=2 go run ./cmd/apigen` from `server/`. The published-document
check compares the actual artifact with the live registry.
