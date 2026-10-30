# Administration: people, diagnostics and connectivity

Companion to the current administration handlers and `portico.openapi.json`;
the generated registry is the API contract.
This page explains the rules behind it and says where each one is enforced.

## Access tiers

Three tiers, ordered `owner` > `admin` > `member`. They are defined once, in
`internal/identity/access_tier.go`, and asked through a single predicate:

```go
identity.Grants(role, needed)   // does this role satisfy this requirement?
identity.Administrative(role)   // owner or admin
identity.ManagesTier(actor, target)
```

Nothing else in the server compares role strings. `Grants` ranks unknown roles
below `member`, so a future or corrupted value can never out-rank a real one.

| Tier | May |
| --- | --- |
| `owner` | Everything. Exactly one account per server. |
| `admin` | Libraries, metadata, invitations, member limits, device trust, diagnostics reads and the debug window, connectivity **status**. |
| `member` | Nothing administrative. |

An `admin` may **not**: change server security or connectivity **policy**, take
backups, create or revoke API keys or other members' tokens, export the diagnostics bundle, or create,
promote, demote or edit another `admin` or the owner.

Two places carry the tier into the running server:

- `internal/httpapi/administration.go` — `tierAuthorityTx` is the admin-tier
  counterpart of `ownerAuthorityTx`. It re-reads the account's **live**
  membership row inside the transaction that does the work, so a token minted
  while an account was an administrator stops working the moment the account is
  demoted. `d.administrator(r)` is the request-level helper; owner-only routes
  keep `d.owner(r)`.
- `internal/identity/direct_policy.go` — `DirectAccess` grants an administrator
  every library, using `Administrative` rather than a second `== "owner"` test.

Changing a tier bumps the account epoch and revokes its sessions, so a demotion
takes effect immediately rather than at the next token expiry.

## Invitations

Invitations are addressed by **account email**, because a direct server has no
mail transport of its own: the address identifies the invitee, and the code is
delivered out of band. One pending invitation per address; at most 200 pending.

The code is returned exactly once, in the `201` from
`POST /v1/admin/access/invitations`. Only its SHA-256 digest is stored. A replay
of the same `operationId` returns the stored document, which never carries the
code.

`POST /v1/access/invitations/accept` is unauthenticated — the invitee has no
account yet — and is rate-limited per peer with the same limiter the sign-in
routes use. Accepting creates the account with the invitation's tier and library
allow list; the password must satisfy the same policy the owner's own member
creation uses (`identity.ValidDirectPassword`).

## Per-member limits

One envelope per account, replaced whole so a client never observes half a
policy. An account with no stored envelope reads as unrestricted at revision 1,
so a client can write against that revision without a create step.

| Limit | Enforced in |
| --- | --- |
| `maxStreams` | `httpapi.admitPlayback`, before a playback lease exists (`POST /v1/playback/sessions` including channel and queue-entry starts, queue transitions). Counts active playback sessions without double-counting their presentation rows. |
| `remoteBitrateKbps` | `httpapi.admitPlayback` returns it as `Decision.MaxVideoBitrateBPS` when the request is remote. Every v1 start path admits through `playbackv1.Caller.Admit` (item, channel, queue-entry starts and queue transitions), and a v1 PATCH that re-plans (quality, version, audio or part change) re-admits the same way, so the cap reaches the transcode decision on each; the playback-options preview reads the same cap through `v1Reach` without admitting. The server-wide ceiling is applied separately through `RegistryDeliverySettings.Delivery()`; the delivery policy clamps against both, so the lower wins. |
| `maxContentRating` / `ratingSystem` / `allowUnrated` | Folded into the viewer's content restrictions before any title is judged (`access.Limits.Narrow`, `contentaccess.RestrictionsTx`), then applied by the one catalogue predicate for lists, counts, search, detail and playback alike. |
| `tagPolicy.deniedLabels` | The same predicate. Matched case-insensitively against the catalog's `label` and `tag` attributes. |
| `schedule` | Every session created or renewed (`identity.IssueTx` and `identity.RefreshSession`, through the rule the router attaches to each request), `httpapi.admitPlayback` and `httpapi.admitChannel`. Evaluated in the schedule's own IANA time zone, or the server's when it names none. |
| `channelPolicy` | `httpapi.admitChannel`, on v1 channel starts only (`POST /v1/playback/sessions` with `channelId`, matched against the canonical v1 id `live:<source>:<ch>` / `library:<ch>`). Writes accept canonical ids only. |

