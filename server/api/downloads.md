# Offline downloads

How a client takes Portico media offline and plays it with no server in reach. The typed contract
is in `portico.openapi.json` (the operations whose ids contain `download`); this page explains the
model and the order things happen in. The first-party apps use exactly these routes.

Downloads produce no media of their own and keep no copy of any. A download is a *claim* on bytes
that already exist — the library's own file, read in place, or a converted copy the server already
holds — plus a verification hash, an address to fetch the bytes from, and a signed statement that
this viewer may play those bytes offline.

Two things are kept apart throughout, and a client must keep them apart too:

- The **claim** is the server's promise to hand the bytes over. It ends when nobody fetched it for
  the retention period, when the file is replaced, when a converted copy is deleted, or when the
  title leaves the library.
- The **receipt** is the viewer's permission to play a copy already on a device. It ends only when
  the server's owner takes something away from the viewer, the receipt is revoked,
or its thirty-day validity is not renewed: downloads are turned off for the
  profile, the account is switched off, the title or its library is closed to them, or the receipt
  is withdrawn.

The server tidying up never ends a receipt. A device copy stays playable while
its receipt is valid and its viewer retains access; reconnect to renew it.

---

## The steps

```
GET  /v1/items/{id}/download-options              what can I take, how big, may I?
POST /v1/downloads/preparations                   claim it
POST /v1/downloads/receipts                       take the offline receipt (as soon as the claim is ready)
POST /v1/downloads/preparations/{id}/grant        get the address of the bytes
GET  /v1/downloads/artifacts/{grant}              move the bytes  (Range, If-Range, ETag, 206)
GET  /v1/downloads/preparations/{id}/bundle       everything that travels with the file
GET  /v1/downloads/artifacts/{grant}/artwork/…    its pictures
GET  /v1/downloads/artifacts/{grant}/subtitles/…  its subtitle tracks, as text and as authored
```

Then, whenever the client is back in contact:

```
POST /v1/downloads/receipts/revalidate            renew the receipts it holds
POST /v1/downloads/progress                       report the plays it made offline
```

Take the receipt before moving the bytes. The server knows the file's digest as soon as the claim
is ready, so a transfer that finishes with the app closed and no connection is already allowed to
play.

## Qualities

`download-options` lists `original` first, then each smaller quality this server can actually hand
over for the title: one it already holds a converted copy of, or one the conversion code reports it
can make. A quality that is only a name in the ladder is not listed, and a claim for one is refused
with `optimized_version_unavailable`. A client keeps no list of qualities of its own; when the
server learns to make a new one it appears in the menu.

Each option carries `estimatedBytes` (exact for the original and for an existing copy, otherwise
bitrate times duration with `estimated: true`), `available`, a `reason` when it is not, and
`requiresPreparation` when choosing it starts a conversion. Only the server's owner can start a
conversion; anyone may download a copy that already exists.

For a whole show, season, album, book or playlist there is no single file to read a menu for.
`GET /v1/downloads/settings` lists `qualities`: the original, and each smaller quality the server
can make while conversions are switched on.

A title stored as several files (a film in two parts) cannot be downloaded yet and says so with
`several_files`. A title with several versions downloads the first version that can be read.

## Claims

`POST /v1/downloads/preparations` takes exactly one of `mediaId`, `mediaIds` (up to 100) or
`nextAfterMediaId` (the episode after this one), with a `quality` and an `operationId`. It is
idempotent on `operationId`. The answer is a batch: the claims (new or already existing), per-item
`rejected` reasons, and `accepted`, the number of new or restarted claims. Asking again for a claim
that failed starts it again. Asking for something already claimed returns it with `accepted: 0`
and nothing rejected.

A claim moves through `queued → running → ready`, and can be `paused`, `failed`, `unavailable`,
`cancelled` or `expired`. Its `actions` are exactly what its state accepts.

Every claim carries `title`: what it is, its year and length, the show, album or book it belongs to
with their ids, its season or disc number and its episode, track or part number, and `onServer`.
A list of claims reads as titles, in order, with no further request, and keeps reading that way
after the title has left the library (`onServer: false`).

`GET /v1/downloads/preparations?ids=<comma-separated preparation IDs>` reads a sparse
snapshot of up to 200 claims, with the same viewer and title-access checks as the ordinary
list. It returns all accessible requested claims; missing or inaccessible claims are omitted.
This form cannot be combined with a cursor and uses a 200-row bound regardless of `limit`.
Clients use it to refresh older queued claims together instead of issuing one request per row.

