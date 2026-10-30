# 4. Playback

Playback uses sessions and device queues under `/v1`. The former `/v2`
controller/occurrence API is removed. The generated registry contract describes
the current resources; `internal/httpapi/playback_v1_routes.go` wires them.

## Capabilities and options

Publish this device's one capability document with
`PUT /v1/me/devices/current/capabilities`, using `If-Match` against the revision
read from GET on that address. It describes engines, codecs, containers,
subtitles and display/audio limits; do not claim capabilities the player lacks.
`GET /v1/items/{itemId}/playback-options` returns sources, controls and the
server's decision for this device. Use the selected `presentation.engine`.
Original is the default quality; Automatic is an explicit viewer option.

A physical version may include `disc: {kind, packaging, titleNumber, chapterCount}`.
`kind` is `bluray` or `dvd`; `packaging` is `iso` or `folder`. The title number
is the stored public Title N number for the exact disc title whose timed media
was measured. Chapter count may be zero. File info carries the same optional
block on its corresponding file. Missing facts mean unknown: old scans, changed
source or technical revisions, and multi-part versions do not invent a title.
This display block does not turn disc titles into playable versions, identify
the current owner-selected main title, or grant a title-change/play command.

Audio sessions can return the same optional `playbackInfo` comparison from
their pinned version-2 AudioPlan. Its source facts were captured when that plan
was accepted; delivered facts describe its direct or converted audio bytes.
The current session, media generation and committed audio-render ID and target
must still match. Legacy, unavailable, ambiguous or replaced plans omit the
comparison. This never substitutes the general VOD delivery recipe for an
AudioPlan. It reports no sink/output handling: `observedPlayback.audio` remains
the current renderer's independent output report. A client using a fallback
outside that AudioPlan must not present the AudioPlan recipe as its delivery.

## Start, change and report

```text
POST /v1/playback/sessions
Authorization: Bearer <token>
Idempotency-Key: play-20261013-1
Content-Type: application/json
```

```json
{"itemId":"<item-id>"}
```

201 returns a session with `id`, `revision`, `state`, `lease` and `presentation`.
Play `presentation.url` using its selected engine and start position. 202 means
preparation is still running: follow `Retry-After`/`retryAfterMs` and replay the
same start/key; do not make a new session for every poll. Grants in returned
media and subtitle URLs authorize bytes; do not invent media paths.

`GET /v1/playback/sessions/{id}` rereads the resource and returns its ETag.
`PATCH` with `If-Match` changes state, quality, version, audio, subtitles or seek.
For example, `{"state":"paused"}`. A change that changes the bytes keeps the
session and queue but advances `presentation.generation`. Adopt the returned
presentation and new URLs. Missing preconditions are 428; stale revisions are
412 `revision_mismatch`. DELETE stops the session (optional `positionMs` body).

`POST /v1/playback/sessions/{id}/timeline` reports renderer evidence:

```json
{"seq":1,"generation":1,"state":"playing","positionMs":321500}
```

Use the actual presentation generation and strictly increasing `seq`. Reports
renew the lease and update progress/history; follow `lease.reportEveryMs` and
`Report-Every-Ms`. Serialize reports and retry a lost response unchanged. Read
[reporting and overload recovery](../playback-admission.md) for admission rules.
A session resource is transport intent; timeline reports are what the device
actually did. A failed player can report `failure` in PATCH for server replanning.

## Queues

`POST /v1/queues` with an `Idempotency-Key` creates this device's queue from
exactly one selector form (container, query or explicit item IDs), or segments:

```json
{"selector":{"items":{"ids":["<item-id>"]}}}
```

The queue header is a bounded resource; read its entries through
`GET /v1/queues/{id}/window` or `/entries`. Do not download a whole queue to
move to its next entry. Header reads include current entry, repeat/shuffle,
segments and available post-play information. Mutation routes use `If-Match`:
`POST …/segments`, `DELETE …/entries/{entryId}`, `POST …/entries/{entryId}:move`,
`PATCH /v1/queues/{id}` and `POST /v1/queues/{id}:advance`.
Prepare/commit-next and save-as-playlist routes also use idempotency keys.
Use the operation's request shape in [portico.openapi.json](../portico.openapi.json).

