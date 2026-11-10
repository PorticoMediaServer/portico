# server-next: the API outline

This is the design the server is built to. It lists the conventions, the shared shapes and
every route with one line each. The exact fields of each route live in the Go contract types
beside its handler, and the OpenAPI document and TypeScript contracts are generated from
those; there is no second hand-kept specification. Before the first release this file is
edited in place.

Budget: 450 operations. Two tiers under one `/v1`:

- **Client API**: what a player or browsing app needs. This is the part that gets a
  compatibility promise at release.
- **Console API** (paths under `/v1/admin`): what the owner console needs. Same conventions,
  weaker promise: an owner feature may ask for a current app.

The Jellyfin-compatible routes (30 behaviours for tools such as Sonarr, Radarr and Home
Assistant) are a separate short document and are not counted here.

## Conventions

| Topic | Rule |
|---|---|
| Identifiers | Opaque strings (`itm_…`, `prf_…`). The path parameter is always `{id}`. Clients never parse or build one. |
| Lists | `cursor` and `limit` in; `items`, `nextCursor` and, where asked for with `total=true`, `total` out. No `nextCursor` means the end. Cursors are opaque and may expire; an expired cursor is `400 cursor_expired` and the client starts again. |
| Errors | One format everywhere: `{"error": {"code", "message", "retry", "requestId", "fields"?}}` with ordinary HTTP statuses. `code` is an open string; clients act on the status and a short documented list of codes. `retry` is `true` when sending the same request again may work. `Retry-After` is set on `429` and `503`. |
| Repeating a request | `PUT`, `PATCH` and `DELETE` are safe to repeat by design. A `POST` that creates something takes an `Idempotency-Key` header; a repeat returns what was created, and the same key with a different body is `422 idempotency_key_reused`. A `2xx` always means it happened. |
| Edits by two people | `ETag` out and `If-Match` in, only on documents two people or devices edit: owner settings, the metadata editor, shared lists, queues. A stale `If-Match` is `412`. Never on watched, favourite, rating or progress: the last tap wins. |
| Slow work | Reads answer or fail; they never say "not ready". A slow write answers `202` with a `Job` to watch at `/v1/jobs/{id}`. Starting playback and preparing a download may answer `202 preparing`; the client repeats the same request. |
| Changes | `GET /v1/events` is a server-sent event stream with a cursor. An event says "this changed, read it again" and never carries the data. A `resync` event means "reread everything you show" and always has an id. An `access` change means what this profile may see has changed: throw the cache away. |
| Time | Timestamps are RFC 3339 UTC strings ending `At`. Durations and positions are whole milliseconds ending `Ms`. |
| Unknown things | Clients ignore fields, enum values and event types they do not know. Every enum is open. The server ignores unknown request fields. |
| Server-tuned values | The server tells clients how often to report progress, how long to wait before retrying and the largest page it will serve. Clients do not hard-code them. |
| Media and artwork | The server returns addresses that are their own permission, and clients fetch them and never build them. A media address (`/v1/media/{grant}/{file}`) is short-lived. An artwork address (`/v1/images/{id}`) is named by a long hash that only a reply about something the caller may see contains; the picture behind it never changes, so it takes no token and can be cached for good. A client may add `w` and `h` to get it no larger than it will draw it. |
| Sign-in | `Authorization: Bearer <access token>`. Access tokens last 15 minutes; the refresh token rotates on every renewal; a renewal repeated with the same `requestId` gets the same answer; an older refresh token used again ends the session. A token that is no longer good is answered `401` at every address under `/v1`, an unknown one included. |
| Versioning | `/v1` in the path. After release, additions are optional fields and new routes, announced in `capabilities` on `GET /v1/system`. |

## Shared shapes