An original is ready as soon as the server knows the file's SHA-256. The first claim on a file
reads it once, in place, in checkpointed windows (a pause or restart costs one window). Every later
claim on the same unchanged file, by anyone, is ready at once.

Actions (`POST …/{id}/actions`) name the `revision` the client read. The revision moves only when
the claim's state does, never for progress, so a Pause or Cancel made against a running claim is
not refused because a byte count ticked. On `409 download_conflict` read the claim again and send
the action once more if it is still offered.

Removing a claim ends what the server will transfer for it. It does not remove copies already on
devices; a device removes its own copy, and tells the server with `receipts/revoke`.

## Transfers

`POST …/{id}/grant` returns `url` (a path on this server) and `token`. The address is the whole
permission: no sign-in is sent with it. It stays good while it is used or renewed, and for 24 hours
after the last of either; every request made with it rechecks that the viewer may still see the
title and still download, and that the bytes are still the ones the claim describes. It ends with
the claim.

To keep the same address alive, send `renew` with the token already held:

```json
{"operationId": "…", "renew": "<token>"}
```

When the token still belongs to this viewer, this claim and this exact file, the same `url` comes
back with `renewed: true`, even if it had run out. Otherwise a new address is issued. Renew a
transfer that is still unfinished every `renewAfterMs`. This is what lets a phone's own
downloader, which re-requests the same address hours later, carry on.

The file's identity is its digest, not its address. `GET` supports `Range`, and the `ETag` is
`"sha256-<digest>"`. Continue a partial file with:

```
Range: bytes=<bytes already held>-
If-Range: "sha256-<digest>"
```

under any address for the same claim. A `206` continues it; a `200` means the server's file is not
the one the partial came from, and the whole body replaces it. Verify the SHA-256 of the finished
file against the claim's `artifact.sha256` before calling it ready.

Errors on the address:

| Status | Code | Meaning |
|---|---|---|
| 401 | `invalid_download_grant` | The address has run out or was withdrawn. Renew it, or take a new one. |
| 403 | `downloads_not_allowed` | Downloads are off for this profile. |
| 410 | `download_unavailable` | The claim ended (file replaced, copy deleted). Claim the title again. |
| 409 | `download_not_ready` | Not ready yet; for a picture, its smaller copy is still being made; for a subtitle file or the bundle, the track is still being read out of its file. Retry. |
| 503 | `download_source_offline` | The library file cannot be read right now. Nothing changed; retry later. |

## What travels with a download

`GET /v1/downloads/preparations/{id}/bundle` (signed in, claim ready) describes everything a device
needs to list, show and play the title with no server in reach:

- `title` and `overview`;
- `media`: the container, codecs and picture size of the downloaded file;
- `artwork`: each picture with a `path`;
- `chapters` and `markers` (intro, recap, credits and the like; `automatic` says the server would
  skip it unprompted), on the file's own timeline. Markers follow the viewer rules of playback:
  weak or ambiguous detector guesses are left out, overlapping markers of one kind become the
  strongest one, and a library whose detection is off carries only markers its owner made or
  approved ([Intro and credits detection](segments.md));