**One rating rule.** An account's rating limit and a profile's own restriction
are the same rule on the same scale: the youngest age a rating admits
(`identity.RatingAge`). The account's limit is one value of one published
rating system (`GET /v1/rating-systems`, sent as `ratingSystem` +
`maxContentRating`), or, with no system named, any rating the table knows.
The document returns what it resolves to as `maximumAge` (`-1` for none). The
lower of the account's and the profile's ceilings holds; unrated titles are
hidden when either side hides them; a title carrying several ratings is
judged by all of them. A PG limit therefore shows TV-PG (both are eight and
up), and a rating from another country is judged by its age rather than
passed off as unrated. A rating the table does not know is **unrated**.

**Outside the allowed hours** a sign-in, a profile choice and a renewal are
refused with `403 outside_allowed_hours`. Nothing is revoked: the device keeps
its sign-in and is admitted again when the hours return.

An administrator is not exempt by accident. Limits are per account and an owner
or admin simply has none by default; a stored envelope on an administrative
account is applied like any other.

## Devices

There is one device record (`identity_devices`): the one a session is bound to
and every request is checked against. `GET /v1/admin/access/devices` lists
those records across accounts for the owner and administrators, with each
device's approval as `trust` (`pending`, `approved`, `blocked`), and
`PUT /v1/admin/access/devices/{id}/trust` decides it. An administrator who is
not the owner sees and decides about plain members' devices only.

`deviceApprovalRequired` is the identity service's own policy value: with it
on, a member's new device is recorded as `pending` and gets no session until it
is approved. The owner's own devices are never held. A television signed in by
code under the policy is recorded and waits; the same code works once the
device is approved. Turning the policy off admits waiting devices; a blocked
device stays blocked. Blocking a device ends its sessions at once.

## Deleting a member

`GET /v1/direct/members/{id}/deletion-preview` says what will go;
`DELETE /v1/direct/members/{id}` with the previewed `expectedRevision` removes
the account, its profiles and what they saved, its devices and its sessions.
Libraries and recordings are untouched. The owner's play history keeps the
plays and blanks the person's name, their profile names and their device
names. The owner deletes anyone but themselves; an administrator deletes plain
members.

## Permission changes apply live

Giving or taking a library, changing a profile's restrictions or library list,
promoting or demoting a member and transferring ownership no longer sign anyone
out: each applies on the person's next request. A person is signed out when the
account is turned off or deleted, when their password is replaced, and when a
profile gets a new PIN.

## API keys and personal access tokens

A key (the owner's) or a token (any member's) acts as the member who made it.
It is stored as a family that does not rotate plus an `api_credentials` row
holding its name, its scopes and the digest of its secret, and it has a
lifetime of its own: the `api_credential_*` triggers in the schema keep
anything that ends a member's sessions (a password change, sign out
everywhere) from ending it. It ends when revoked, when it expires, or when its
member is disabled or deleted. It authenticates on every route
with `Authorization: Bearer pk.…` or `pat.…`, as that member, with every live
check a session gets; `internal/httpapi/api_gate.go` resolves it, holds it to
the route's declared scope and to its own rate and concurrency limits before
any handler runs, and `identity.AuthenticateContext` refuses a credential-shaped
bearer that did not come through that gate.

Scopes are `read`, `playback`, `library` and `admin` (`admin` is all of them).
Every route declares the one it needs: typed routes in `apikit.Metadata.Requires`,
hand-registered routes in `internal/httpapi/route_scopes.go`. A route nothing
has classified, and every route that signs in, changes a password, approves a
device or manages keys and tokens, is session-only.

Routes: `/v1/admin/api-keys` (owner), `/v1/me/access-tokens` (the caller's
own), `/v1/admin/access-tokens` (the owner's view of every member's). The
developer guide is `guide/07-keys-tokens-and-scopes.md`.

## Message logs

The standard library's `log` package stays the writing interface for the whole
server. `servicelog.Recorder` is installed as `log.SetOutput`'s destination (teed
to stderr, which a container operator still needs), so every existing
`log.Printf` lands in the message log with no call site changing.

A line may carry optional prefixes: `warn: …` sets the level, `[playback] …`
sets the category, and both may appear in either order. Anything else is an
`info` line in the `server` category. The standard date/time prefix is dropped in
favour of the recorder's own timestamp.

Storage is two-layer:

- A bounded in-memory ring (5000 records) answers `GET /v1/admin/logs` and the
  SSE tail. Paging is by `sequence`, which is monotonic for the life of the
  process.
- Rotating files under `<state>/logs/` (`messages.log`, `messages.1.log`, …,
  4 MiB each, 4 kept) survive a restart and travel in the diagnostics bundle.

Rotation closes the active file before moving it and removes the destination
first. Windows refuses to rename or remove an open file, and refuses a rename
onto an existing path; doing both unconditionally keeps one code path rather
than a per-OS branch only one target would ever exercise.

Per-category retention (`server`, `playback`, `scan`, `network`, `client`) is a
settings row. A category with no entry keeps whatever the ring holds.