- **`Error`**: as above.
- **`Page<T>`**: `items`, `nextCursor?`, `total?`.
- **`Item`**: `id`, `kind`, `title`, `sortTitle?`, `year?`, `parentId?`, `rootId?`, `index?`,
  `parentIndex?`, `durationMs?`, `summary?`, `contentRating?`, `ratings?`, `images`, `counts?`
  (children, leaves, unwatched leaves), `state?` (the caller's `ItemState`), and on detail only:
  `versions[]` (each a `Media` with its streams), `tags`, `externalIds`, `markers`.
  One shape for every kind; a new media type is a new `kind` value.
- **`ItemState`**: `watched`, `positionMs?`, `position?` (a book location), `playCount`,
  `lastPlayedAt?`, `favorite`, `inList`, `hidden`, `rating?`.
- **`Library`**: `id`, `name`, `kind`, `views`, `sorts`, `filters` (what this library can be
  browsed by).
- **`Person`**, **`Tag`**, **`Credit`**: small named shapes used by `Item`.
- **`Account`**: `id`, `username`, `role`. **`Profile`**: `id`, `name`, `avatar?`,
  `pinRequired`, `accessMarker` (changes when what the profile may see changes; part of an
  app's cache key and nothing else). **`Device`**, **`Session`** (a sign-in).
- **`Tokens`**: `accessToken`, `expiresAt`, `refreshToken`.
- **`Job`**: `id`, `kind`, `state`, `progress?`, `error?`.
- **`PlaybackSession`**, **`Queue`**, **`List`**, **`Download`**: defined with their areas.

## Client API

### Server (6)

| Route | Purpose |
|---|---|
| `GET /v1/system` | Public facts: name, API major, `capabilities`, sign-in methods, whether setup is needed; the version only for a signed-in caller |
| `POST /v1/system/identity-proof` | Prove this is the server the app pinned |
| `GET /v1/health/live`, `GET /v1/health/ready` | For process managers and proxies |
| `GET /v1/events` | The change feed |
| `GET /v1/attribution` | Required notices for metadata providers |

### Sign-in (16)

| Route | Purpose |
|---|---|
| `POST /v1/auth/sign-in` | Password sign-in; answers tokens, or a two-step challenge |
| `POST /v1/auth/two-factor` | Finish a two-step sign-in |
| `POST /v1/auth/refresh` | Renew tokens |
| `DELETE /v1/auth/session` | Sign out |
| `POST /v1/auth/portico/challenge`, `POST /v1/auth/portico/exchange` | Sign in with a Portico Account |
| `POST /v1/auth/codes`, `GET /v1/auth/codes/{id}`, `POST /v1/auth/codes/{id}/approve`, `POST /v1/auth/codes/{id}/redeem` | Sign in on a TV by approving a code on a phone |
| `GET /v1/auth/invitations/{id}`, `POST /v1/auth/invitations/{id}/accept` | Preview and accept an invitation |
| `POST /v1/auth/password-reset/confirm` | Choose a new password with a reset code. The owner or an admin makes the code in People; the server sends no email, so there is no route that asks for one |
| `POST /v1/auth/password` | Change your password |
| `GET /v1/auth/remembered`, `DELETE /v1/auth/remembered/{id}` | Accounts remembered on this device's sign-in screen |

### Me, profiles and devices (33)

| Route | Purpose |
|---|---|
| `GET /v1/me`, `PATCH /v1/me` | Your account, current profile, role and features; edit account details |
| `GET /v1/profiles`, `POST /v1/profiles` | List and create profiles (at most eight per account). With `member=<id>` an admin manages another person's. |
| `GET /v1/profiles/{id}`, `PATCH /v1/profiles/{id}`, `DELETE /v1/profiles/{id}` | Read, edit, delete (`?preview=true` says what would be lost) |
| `POST /v1/profiles/{id}/select` | Choose a profile, with its PIN if it has one; answers tokens bound to it |
| `PUT /v1/profiles/order` | Reorder |
| `PUT /v1/profiles/{id}/avatar`, `DELETE /v1/profiles/{id}/avatar` | Avatar |
| `PUT /v1/profiles/{id}/pin`, `DELETE /v1/profiles/{id}/pin` | Set, change or remove a PIN. With the account's password, `PUT` replaces a forgotten PIN from any profile. |
| `GET /v1/profiles/{id}/restrictions`, `PUT /v1/profiles/{id}/restrictions` | Rating limit, blocked labels, library access |
| `GET /v1/me/two-factor`, `POST /v1/me/two-factor`, `POST /v1/me/two-factor/confirm`, `DELETE /v1/me/two-factor`, `POST /v1/me/two-factor/recovery-codes` | Two-step sign-in |
| `GET /v1/devices`, `PATCH /v1/devices/{id}`, `DELETE /v1/devices/{id}` | Your devices: list, rename, forget (ends its sign-ins) |
| `GET /v1/devices/{id}/capabilities`, `PUT /v1/devices/{id}/capabilities` | What a device can play |
| `GET /v1/me/sessions`, `DELETE /v1/me/sessions/{id}`, `DELETE /v1/me/sessions` | Your sign-ins: list, end one, end all others |
| `GET /v1/preferences`, `PATCH /v1/preferences` | A profile's own settings: the list of settings with their defaults, what the profile saved for everywhere, for this kind of device and for this device, and the values in force. The last save wins. |
| `/v1/me/tokens`, `/v1/me/app-passwords` | Your personal tokens and app passwords: list, make, revoke |

### Browse, search and Home (23)

| Route | Purpose |
|---|---|
| `GET /v1/libraries`, `GET /v1/libraries/{id}` | Libraries you may see; one library with its views, sorts and filters |
| `GET /v1/libraries/{id}/landing` | The library's landing page, composed by the server |
| `GET /v1/items` | **The one list.** By `library`, `parent`, `person`, `tag`, `list`; `kind`; filters; `sort`; `cursor`, `limit`, `total`. `at=<position>` opens the list at any position in place of a cursor, for a scroll bar or a letter |
| `POST /v1/items/query` | The same list with a structured filter body, for filters too long for a query string |
| `GET /v1/items/{id}` | Detail, with your state |
| `GET /v1/items/{id}/credits`, `GET /v1/items/{id}/related`, `GET /v1/items/{id}/files` | Cast and crew; related titles; file facts |
| `GET /v1/facets` | Counted filter choices for a list. `facet=letter` is the letter rail: each first letter in title order with its count, so a letter's position is the sum of the counts before it |
| `GET /v1/search`, `GET /v1/search/suggestions` | Search across what you may see |
| `GET /v1/search/history`, `DELETE /v1/search/history` | Your recent searches |
| `GET /v1/home`, `GET /v1/home/rows/{id}` | Home, composed by the server; one row paged |
| `GET /v1/home/layout`, `PUT /v1/home/layout` | Which rows you show and in what order |
| `GET /v1/people`, `GET /v1/people/{id}` | People |
| `GET /v1/genres` | Genres with artwork, per library |
| `GET /v1/music/mixes` | Mixes built from your library |
| `GET /v1/images/{id}` | Artwork bytes, sized by `w` and `h` |

### Your state and lists (26)

| Route | Purpose |
|---|---|
| `PUT /v1/items/{id}/state` | **The one state route.** Watched, position, favourite, My List, rating, hidden. On a show, season or album it sets every leaf and answers an undo token. |
| `POST /v1/items/state:batch` | The same for many items (what a device queued while out of reach) |
| `POST /v1/items/state:undo` | Undo a container change within about a minute |
| `GET /v1/history`, `DELETE /v1/history/{id}`, `DELETE /v1/history` | Your play history: list, remove one, clear |
| `GET /v1/lists`, `POST /v1/lists` | Playlists, collections and saved views (`kind`) |
| `GET /v1/lists/{id}`, `PATCH /v1/lists/{id}`, `DELETE /v1/lists/{id}` | Read, rename, delete |
| `POST /v1/lists/{id}/entries`, `PATCH /v1/lists/{id}/entries/{entryId}`, `DELETE /v1/lists/{id}/entries/{entryId}` | Add, move, remove entries. Entries are read with `GET /v1/items?list=`. |
| `GET /v1/lists/{id}/shares`, `PUT /v1/lists/{id}/shares` | Who a list is shared with |
| `GET /v1/jobs/{id}`, `POST /v1/jobs`, `DELETE /v1/jobs/{id}` | Run a bulk action over a selection, watch it, cancel it |

### Playback (34), playing together and to other devices (21), downloads (10), reading (11), Live TV and DVR (19), notices and reports (9)

Listed route by route when each area's definition is written, just ahead of its build. The
resources and their counts are fixed now so the whole product fits the budget:

| Area | Resources |
|---|---|
| Playback | `GET /v1/items/{id}/playback-options`; `/v1/playback/sessions` (start, read, change, stop, report progress, prepare for AirPlay); your sessions on other devices; `/v1/queues` (11, with gapless prepare and commit); subtitles (6); lyrics (4); `/v1/media/{grant}/…` byte families (4) |
| Together | `/v1/groups` (10); `/v1/receivers` (6); Cast sign-in for a receiver (3); transfer a session (2) |
| Downloads | `GET /v1/items/{id}/download-options`; `/v1/downloads` (5); manifest; `POST /v1/downloads/sync` (with the signed offline permission); usage; byte family |
| Reading | Books and issues are items. `/v1/reading/sessions` (2); page, asset and source bytes (3); `/v1/items/{id}/annotations` (4); search inside a book; save the original |
| Live TV and DVR | `/v1/live/channels`, `/guide`, `/now`, programme detail, channel favourites and hiding; reminders (3); `/v1/dvr/recordings` (6); `/v1/dvr/rules` (4). Watching a channel is an ordinary playback session. |
| Notices and reports | Notification inbox (3); feedback reports (2); client log upload (1); app reports (3) |

### Playback, route by route (built so far: 22 of 34, and 4 console routes)

A session is one device playing one title. It lives in memory: after a server restart every
session is unknown (`404`), and the app starts again by itself where it was.

| Route | Purpose |
|---|---|
| `GET /v1/devices/{id}/capabilities`, `PUT /v1/devices/{id}/capabilities` | What a device's players can do, stored as the app sent it. `{id}` may be `current`. |
| `GET /v1/items/{id}/playback-options` | Versions, tracks, the quality menu, where it resumes, and what a start that names nothing would play and how |
| `POST /v1/playback/sessions` | Start. `201` with the session, `200` for a repeat of the same `Idempotency-Key`, or `202` with `retryAfterMs` while the stream gets ready. Ends whatever the same device was playing. |
| `GET /v1/playback/sessions` | What the account is playing on all its devices, and its stream limit. A title the asking profile may not see is unnamed. |
| `GET /v1/playback/sessions/{id}` | The device's own session. An ended one can be read for two minutes and says why it ended. |
| `PATCH /v1/playback/sessions/{id}` | Pause, play, seek, change version, audio, subtitles or quality, or report that the player could not play it |
| `PUT /v1/playback/sessions/{id}/progress` | Where the player is. Answers how long the session now lives and when to report next (10 seconds while playing). `410` when the session is over. |
| `DELETE /v1/playback/sessions/{id}` | Stop, with the final position. Safe to repeat; an unknown session stops fine. Also stops another device of the same account. |
| `POST /v1/queues`, `GET /v1/queues` | Build a queue from a show, season, album, artist, playlist, collection, library view, selection or radio; the queues this device has. A device keeps one music queue and one video queue. The app starts the current entry with `POST /v1/playback/sessions` and `queue`. |
| `GET /v1/queues/{id}`, `PATCH /v1/queues/{id}`, `DELETE /v1/queues/{id}` | The queue with its `ETag`; repeat and shuffle; clear |
| `GET /v1/queues/{id}/entries` | A run of entries: `from` and `limit`, or `around=current` with `before` and `after` |
| `POST /v1/queues/{id}/entries`, `DELETE /v1/queues/{id}/entries/{entryId}`, `POST /v1/queues/{id}/entries/{entryId}/move` | Play next, play later, remove, move. These need `If-Match`; a stale one is `412`. |
| `POST /v1/queues/{id}/advance` | The entry ended, Next, Previous, or jump to an entry |
| `POST /v1/queues/{id}/prepare-next`, `POST /v1/queues/{id}/commit-next` | Get the next song ready before this one ends, then make it the session, for a join without a gap |
| `POST /v1/queues/{id}/playlist` | Save the queue as a playlist |
| `GET /v1/admin/playback/stream-limits`, `PUT /v1/admin/playback/stream-limits` | The owner's stream limits: in total, per person, and for one person |
| `GET /v1/admin/playback/failures`, `DELETE /v1/admin/playback/failures/{id}` | Remembered "this way of playing this file failed on this device", and forgetting one |

## Console API (about 180)

Listed route by route with each console panel's definition. Resources:

| Area | Ops | Resources |
|---|---:|---|
| People and access | 22 | Members; invitations; members' devices; sign-in policy; security summary and unblock; moving between sign-in kinds |
| Setup and remote access | 18 | First-run setup (`POST /v1/setup/owner` exists now, needs the setup code the server logs and writes to `setup-code.txt` in its data folder, and works from any network); remote access; certificate; Portico Account link; `GET`/`PUT /v1/admin/settings/network` (exists now: the owner's home networks, which alone decide local and remote, and the reverse proxies whose `X-Forwarded-For` is believed) |
| Libraries, storage, files | 33 | Libraries, folders, scans and their issues; storage sources; folder browser; missing titles; delete; merge and split |
| Metadata and matching | 18 | `GET`/`PATCH /v1/admin/items/{id}/metadata`; match; refresh; artwork; needs-attention; provider settings |
| Playback and analysis | 17 | Active sessions; play history; hardware and transcode capacity; remembered playback failures; intro and credits review; analysis jobs |
| Live TV and DVR setup | 24 | Sources; live settings; DVR settings; library channels |
| Server operations | 38 | Settings; jobs and schedules; logs; backups; updates; restart; dashboard; storage; diagnostics; notices; feedback triage; resource usage |
| Keys, tools, webhooks | 9 | Server API keys; webhooks; MCP. A person's own tokens and app passwords are under `/v1/me` |

## Not on the wire, on purpose

`viewerFence`, `expectedRevision`, `operationId`, a "preparing" answer on a read, query handles,
lower-bound totals, write lookups after the fact, conflict choices for offline edits, a
revision on personal state, per-provider and per-screen route families, and step-by-step
wizard routes. Each existed for the old storage design. What a person would lose without
them is nothing: the last deliberate edit wins, a `2xx` means it happened, and reads answer.