- `audio`: the audio tracks inside the file, in order;
- `subtitles`: each track with its `files`, keyed by form. `vtt` is the track as plain text
  (every text track, and a styled one's plain reading). `ass`, `pgs`, `vobsub` and `dvb` are the
  track as it was authored, for a device that draws styled or picture subtitles itself; `sub` is
  the pictures beside a VobSub index; `font0` onward are a styled script's attached fonts. A
  picture-only track has no `vtt`.

Every `path` is relative to the grant's address: fetch `<grant url>/<path>`. Pictures are sized for
a phone or tablet (a poster 800 wide, a backdrop 1920). `404` on a picture means the title has no
such picture, which is an ordinary answer.

## Offline receipts

`POST /v1/downloads/receipts` with up to 100 `preparationIds` returns one outcome each. `issued`
carries a receipt: `payload` is the exact signed bytes (base64url JSON), `signature` an Ed25519
signature over them, `keyId` the key, and `claims` the same document decoded. The claims name the
viewer (authority, account, profile, server), the title, the quality, the file's SHA-256 and size,
and `expiresAt` thirty days out. Verify against the keys from `GET /v1/downloads/receipt-keys`,
remembered on the device so verification needs no connection.

Play a downloaded file only while its receipt verifies, names the signed-in viewer and has not
expired.

### Revalidation

`POST /v1/downloads/receipts/revalidate` with up to 100 `receiptIds` renews each for another thirty
days. Once a day is enough; it is also how a device learns that access was withdrawn.

- `renewed` carries the new receipt, `titleOnServer` (false when the title has left the library;
  the copy still plays, say so beside it), and `viewing`: the title's resume point and watched
  state on the server now, so a copy resumes from where another device left off.
- `refused` with `revoked`, `downloads_not_allowed`, `account_disabled` or `item_deleted` means the
  viewer may no longer play the copy: remove it.
- `refused` with `unknown_receipt` means the server has no record of the receipt (it belongs to a
  different profile, or the server was restored from an older backup). It says nothing about the
  copy: keep it, and ask for a new receipt for its claim. `unknown_preparation` on that request
  means the claim is gone too: claim the title again and, when the new claim's digest equals the
  file already held, take its receipt without transferring anything.

This route, and `POST /v1/downloads/progress`, answer even when downloads are switched off for the
profile, so a device can be told to remove its copies and the plays made while it was allowed still
count.

### Revocation

`POST /v1/downloads/receipts/revoke` withdraws receipts: a viewer their own, the owner anyone's
(`allProfiles: true`). `reason` is one of the documented reason codes. A device that removes its
own copy revokes that copy's receipt. `GET /v1/downloads/receipts/revocations` pages what has been
withdrawn, by sequence.

## Offline plays

`POST /v1/downloads/progress` takes up to 100 entries, one per *play*:

```json
{"playId": "…", "itemId": "…", "positionMs": 1432500, "ended": false, "observedAt": "2026-10-13T09:12:00Z"}
```

`playId` is the device's own name for one play and stays the same while later positions of that
play are reported. Watching a film twice on a flight is two plays. `observedAt` is the device's
clock at the observation. `ended` is true only when the file played to its end; whether a play that
stopped short counts as watched is decided by the server, by the viewer's own settings and the
title's credits, exactly as for online playback.

Every play that really started is recorded in the server's play history and the viewer's own, and
counted, dated to when it happened. It also moves the title's resume point and watched state when
it is the newest thing the server knows about the viewer and the title. It is not when a later
offline play was already accepted (`stale_observation`), or when the viewer played the title online
afterwards, marked it (or its show, season, album or book) watched or unwatched afterwards, or
dismissed it from Continue Watching afterwards (`superseded_online`). A viewer who has paused their
history gets `history_paused`: the owner's record of plays is kept, nothing of the viewer's own is
written.

Each result carries `positionMs` and `watched` as the server now holds them, whatever became
of the entry. Use them as the copy's resume point.

## Whole shows, seasons, albums, books and playlists

`POST /v1/downloads/requests` names a container, a `deviceId`, a `quality` and a policy
(`all`, `unwatched`, or `next` with `keepNext`). The server reads the members in pages and claims
each; there is no size limit. A library that changes while it is being read does not fail the
request: the reading starts again, and after a few tries carries on regardless.
`GET /v1/downloads/requests/{id}` reports counts and a page of members.
`GET /v1/downloads/requests?deviceId=…` lists the requests a device started that are still at work
or finished in the last day, so a device finds them again after the app was closed.

## Storage and retention

`downloads.maxPreparedBytes` (0 means unlimited) caps the converted copies claimed across the
server. Originals take no room and are never refused for lack of it. A ready claim nobody fetched
for `downloads.retentionDays` (default 30) expires; this frees the claim and nothing else.
`GET /v1/downloads/usage` reports what a viewer, or for the owner the whole server, has claimed.
Ended claims, finished requests and receipts nobody could still be holding are removed after a
month.

## Policy

A profile with downloads switched off is refused on every route here except revalidation and
offline plays. Permission is rechecked at claim, grant, every transfer request, receipt and
renewal, and a title the viewer may not see answers exactly like one that does not exist.

Offline observations up to 24 hours ahead are retained with their effective observation time clamped to the receiving server time. Larger future skew is refused per entry. A skewed device cannot reserve tomorrow’s resume point against newer activity. Clients still need a persisted monotonic receipt clock for offline expiry across device-clock changes.