The debug window is **runtime state, not a settings change**: it raises the
effective level to `debug` for up to 240 minutes and reverts on its own, without
touching the saved `logLevel` or moving the settings revision. Its expiry is
persisted, so a restart inside the window keeps debugging rather than silently
going quiet — which matters, because the restart is often the thing being
debugged.

## Client log uploads

Any authenticated principal, including an API key, may upload. The body is
bounded twice — by the request reader and by the store — at 256 KiB; the account
and profile come from the credential, never from the body. At most 100 uploads
are retained, at most five per authenticated device.
Each account has a 1 MiB daily upload budget; uploaded device names do not
replace the session’s verified device identity.

## Diagnostics bundle

Owner only, because it carries the settings document. A zip of `manifest.json`,
`settings.json`, `system-report.json`, `connectivity.json`, `messages.json` and
`logs/`. Settings have every field the registry marks `secret` removed — the
bundle writer does not guess which those are; it asks the registry.

The archive stops at 16 MiB. A support archive that grows without a ceiling is a
denial-of-service against the server's own disk and against the person trying to
send it.

## Connectivity

Policy lives in the settings registry — one authority for every server setting —
and `/v1/admin/connectivity/policy` is its typed projection. `revision` is the
settings document revision, so this page and the general settings page conflict
correctly instead of silently overwriting each other.

| Setting | Effect |
| --- | --- |
| `remoteSignInPolicy` | `allow` / `owner-only` / `off`, applied at session creation, before admission commits. Off refuses remote sign-in before credentials are examined. Sessions already signed in are not disturbed. |
| `remoteBitrateLimitKbps` | Server-wide ceiling, exposed as `playback.DeliveryConfiguration.MaxVideoBitrateBPS` through `RegistryDeliverySettings.Delivery()`. |
| `lanNetworks` | The complete home-network list, stored masked. Defaults include standard local ranges and attached subnets. An empty list means all requests are remote. |
| `trustedProxies` | Socket addresses or ranges allowed to supply forwarded client address, scheme and host. |
| `advertiseUrl` | External HTTP or HTTPS origin given to local clients. Only HTTPS is published to Hosted. |
| `connectionLimit` | Ordinary connection ceiling, 0 for the memory-based default. Restart to apply; console/sign-in connections have a separate reserve. |
| `accessUrls` | Additional HTTP or HTTPS origins; no paths. Hosted publication filters to HTTPS. |
| `lanDiscoveryEnabled` | mDNS/Bonjour advertisement, applied immediately on save. |
| `dlnaEnabled` | The DLNA server (see [social.md §5](social.md#5-what-this-server-does-not-do-and-dlna)). Off by default; on and off apply immediately on save, including SSDP advertisement and goodbye. |

### LAN discovery

`connectivity.Advertiser` is portable: it uses only `net`'s UDP multicast
support. It sends unsolicited DNS-SD announcements for `_portico._tcp.local.`
every minute, and — when it can also bind the multicast group for reading —
answers queries that name the service.

A host that refuses either operation (a container without multicast, a
restricted Windows service account, a network with IGMP filtered) is not a
failure. The advertiser reports the reduced state and the status report shows it:

| `state` | Meaning |
| --- | --- |
| `stopped` | The owner turned it off. |
| `advertising` | Announcing and answering queries. |
| `announcing` | Announcing only; this host would not let the multicast group be read, so query-only browsers may not see the server. |
| `degraded` | The last announcement could not be sent; `detail` says why. |
| `unavailable` | This host refused a multicast socket at all. `supported` is false. |

### Status report

`GET /v1/admin/connectivity/status` combines the policy with what the host can
observe: interfaces from `net.Interfaces` (portable on every target; a host that
refuses the enumeration yields an empty list rather than an error), each
address classified as `public`, `lan` or `loopback` against the configured LAN
networks, the certificate state from `internal/networking`, and the real
discovery state. `warnings` names policy this host cannot satisfy:
`remote_sign_in_without_public_address`,
`lan_discovery_unsupported_on_this_host`.

## Concurrency and retries

Revision-fenced configuration mutations carry `expectedRevision` and, where
the route requires it, `operationId`. Missing required revisions answer 428.

- `expectedRevision` is the revision of the document being changed. A mismatch
  is `409 administration_conflict` with `error.currentRevision`.
- `operationId` is an idempotency key of 8–128 characters. The same key with the
  same body replays the stored response; the same key with a different body is
  `400 invalid_request` naming `operationId`. Receipts are a retry aid, not
  history: the newest 2000 are kept.

Lists use their own cursor or positional paging contract. Reuse returned
cursors and revisions unchanged; do not infer a uniform pagination rule.