A Music radio uses the existing container selector:

```json
{"selector":{"container":{"kind":"radio","id":"<song-album-or-artist-id>"}},"startPlayback":{"state":"playing"}}
```

The server starts the seed song, or the first permitted song on the seed album
or artist, then selects from the profile's permitted Music libraries. Actual
current sonic vectors rank the songs when available; genres and qualified
co-listening supply the fallback. A segment reports `kind: "radio"` and the
seed's stored title as its label. It cannot be replaced by a finite shuffle.

Radio stores bounded lookahead in the ordinary queue. `total` counts buffered
occurrences and may grow; it is not the radio's final size or duration. Advance
and committed local audio preparations refill that lookahead in the same queue
revision change. Existing entry identities, manual queue order, idempotency,
profile restrictions and the single active-session transfer remain in force.
With only one permitted song, repeated occurrences refer to that real song.
No extra query flag or parallel session is needed. This queue protocol does not
establish receiver preload, media cache reuse, gapless output or hardware facts.

`session.updated`, `queue.updated` and related [feed hints](06-events.md) tell a
client to reread; they do not replace the resource. Revocation ends grant access.

## Subtitle delivery

The generated registry contract in `portico.openapi.json` describes the subtitle
routes. `GET /v1/items/{itemId}/subtitles` includes all admitted discoveries as
selectable resources. Discovery is enough to choose or automatically choose a
track: clients do not import a track before playback. Its bytes are an internal
cache, filled on first delivery. A disconnected first request does not discard
that work; a retry joins it. A cold embedded track can still require a full demux
pass before its bytes are available.

Each resource and playback-option track exposes `deliveries`. Formats are `text`,
`ass`, `pgs`, `vobsub`, and `dvb`. A selected track includes URLs under the current
playback grant:

| Delivery | Payload | Additional files |
| --- | --- | --- |
| `text` | `{base}.vtt`, UTF-8 WebVTT | None |
| `ass` | `{base}/original.ass` or `original.ssa`, authored script | `{base}/fonts` lists font names and grant URLs |
| `pgs` | `{base}/original.sup`, PGS packets | None |
| `vobsub` | `{base}/original.idx` | `companionUrl` supplies `original.sub`; preserve the matching basename locally |
| `dvb` | `{base}/original.mks`, a single-track Matroska DVB packet capsule | None |

Here `{base}` is `/v1/media/subtitles/{grant}/{resource}/{revision}`. Original and
font files accept GET, HEAD, byte ranges, and conditional reads. WebVTT also
supports byte ranges. ASS/SSA originals preserve their source encoding; the
response content type declares its detected charset. Decode that encoding before
passing script text to a renderer that requires Unicode. Font filenames are
labels; download through each returned URL, not a filename-derived path.

The `subtitles` namespace is separate from Live TV's `linear` namespace. Use the
URLs returned by the presentation or subtitle plan; do not derive them from the
video URL. Every subtitle payload, including JSON and WebVTT, uses this base.

The separate `{base}` route returns a version 1 JSON cue document:
`{version:1,timeDomain:"source-relative",cues:[{startUs,endUs,text,italic?,top?}],skippedCues?}`.
Times are decimal microsecond strings. The selected `documentUrl` names this JSON
form; a presentation labelled `webvtt` names the `.vtt` form. Plain-text clients
apply the selected offset locally to JSON cues. WebVTT includes the offset.

Subtitle selection accepts `rendering: "text"|"ass"|"pgs"|"vobsub"|"dvb"` when a
device chooses a supported delivery itself. `renderer: "client"` means an
original is drawn on the device, without changing the video. The v1
`embeddedClient` selection now uses this path and returns the original URLs.
A client should choose it only for formats its player declares it can render;
otherwise the server's ordinary burn-in path remains available subject to policy.
Styled resources can also be explicitly selected with `rendering: "text"`.

Grants, live session pins, viewer visibility, exact source bytes, and remote
representation evidence authorize delivery. Stopping a session or changing its
grant ends access. Re-read the presentation after renewal. JSON cue clients can
retain the existing validator-based authorization checks; byte ranges and
conditional responses never bypass grant checks.

Admission remains bounded: 32 discovered tracks per file, 4 MiB source text,
32 MiB bitmap data, 20,000 cues, 8 KiB per plain cue, and a 24-hour timeline.
Inventory capacity retains usable tracks and a warning. Font extraction admits
up to 16 attempts, 8 MiB per font, 16 MiB total; a font list can report
`skippedCount`. Malformed cues are skipped; `skippedCues` reports the count where
available. Two cache fills run at once, with at most 64 in flight. Each track
allows two minutes without input progress, bounded by two hours overall.

Subtitle search uses the profile's preferred subtitle language when `language`
is empty, falling back to English. It keeps regional Portuguese/Chinese tags and
uses title/provider identities or series and episode identity.

Portico ships no OpenSubtitles key: the owner brings their own free
OpenSubtitles.com account and API key (an "API consumer" in their OpenSubtitles
profile). They save them in Server › Metadata
(`PUT /v1/admin/metadata/settings`: `openSubtitlesApiKey`,
`openSubtitlesUsername`, `openSubtitlesPassword`, all write-only), or set
`PORTICO_OPENSUBTITLES_API_KEY`, `PORTICO_OPENSUBTITLES_USERNAME` and
`PORTICO_OPENSUBTITLES_PASSWORD`; saved values win over the environment and
apply without a restart. `PORTICO_OPENSUBTITLES_USER_AGENT` overrides the
default `Portico vX.Y.Z` User-Agent. Credentials and tokens never appear in any
response. The former immutable `PORTICO_OPENSUBTITLES_TOKEN` input is removed.

With an account the server signs in once and keeps the session for its
lifetime (24 hours, or the token's own expiry), sends later calls to the
`base_url` the sign-in names (only `*.opensubtitles.com` hosts are followed),
signs in again once if a call is refused as unauthorised, and never repeats a
refused sign-in until the credentials change or the owner tests the connection.
Sign-ins stay within OpenSubtitles' limits (10 a minute, 30 an hour, a 30-second
pause after a failure); all calls are paced at one a second and honour
`Retry-After`/`RateLimit-Reset`.

`GET /v1/admin/metadata/settings` includes `openSubtitles`: `keySource` and
`accountSource` (`owner`, `environment`, `none`), `account` (`none`,
`incomplete`, `not_checked`, `signed_in`, `rejected`), the last `problem`, and
the download quota as OpenSubtitles last reported it (`downloadsRemaining`,
`downloadsAllowed`, -1 when unknown; `downloadsResetAt`).
`POST /v1/admin/metadata/settings/opensubtitles/check` tests the saved
credentials now (one or two requests, no download) and answers the same status.

Search and apply answer OpenSubtitles' refusals by code:
`subtitle_provider_not_configured`, `subtitle_provider_key_rejected` (403: a key
or User-Agent OpenSubtitles refuses), `subtitle_provider_sign_in_failed`,
`subtitle_provider_sign_in_required` (a download refused without an account),
all 503 retry `never`; `subtitle_provider_quota` (406 from OpenSubtitles: the
day's downloads are used) as 429 retry `never` with `Retry-After` until the
quota resets; and `subtitle_provider_busy` (429 from OpenSubtitles) as 429
retry `same_request` with its wait. Clients show "Subtitles provided by
OpenSubtitles.com", linked where they can, wherever these subtitles are searched
or picked.


After publishing a changed device capability document, request
`PATCH /v1/playback/sessions/{id}` with `{"replan":true,"positionMs":61000}`
and the session's current `If-Match`. The new presentation uses the playing
device's current capabilities while retaining the session, queue entry, state,
version, quality and track choices. The action is not saved as a preference or
replayed on the next title. Live channel sessions reject this action; their
wall-clock transport has its own controls.

When the server's conversion slots are occupied, operations that create a
conversion (start, session change, queue create/advance and prepare-next) answer
429 with code conversion_capacity, retry same_request and Retry-After: 2.
Retry the same request after that delay. This is temporary server occupancy;
the account's stream_limit_reached policy refusal remains a separate 409.
