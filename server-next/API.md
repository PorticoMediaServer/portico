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
| Lists and their counts | A page of a list never waits for a count. It carries no filter-choice counts at all, and its `total` only when the request asked with `total=true`. A count reads in proportion to the list where a page does not, so an app shows the page from a request without `total`, and fills the total (the same request with `total=true&limit=1`) and each filter's choices (`GET /v1/facets`) from their own requests when those return. A screen must not hold the list back for either. |
| Errors | One format everywhere: `{"error": {"code", "message", "retry", "requestId", "fields"?}}` with ordinary HTTP statuses. `code` is an open string; clients act on the status and a short documented list of codes. `retry` is `true` when sending the same request again may work. `Retry-After` is set on `429` and `503`. |
| Repeating a request | `PUT`, `PATCH` and `DELETE` are safe to repeat by design. A `POST` that creates something takes an `Idempotency-Key` header; a repeat returns what was created, and the same key with a different body is `422 idempotency_key_reused`. A `2xx` always means it happened. |
| Edits by two people | `ETag` out and `If-Match` in, only on documents two people or devices edit: owner settings, the metadata editor, shared lists, queues. A stale `If-Match` is `412`. Never on watched, favourite, rating or progress: the last tap wins. |
| Slow work | Reads answer or fail; they never say "not ready". A slow write answers `202` with a `Job` to watch at `/v1/jobs/{id}`. Starting playback and preparing a download may answer `202 preparing`; the client repeats the same request. |
| Changes | `GET /v1/events` is a server-sent event stream with a cursor. An event says "this changed, read it again" and never carries the data. A `resync` event means "reread everything you show" and always has an id. An `access` change means what this profile may see has changed: throw the cache away. Things that live only in memory (`receiver`, `playback`, `group`) come on the same feed; they are kept two minutes, so a reader away longer, or across a restart, gets a `resync`. The cursor is opaque. |
| Time | Timestamps are RFC 3339 UTC strings ending `At`. Durations and positions are whole milliseconds ending `Ms`. |
| Unknown things | Clients ignore fields, enum values and event types they do not know. Every enum is open. The server ignores unknown request fields. |
| Server-tuned values | The server tells clients how often to report progress, how long to wait before retrying and the largest page it will serve. Clients do not hard-code them. |
| Media and artwork | The server returns addresses that are their own permission, and clients fetch them and never build them. A media address (`/v1/media/{grant}/{file}`) is short-lived. An artwork address (`/v1/images/{id}`) is named by a long hash that only a reply about something the caller may see contains; the picture behind it never changes, so it takes no token and can be cached for good. Each picture is stored in a small and a large size when it arrives (library artwork 800 and 1920 pixels on its longest edge, profile pictures 160 and 512); a client may add `w` and `h`, the size it will draw at, and is served the small size when that is large enough, else the large. Nothing is resized when a screen asks. |
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
  `lastPlayedAt?`, `favorite`, `inList`, `notInterested`, `rating?`.
- **`Library`**: `id`, `name`, `kind`, `views`, `sorts`, `filters` (what this library can be
  browsed by).
- **`Person`**, **`Tag`**, **`Credit`**: small named shapes used by `Item`.
- **`SearchGroup`**: `kind`, `total?`, `totalIsMinimum?`, `items?` or `people?`, `nextCursor?`. A total is
  exact for every kind of title. Only the `person` group of a profile with restrictions can carry
  `totalIsMinimum`: its count stops at 2,000 people and an app then shows "2,000+".
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
| `PUT /v1/profiles/{id}/avatar`, `GET /v1/profiles/{id}/avatar`, `DELETE /v1/profiles/{id}/avatar` | A profile's picture: the body of `PUT` is a JPEG, PNG, GIF or WebP of at most 4 MB; `GET` needs a sign-in to the same account (or `member=<id>` from the owner or an admin) and takes `size=small` or `large` |
| `PUT /v1/profiles/{id}/pin`, `DELETE /v1/profiles/{id}/pin` | Set, change or remove a PIN. With the account's password, `PUT` replaces a forgotten PIN from any profile. |
| `GET /v1/profiles/{id}/restrictions`, `PUT /v1/profiles/{id}/restrictions` | Rating limit, blocked labels, library access |
| `GET /v1/me/two-factor`, `POST /v1/me/two-factor`, `POST /v1/me/two-factor/confirm`, `DELETE /v1/me/two-factor`, `POST /v1/me/two-factor/recovery-codes` | Two-step sign-in |
| `GET /v1/devices`, `PATCH /v1/devices/{id}`, `DELETE /v1/devices/{id}` | Your devices: list, rename, forget (ends its sign-ins) |
| `GET /v1/devices/{id}/capabilities`, `PUT /v1/devices/{id}/capabilities` | What a device can play |
| `GET /v1/me/sessions`, `DELETE /v1/me/sessions/{id}`, `DELETE /v1/me/sessions` | Your sign-ins: list, end one, end all others |
| `GET /v1/preferences`, `PATCH /v1/preferences` | A profile's own settings: the list of settings with their defaults, what the profile saved for everywhere, for this kind of device and for this device, and the values in force. The last save wins. |
| `/v1/me/tokens`, `/v1/me/app-passwords` | Your personal tokens and app passwords: list, make, revoke. A key's secret is shown once and not kept, so a repeat of the same `Idempotency-Key` answers the key made the first time: with a new secret while that key has never been used (the unseen first one stops working), and with no secret and a `note` once it has been used; this holds for `/v1/admin/api-keys` too. |

### Browse, search and Home (21)

| Route | Purpose |
|---|---|
| `GET /v1/libraries`, `GET /v1/libraries/{id}` | Libraries you may see; one library with its views, sorts and filters |
| `GET /v1/libraries/{id}/landing` | The library's landing page, composed by the server |
| `GET /v1/items` | **The one list.** By `library`, `parent`, `person`, `tag`, `list`; `kind`; filters; `sort`; `cursor`, `limit`, `total`. `at=<position>` opens the list at any position in place of a cursor, for a scroll bar or a letter |
| `POST /v1/items/query` | The same list with a structured filter body, for filters too long for a query string |
| `GET /v1/items/{id}` | Detail, with your state |
| `GET /v1/items/{id}/credits`, `GET /v1/items/{id}/related`, `GET /v1/items/{id}/files` | Cast and crew; related titles; where each file is on the server, for the owner and admins only (everyone sees the format facts on the detail) |
| `GET /v1/facets` | Counted filter choices for a list, asked for when someone opens the filter and never with the page (see "Lists and their counts"). `facet=letter` is the letter rail: each first letter in title order with its count, so a letter's position is the sum of the counts before it |
| `GET /v1/search` | Search titles and people you may see, by `q`. Results come in groups by kind, each with its total. With `kind`, one group a page at a time (`cursor`, `limit`), best match first or by `sort` and `order`. `record=true` also keeps the search in your recent searches. `503 search_unavailable` (retry) while the index is being built. |
| `GET /v1/search/suggestions` | The few best titles and names for what has been typed so far |
| `GET /v1/search/history`, `DELETE /v1/search/history` | Your recent searches: read; clear, or remove one with `query` |
| `GET /v1/home`, `GET /v1/home/rows/{id}` | Home, composed by the server in the profile's order without the rows it hid; one row paged. Customize Home saves the settings `home.rowOrder` and `home.hiddenRowIds` with `PATCH /v1/preferences`; the Continue rows cannot be hidden |
| `GET /v1/people`, `GET /v1/people/{id}` | People |
| `GET /v1/genres` | Genres with artwork, per library |
| `GET /v1/music/mixes` | Mixes built from your library |
| `GET /v1/images/{id}` | Artwork bytes: the stored small size when it covers `w` and `h`, else the large |

### Your state and lists (26)

| Route | Purpose |
|---|---|
| `PUT /v1/items/{id}/state` | **The one state route.** Watched, position, favourite, My List, rating, Not interested, and removing a title from Continue Watching until it is played again. On a show, season or album it sets every leaf and answers an undo token. |
| `POST /v1/items/state:batch` | The same for up to 500 items in one transaction (what a device queued while out of reach, or several selected titles), answered title by title |
| `POST /v1/items/state:undo` | Undo a container change within about a minute |
| `GET /v1/history`, `DELETE /v1/history/{id}`, `DELETE /v1/history` | Your finished plays, newest first, by `period` (`24h`, `7d`, `30d`, `90d`, `all`): list, remove one, clear. The owner's play record keeps every play. Removing an entry or clearing sends a `history` event to the profile's other devices |
| `GET /v1/lists`, `POST /v1/lists` | Playlists, collections and saved views (`kind`) |
| `GET /v1/lists/{id}`, `PATCH /v1/lists/{id}`, `DELETE /v1/lists/{id}` | Read, rename, delete |
| `POST /v1/lists/{id}/entries`, `PATCH /v1/lists/{id}/entries/{entryId}`, `DELETE /v1/lists/{id}/entries/{entryId}` | Add, move, remove entries. Entries are read with `GET /v1/items?list=`. |
| `GET /v1/lists/{id}/shares`, `PUT /v1/lists/{id}/shares` | Who a list is shared with |
| `GET /v1/jobs/{id}`, `POST /v1/jobs`, `DELETE /v1/jobs/{id}` | Run a bulk action over a selection, watch it, cancel it |

### Playback (34)

The rest of playback is listed route by route as it is built. Its resources and count are
fixed so the whole product fits the budget:

| Area | Resources |
|---|---|
| Playback | `GET /v1/items/{id}/playback-options`; `/v1/playback/sessions` (start, read, change, stop, report progress, prepare for AirPlay); your sessions on other devices; `/v1/queues` (11, with gapless prepare and commit); subtitles (6); lyrics (4); `/v1/media/{grant}/…` byte families (4) |

Setup, playing together, downloads, reading, Live TV, notices and tools are listed route by
route after playback, each route with who may call it. How each area is built is in
`AREAS.md`.

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
| `GET /v1/media/{grant}/{file}` | The bytes a session plays, at the address the session gave: the file itself by byte range (`original`), or a conversion's playlists and segments (`master.m3u8` and what it names). No sign-in header: the grant is the permission, and it dies when the session stops or changes stream. Anything else is `404`. |
| `GET /v1/media/subtitles/{grant}/{track}/{n}.vtt` | A text subtitle of the session's title as WebVTT, at the address the session gave in `presentation.subtitles`; `?segment=N` is one minute of it, which is what the stream's own playlist names. Same permission and same `404` as above. |
| `GET /v1/items/{id}/subtitles`, `PUT /v1/items/{id}/subtitles/offset` | A title's subtitles (in the file, or beside it) and the timing nudge this profile gave them, which is kept for that title |
| `GET /v1/items/{id}/lyrics` | A song's lyrics from files beside it, timed lines in order |
| `GET /v1/admin/playback/stream-limits`, `PUT /v1/admin/playback/stream-limits` | The owner's stream limits: in total, per person, and for one person |
| `GET /v1/admin/playback/failures`, `DELETE /v1/admin/playback/failures/{id}` | Remembered "this way of playing this file failed on this device", and forgetting one |

### Setup and the kind of sign-in (5 new)

The Console half is under "Setup, remote access and sign-in kind" below. Setup holds nothing
until it finishes: the first to finish with the setup code is the owner.

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/system` | Anyone | Listed above. Adds `signIn` (`portico` or `local`, from the `sign_in_kind` setting), `move` (`to_portico`, `to_local` or absent: a switch has people waiting) and `atHome` |
| `POST /v1/setup/owner` | Anyone with the setup code, while there is no owner | Exists. Local sign-in: server name, username, password; answers tokens |
| `POST /v1/setup/portico`, `POST /v1/setup/portico/finish` | Anyone with the setup code, while there is no owner | Portico Accounts: name and code in, the approval address out; then the approval that came back in, tokens out. Nothing is saved between the two |
| `POST /v1/auth/move/check`, `POST /v1/auth/move/complete` | A person whose account is waiting to move | An old password or a move code in, the username and the next step out; then a Portico Account, or a new username and password, in and tokens out. A wrong, used or expired code has one answer |
| `POST /v1/me/leave` | A member of a Portico Accounts server (not the owner) | Leave the server, with "also delete my profiles and history" |

Not carried: `GET /v1/setup/state`, `POST /v1/setup/browser`, `/app`, `/remote-check`,
`/abandon`, `/retry` and `/start-over` (setup holds nothing to resume or abandon);
`/v1/networking/claim/*` and `/v1/auth/portico-return/exchange` (the two setup and switch
steps above); `/v1/networking/connection` (`atHome` on `GET /v1/system`). Linking a TV with a
Portico Account is between the app and Portico Services.

### Playing together and on other devices (21)

All of it is memory: a restart ends every group and every move in flight. Changes arrive on
`GET /v1/events` as "this group changed" or "this device is being asked"; the app rereads.
Nothing here takes `If-Match`: the last action wins.

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/groups`, `POST /v1/groups` | A profile allowed Watch Together | Groups this profile is in; start one (name, your name in it, who controls) |
| `POST /v1/groups/join` | A profile allowed Watch Together | Join with a code. A code that did not work has one answer |
| `GET /v1/groups/{id}` | A member of the group | The room: what is on, the shared clock, members and whether each is in step, the queue, the invitation code, what this member may do. A title this member may not see has no name or picture |
| `PATCH /v1/groups/{id}` | The host; any member in step for "become host" once the host has been gone two minutes | Who controls; make someone host; become host; a new invitation code |
| `DELETE /v1/groups/{id}` | The host | End for everyone |
| `POST /v1/groups/{id}/playback` | Whoever may control | Play, pause, seek, watch this entry, go ahead without someone |
| `PUT /v1/groups/{id}/queue` | Whoever may control | The whole queue as it should now be (at most 256 titles) |
| `PUT /v1/groups/{id}/me`, `DELETE /v1/groups/{id}/me` | A member | Where this member is and whether it is ready, which also keeps it present; leave |
| `GET /v1/receivers` | Any signed-in profile | The account's open devices that accept playback, at home or away, and what each is playing. Cast and AirPlay targets are found by the app, not listed here |
| `PUT /v1/receivers/current`, `DELETE /v1/receivers/current` | A signed-in device | This device accepts playback from the account's other devices, or stops |
| `POST /v1/receivers/{id}/requests`, `GET /v1/receivers/{id}/requests/{requestId}`, `DELETE /v1/receivers/{id}/requests/{requestId}` | A device of the same account | Ask a device to play (title, place, audio and subtitle picks, queue) or to be controlled; see allowed, declined, playing or failed; cancel. The first device keeps playing until the second is, and a move not playing in two minutes has failed |
| `GET /v1/receivers/current/requests`, `PATCH /v1/receivers/current/requests/{requestId}` | The receiving device | What is being asked; allow, decline, report playing or failed. The device itself remembers which devices it allowed |
| `GET /v1/cast/config` | Any signed-in profile, on a Portico Accounts server | The Cast application id. A local server answers `404` and has no `cast` capability |
| `POST /v1/cast/codes` | Any signed-in profile, on a Portico Accounts server | A pairing code for the TV the sender is casting to |
| `POST /v1/cast/redeem` | The Cast receiver, with the code | A sign-in that can play what the casting profile may see and nothing else (session scope `cast`), renewed by the ordinary refresh; it ends when the account signs it out or after a day unused |

Controlling a device afterwards is the ordinary `PATCH` and `DELETE /v1/playback/sessions/{id}`.
`DELETE` stops any of the account's sessions, as the stream limit screen needs. `PATCH` on a
session of another device, once that device allowed this one, does not change the session: it
sets the session's `command` (state, seek, skip next or previous, audio, subtitles), the device
hears of it on its feed and does it through its own routes. The device moving playback in
starts with `requestId`, so the two devices count as one stream until it reports playing. The
queue moves with `POST /v1/queues` and `source.queue`, another queue of the account. "Play from
other devices" is the device setting `devices.playFromOtherDevices`. AirPlay is
`POST /v1/playback/sessions/{id}/airplay`, counted with playback.

Not carried: `/v1/handoffs/*` (the request above is the move, and it is not stored);
`/v1/receivers/{id}/grants` (permission is remembered by the device that gives it);
`/v1/receivers/{id}/heartbeat` and `/inbox` (`PUT /v1/receivers/current` and the events feed);
`GET /v1/groups/{id}/events` (the one events feed); the separate group `heartbeat`,
`readiness`, `transport`, `settings`, `invites`, `host-transfer`, `leave` and `end` routes
(folded into the routes above); `/v1/cast/bootstrap`, `/controllers/*`, `/preview` and
`/reconnect` (taking over a TV is settled on Google's own channel between sender and TV).

### Downloads (10)

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/items/{id}/download-options` | A profile allowed to download, for a title it may play | The real choices with the size of each ("about" when estimated), which need the server to prepare a copy first, and why a choice is not offered; with `selection` and `keepNext`, for a show, season, album, artist or playlist as a whole; the room left in the owner's download space |
| `GET /v1/downloads`, `POST /v1/downloads` | A profile allowed to download | This device's requests, or with `scope=profile` all of the profile's (the browser page); ask for one. `201`, or `200` for the same thing asked twice. A person limited to home can ask only at home |
| `GET /v1/downloads/{id}` | The profile that asked | One request with its titles and the state of each: waiting, preparing with progress, ready, paused, expired, could not be prepared or included, with the reason |
| `PATCH /v1/downloads/{id}` | The profile that asked | Pause, resume, retry; change or remove "keep the next" |
| `DELETE /v1/downloads/{id}` | The profile that asked | Remove the request. The server's prepared copy stays for anyone else who asked for it |
| `GET /v1/downloads/{id}/items/{itemId}` | The profile that asked | What to fetch for one title: its details, and the addresses, sizes and hashes of its file or files, artwork, subtitles, chapters and markers, and lyrics |
| `POST /v1/downloads/sync` | A signed-in device | The device says which titles it holds. The answer says which to remove (the profile may no longer see them, or may no longer download), which to add or drop under "keep the next", and until when everything plays offline (30 days from now) |
| `GET /v1/downloads/usage` | A profile allowed to download | Space the profile's prepared copies take |
| `GET /v1/media/{grant}/…` | Whoever holds the address | The bytes, by range, so a transfer carries on where it stopped. The grant is in the answer of the items route and is short-lived |

Offline plays, places and watched marks come back through `POST /v1/items/state:batch`, like
any change made out of reach. In a browser a ready title is saved through the same media
address.

Not carried: `/v1/downloads/receipts*`, `/receipt-keys`, `/revocations` and
`/preparations/{id}/grant` (signed offline permissions: the app enforces the 30 days, and
`sync` says what goes); `/v1/downloads/preparations*` and `/requests*` as two resources (one
`/v1/downloads`); `POST /v1/downloads/progress` (`state:batch`); `GET`/`PUT
/v1/downloads/settings` (the `downloads` settings document); seek previews inside a download.

### Reading (8)

A book is an item: its page is `GET /v1/items/{id}`, its place and read mark go through `PUT
/v1/items/{id}/state`, "mark all read" is a job over a selection, and its lists are `GET
/v1/items`. Nothing is "open" on the server. Search inside the open book and the highlights
export are done by the app.

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/items/{id}/book` | A profile that may see the book | How to read it: the version chosen (or `version=`), its kind, contents and page list, the addresses of its parts, the fingerprint of the file, and whether the caller may save the file. A book the server cannot open says why |
| `GET /v1/media/{grant}/pages/{n}` | Whoever holds the address | One comic or image page, no wider than `w`. A page too large to prepare safely is `422` and the rest of the book reads |
| `GET /v1/media/{grant}/parts/{path}` | Whoever holds the address | One part of an EPUB, named in the book's own list of parts and nothing else |
| `GET /v1/media/{grant}/source` | Whoever holds the address; saving needs a profile allowed to download | The file's own bytes by range: a PDF read in the app, and Save the file at any size |
| `GET /v1/items/{id}/annotations` | The profile, for a book it may see | Its bookmarks, highlights and notes in the book, with those deleted since `since=` |
| `PUT /v1/items/{id}/annotations/{annotationId}`, `DELETE /v1/items/{id}/annotations/{annotationId}` | The profile | Add or change one (the app makes the id, so a repeat changes nothing; the 5,001st is refused); delete one everywhere |
| `POST /v1/annotations:batch` | The profile | What a device made while out of reach; the later edit to one id wins |

Not carried: `/v1/reading/sessions*` (nothing is open on the server; bytes are media
addresses); reading `progress` routes and `/v1/reading/offline/progress*` (the one state
route; the later place wins and nothing is refused); `/v1/items/{id}/reading/mark` and
`/v1/libraries/{id}/reading/mark-all` (the state route and a job); `/v1/reading/items`,
`/discover`, `/continue`, series and periodical routes (the item list, the library landing and
Home); the reading `my-list`, `favorite` and saved-group routes (the state route);
`GET /v1/reading/search` across every book (put off until someone asks).

### Live TV and recordings (18)

Watching a channel is `POST /v1/playback/sessions` with `channel`; pause, rewind and Go to
live are the ordinary `PATCH`. "Every tuner is busy" is `409 no_tuner` naming what is using
them. A countdown before a stream is taken for a recording arrives as a session change.
Channel logos and programme pictures are `/v1/images/{id}` addresses. A recorded programme is
a title in the Recorded TV library: it is listed with `GET /v1/items?library=` and played,
marked and deleted like any title.

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/live/channels` | A profile allowed Live TV | Channels it may watch: by source, group, favourites, with its hidden ones on request; by number or name; `around=` for the mini guide. The sources and groups to filter by come with the first page |
| `GET /v1/live/guide` | A profile allowed Live TV | Programmes for a run of channels and a stretch of time, and whether the guide is out of date |
| `GET /v1/live/now` | A profile allowed Live TV | On now: favourites first, then by number, with what is on and next |
| `GET /v1/live/programmes/{id}` | A profile allowed Live TV | One programme, its other airings, and whether it is booked or has a reminder. One the profile's rating limit hides says so and cannot be watched or recorded |
| `PUT /v1/live/channels/{id}/state` | A profile allowed Live TV | Favourite and hide, for this profile |
| `GET /v1/live/reminders`, `PUT /v1/live/reminders/{id}`, `DELETE /v1/live/reminders/{id}` | A profile allowed Live TV | Reminders; `{id}` is the programme. The app shows them |
| `GET /v1/dvr` | A profile allowed Live TV | Counts (upcoming, recorded, unwatched, conflicts), space used and free, about how many hours are left, the keep rule in force |
| `GET /v1/dvr/recordings`, `POST /v1/dvr/recordings` | Read: a profile allowed Live TV. Book: a profile allowed to record | Upcoming (conflicts first) and History by `state`, the profile's own or with `scope=everyone` all of them; book an airing. An airing someone else already booked answers that booking and adds this profile to it, so it is still recorded if the first person cancels |
| `GET /v1/dvr/recordings/{id}`, `PATCH /v1/dvr/recordings/{id}`, `DELETE /v1/dvr/recordings/{id}` | Read: as above. Change and delete: the profile that booked it, the owner and administrators | One booking with what it clashes with and its size; change padding or priority, Keep, "Record this instead" or "Skip this one"; cancel (the booking passes to the next profile that asked for it, if any), stop (what was captured is kept), or delete the recording and its file |
| `GET /v1/dvr/rules`, `POST /v1/dvr/rules` | A profile allowed to record; the owner for any profile with `profile=` | Series rules, the profile's own or with `scope=everyone` all of them for the owner; make one (`?preview=true` answers what it would record and makes nothing) |
| `PATCH /v1/dvr/rules/{id}`, `DELETE /v1/dvr/rules/{id}` | The profile that made it; the owner | Edit, pause, resume; delete, with `bookings=keep` or `cancel` |

Not carried: `/v1/guide/sources` (comes with the channel list); `/v1/dvr/groups` (recorded
titles are the item list); `/v1/dvr/recordings/{id}/delete-preview`, `/cancel` and `/keep`
(the read, `DELETE` and `PATCH` above); `GET /v1/dvr/channels` (the channel list);
`/v1/dvr/storage` and `/v1/admin/dvr/recording-root` (the `dvr` settings document);
`/v1/channels/preferences` and `/v1/library-channels/preferences` (one channel state route);
the seven recording controls the old server never saved.

### Notices and reports (5)

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/notifications` | Any signed-in profile | Its notices: unread, all or archived, with the unread count |
| `PATCH /v1/notifications/{id}`, `POST /v1/notifications/read-all` | Any signed-in profile | Mark read or unread, archive; mark all read |
| `POST /v1/reports` | Any signed-in profile | Report a problem, or send a crash report, with what was playing and, if the person agrees, the app's log (at most 5 MB) |
| `GET /v1/reports` | Any signed-in profile | Its own reports and the owner's replies |

Not carried: `/v1/notifications/capabilities`, `/inbox`, `/unread-count` and `/inbox/actions`
(the three routes above); a separate client log upload and app report routes (`POST
/v1/reports`).

### For tools (2)

A key, token or app password is turned into its person and profile in one place, where it is
also refused whenever and wherever that person could not sign in (outside viewing hours, away
from home for an account limited to home, a blocked network, an account turned off or
waiting). After that a tool uses the same routes as the apps, within what its key may do
(Read, Play, Libraries, Administer). No key can make, change or revoke keys, change a
password, PIN or two-step sign-in, change who is owner or administrator, or restore a backup.

| Route | Who | Purpose |
|---|---|---|
| `POST /v1/libraries/changed` | A key allowed Libraries; the owner and administrators | Files changed at these paths: the folders are scanned, and a path outside every library is ignored |
| `POST /v1/mcp` | A key, while "Assistants" is on | The assistant address. Tools offered: find and read titles, see what is playing, mark watched, scan and report changed files, list unmatched titles, read the guide and recordings, and with Administer read status, storage and recent messages. Nothing that deletes or reconfigures |

The Jellyfin-compatible routes (30 behaviours, their own document, off until the owner turns
them on) sign in with an app password and nothing else; a tool that then asks for "a key" is
given access equal to that app password. DLNA (one switch in the `remote-access` document, off by default) answers on its own
UPnP addresses outside `/v1`, to the home networks only, never through a proxy, and serves
original files with no sign-in.

## Console API (about 180)

Listed route by route with each console panel's definition. Resources:

| Area | Ops | Resources |
|---|---:|---|
| People and access | 22 | Members; invitations; members' devices; sign-in policy; security summary and unblock; moving between sign-in kinds |
| Setup and remote access | 14 | Listed below (10); `GET`/`PUT /v1/admin/settings/network` (exists now: the owner's home networks, which alone decide local and remote, and the reverse proxies whose `X-Forwarded-For` is believed); `GET /v1/admin/blocked-networks` and `DELETE /v1/admin/blocked-networks/{id}` (exist now: the networks blocked for wrong guesses, and unblocking one). First-run setup is in the Client API (`POST /v1/setup/owner` exists now, needs the setup code the server logs and writes to `setup-code.txt` in its data folder, and works from any network) |
| Libraries, storage, files | 33 | Libraries, folders, scans and their issues; storage sources; folder browser; missing titles; delete; merge and split |
| Metadata and matching | 18 | `GET`/`PATCH /v1/admin/items/{id}/metadata`; match; refresh; artwork; needs-attention; provider settings |
| Playback and analysis | 10 | Stream limits; remembered playback failures; intro and credits review; analysis jobs. Active sessions, Play history and hardware are listed with Server operations |
| Live TV and DVR setup | 27 | Listed below: sources, channels, tuners, Library Channels |
| Server operations | 42 | Listed below: the settings documents (3), the Dashboard and operations (37), downloads by person (2) |
| Keys, tools, webhooks | 10 | Listed below: server API keys and webhooks. The assistant address and "files changed" are in the Client API. A person's own tokens and app passwords are under `/v1/me` |

### Metadata and matching, route by route (16 of 18)

For the owner and administrators. Provider keys and a library's source are the owner's alone.
A library's provider, language and region are options of the library, set on its own routes.
Refreshing or editing a selection of titles is a bulk action, `POST /v1/jobs`.

| Route | Purpose |
|---|---|
| `GET /v1/admin/items/{id}/metadata`, `PATCH /v1/admin/items/{id}/metadata` | The editor's document with its `ETag`: each field and list with its value, where it came from (provider, file, edit) and whether it is locked. Edit fields and lists, which locks them; lock or unlock a field, a list, the match or everything. Needs `If-Match`; a stale one is `412`. A value out of range is `422` naming the field and its limit |
| `GET /v1/admin/items/{id}/metadata/history`, `POST /v1/admin/items/{id}/metadata/history/{entryId}/undo` | The last 20 changes people made, newest first; put the title back to how it was before one |
| `POST /v1/admin/items/{id}/metadata/refresh` | Ask the provider again now. Replaces only what is not locked and did not come from the files |
| `GET /v1/admin/items/{id}/matches` | Possible matches with their posters: the closest by themselves, a search by `q` and `year`, or the one a provider id or address in `ref` names. Asks the provider; `502 provider_unavailable` when it does not answer |
| `PUT /v1/admin/items/{id}/match` | Use a match, with a show's episode order and number offset; or Keep as is. The title keeps its id, everyone's state, locked fields and chosen pictures. Answers when the title's own details are in; a show's episodes follow in the background. A match the library already has merges the two |
| `POST /v1/admin/items/{id}/merge` | Merge with another title of the same kind in the same library (counted with Libraries, storage, files) |
| `GET /v1/admin/libraries/{id}/unmatched` | Unmatched items: the library's titles looked up without a confident answer, by title, each with a plain reason and up to three closest matches. No count |
| `GET /v1/admin/items/{id}/artwork` | The pictures a title could use for a `role`: beside the media, inside the file, the provider's, uploads, and a frame from the video |
| `POST /v1/admin/items/{id}/artwork` | Upload a JPEG, PNG or WebP of at most 10 MB for a role; it is used and locked |
| `PUT /v1/admin/items/{id}/artwork/{role}` | Use this picture for the role, which locks it; or unlock the role |
| `DELETE /v1/admin/items/{id}/artwork/{imageId}` | Remove an upload; the next best picture is used |
| `POST /v1/admin/items/{id}/artwork/find` | Find more from the provider, fetch again, or make an episode's picture from the video |
| `GET /v1/admin/metadata/settings`, `PUT /v1/admin/metadata/settings` | Own provider keys (write-only: the answer says only whether one is set), Include adult titles, cast photos, Download title data, the subtitle account |

### Remote sources, route by route (8 of the 33 for libraries, storage and files)

The owner's alone. A source's password or key is never sent back; it can only be replaced.

| Route | Purpose |
|---|---|
| `GET /v1/admin/remote-sources`, `GET /v1/admin/remote-sources/{id}` | The sources with their kind, settings, preset, limits and state (connected, unreachable, sign_in, waiting, setup) |
| `POST /v1/admin/remote-sources` | Add one: the connection is tested first and nothing is saved unless it works. Takes an `Idempotency-Key` |
| `PATCH /v1/admin/remote-sources/{id}` | Rename, change the preset or limits, change the address or replace the password (tested first; a new host needs the password again) |
| `DELETE /v1/admin/remote-sources/{id}` | Disconnect, once no library folder is on it (`409 source_in_use` otherwise) |
| `POST /v1/admin/remote-sources/{id}/test` | Test the saved source now |
| `GET /v1/admin/remote-sources/{id}/folders?path=` | The folders in one folder of the source, for choosing a library's folders |
| `POST /v1/admin/remote-sources/drive-sign-in` | Start a Google sign-in with the owner's own OAuth client; the address the browser is sent to is pasted into the Drive source's settings |

A source is `webdav` (an address, user and password), `rclone` (one remote pasted from rclone's configuration, and a folder in it) or `drive` (Google Drive with a service account key or the sign-in above, and a folder in it). rclone and Drive are served by the rclone that comes with Portico.

### Libraries, scans and files (15 so far)

Only the owner. A key made for a tool needs the `library` scope to scan and delete.

| Route | Purpose |
|---|---|
| `GET /v1/admin/libraries`, `POST /v1/admin/libraries` | The libraries with their folders and options; make one (kind, name, folders, language, options), which starts its first scan |
| `GET /v1/admin/libraries/{id}`, `PATCH /v1/admin/libraries/{id}` | One library; rename it, change its options, give the whole list of its folders (an added folder is scanned, a removed folder's titles leave) |
| `DELETE /v1/admin/libraries/{id}?confirm=<name>` | Remove the library; answers `202` with a job. Files on disk are not touched |
| `POST /v1/admin/libraries/{id}/scan`, `DELETE …/scan`, `GET …/scan` | Scan now, or Scan all files with `allFiles` (`202` with a job; joins a scan already asked for), cancel, and the scan's state, counts and folders |
| `GET /v1/admin/libraries/{id}/skipped` | Files the last scan could not use, 40 at a time, each with a reason |
| `GET /v1/admin/libraries/{id}/missing`, `DELETE …/missing?confirm=<count>` | The library's trash, with when each title will be removed; Empty trash |
| `GET /v1/admin/items/{id}/deletion`, `DELETE /v1/admin/items/{id}?confirm=<title>` | What deleting a title would take; delete its files from disk and remove it |
| `DELETE /v1/admin/versions/{id}` | Delete one version's files from disk |
| `GET /v1/jobs/{id}` | Watch a job (listed with the client API above) |
### Owner settings: one mechanism (3)

Every owner setting lives in a named settings document, one row of `settings`, saved whole
with its `ETag`. Each area owns its documents and every other area reads them from memory, so
a change applies to the next play or the next job with no restart. A secret inside a
document is write-only: the answer says only whether it is set.

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/admin/settings` | Owner; a TV sees values and no secrets | The documents there are, each with its `ETag` |
| `GET /v1/admin/settings/{name}`, `PUT /v1/admin/settings/{name}` | Owner | One document: each value, its default, and whether the config file sets it (then it cannot be changed here). `PUT` needs `If-Match`; a stale one is `412` with the current document. A value out of range is `422` naming the field |

Documents: `general` (server name, when heavy work runs, background speed, update check),
`streaming` (Convert when needed, quality and music away from home, upload speed, which
playback reads when a play starts away from home and may refuse it for, the
conversion folder, the graphics device, hardware decoding, conversion limits, End paused video
after), `storage` (Keep generated files, the free-space warnings), `backups`, `logs`,
`notices` (who gets server notices, keeping times for notices and Play history), `downloads`
(on or off, highest quality, space, how long prepared copies are kept), `live` and `dvr`,
`remote-access` (remote access on or off, Remote security, Secure connections, port mapping,
the public port, IPv6, your own address and certificate files, who may sign in from outside,
Find this server at home, and the DLNA switch), `integrations` (Assistants, the compatible
routes, the cap on members' tokens). Already built under their own addresses and kept there: `network`
(`/v1/admin/settings/network`), provider keys (`/v1/admin/metadata/settings`), stream limits
(`/v1/admin/playback/stream-limits`).

### Setup, remote access and sign-in kind, route by route (10)

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/admin/sign-in` | Owner | The kind of sign-in, the owner, a switch in progress and who is waiting |
| `POST /v1/admin/sign-in/preview` | Owner, from the main profile, on the web | What a switch or Start over would do, with this server's real numbers. Changes nothing |
| `POST /v1/admin/sign-in/switch` | Owner, from the main profile, on the web | Confirm: a username and password for local sign-in, or the approval that came back for Portico Accounts; the server's name typed for Start over. Everyone is signed out |
| `POST /v1/admin/sign-in/finish` | Owner | Finish now: accounts still waiting are turned off as "Didn't move" |
| `POST /v1/admin/members/{id}/move-code`, `POST /v1/admin/sign-in/move-codes` | Owner and administrators | A single-use code or link for one waiting person, valid 7 days and shown once; for everyone waiting |
| `POST /v1/admin/owner` | Owner, with the password or a new Portico approval | Change owner; the previous owner becomes an administrator, a member, or is removed |
| `GET /v1/admin/remote-access` | Owner | What is true now: the Portico address, the certificate and its expiry, the last error, reachable from outside or not as just checked, outside addresses refused under Enhanced, this device at home or away, the identity code. The switches are the `remote-access` settings document |
| `POST /v1/admin/remote-access/check` | Owner | Check now; Try again |
| `POST /v1/hosted/notices` | Portico Services, signed with a key this server trusts | "This account left", "this approval was decided". Refused on a local server |

Not carried: `/v1/admin/sign-in/move*` as seven routes (the four above);
`/v1/direct/ownership` (`POST /v1/admin/owner`); `/v1/networking/remote*` and
`/v1/networking/certificate*` (one status read and one settings document; the per-protocol
router switches, router address and renewal time are gone); `/v1/admin/connectivity/policy`
and `/status` (the `remote-access` and `network` documents, and the status read).

### Downloads, the owner's side (2)

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/admin/downloads` | Owner | Downloads by person: how many titles were asked for and how much space the server's copies take, each copy counted once |
| `DELETE /v1/admin/members/{id}/downloads` | Owner | Remove their downloads: their requests go and the server's copies nobody else asked for are freed. It does not reach into a device |

Not carried: `POST /v1/admin/downloads/revoke` and `/cache/release` as two removals (one
removal here; cutting someone off is the Downloads switch on the account or the profile).

### Live TV and recordings setup, route by route (27)

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/admin/live/sources`, `POST /v1/admin/live/sources` | Owner | Sources with their state in plain words; add one (a playlist by address or upload, an Xtream sign-in, an HDHomeRun tuner, or a guide alone) |
| `POST /v1/admin/live/sources/preview` | Owner | What a source would bring in and leave out, and why; saves nothing |
| `POST /v1/admin/live/sources/discover` | Owner | Tuners found on the home network |
| `GET /v1/admin/live/sources/{id}`, `PATCH /v1/admin/live/sources/{id}`, `DELETE /v1/admin/live/sources/{id}` | Owner | Read, change (pause, audience, interval, which channels come in, numbering, tuner count; needs `If-Match`), remove with its channels, rules, upcoming recordings and reminders |
| `POST /v1/admin/live/sources/{id}/refresh` | Owner | Refresh guide now; answers a job |
| `GET /v1/admin/live/sources/{id}/channels`, `PATCH /v1/admin/live/sources/{id}/channels` | Owner | Its channels, with those not brought in; set number, name, listings and hidden for many rows at once |
| `GET /v1/admin/dvr/tuners` | Owner | Tuners in use now and clashes in the next 12 hours |
| `GET /v1/admin/library-channels`, `POST /v1/admin/library-channels` | Owner | Library Channels (at most 1,000) in the owner's order, a page at a time, each with one sentence saying what it plays, how many titles it chooses from, how far its schedule reaches and its state; make one |
| `PUT /v1/admin/library-channels/order` | Owner | The owner's order, which is the channels' order everywhere |
| `GET /v1/admin/library-channels/templates` | Owner | The nine templates, each a whole channel filled in for this server to review, or off with "Not enough media" when fewer than three titles match |
| `POST /v1/admin/library-channels/preview` | Owner | For a channel as it is being edited: how many titles match and about how many hours that is, some of what plays, how many have no known length, and today's schedule exactly as saving would make it, blocks included |
| `GET /v1/admin/library-channels/{id}`, `PATCH /v1/admin/library-channels/{id}`, `DELETE /v1/admin/library-channels/{id}` | Owner | The whole builder with its `ETag`, its state and its health (on the air or one sentence why not, how far the schedule reaches, how many programmes, how many titles it could not place); save it or turn it on or off (needs `If-Match`; at most 500 blocks, 500 titles in each pinned list and 2 MB): the rest of the schedule is built again from the end of the programme now on; delete |
| `POST /v1/admin/library-channels/{id}/regenerate` | Owner | Regenerate schedule: build the rest again from the end of the programme now on; answers a job |
| `PUT /v1/admin/library-channels/{id}/logo`, `DELETE /v1/admin/library-channels/{id}/logo` | Owner | Upload its logo (up to 10 MB, 16 to 4,096 pixels a side, stored small) or go back to the monogram |
| `GET /v1/admin/library-channels/presets`, `POST /v1/admin/library-channels/presets` | Owner | Saved block presets (at most 1,000), a page at a time; make one |
| `PATCH /v1/admin/library-channels/presets/{id}`, `DELETE /v1/admin/library-channels/presets/{id}` | Owner | Change one (needs `If-Match`); delete one. Adding a block from a preset copies it: changing or deleting a preset never changes a channel |

Everyone's series rules are `GET /v1/dvr/rules?scope=everyone`. Live TV and recording
settings (guide length, rewind stretch, retry time, logos; the recordings folder, paddings,
the keep rule, the space floor and cap, what a tuner clash does) are the `live` and `dvr`
settings documents.

Not carried: `/v1/admin/live-sources/remote/preview` and `/remote/save` (one preview, one
create); `/{id}/enabled`, `/remove`, `/logos`, `/configuration` and `/channel-map` (the
source's `PATCH`, `DELETE` and channels routes); `/v1/admin/dvr/recording-owners`,
`/recording-permissions` and `/recording-groups` (the account and profile switches, and the
rules list); `/v1/admin/library-channels/defaults` and `/{id}/health` (templates; the read says what is
wrong); a priority between overlapping blocks, a
second audience setting and a stored seed (blocks never overlap, and who sees a channel
follows its libraries).

### Server operations and the Dashboard, route by route (37)

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/admin/dashboard` | Owner | Alerts (each one line with one action, present while its cause lasts), totals, tasks running |
| `GET /v1/admin/sessions`, `DELETE /v1/admin/sessions/{id}` | Owner and administrators | Now playing, a problem first; stop one with a message of up to 500 characters |
| `GET /v1/admin/load` | Owner | Bandwidth, processor, memory and conversions, `range=live` or `24h`. Kept in memory; starts again after a restart |
| `GET /v1/admin/statistics` | Owner | The server's whole history in daily totals: plays, watch time, bandwidth at home and away, conversions, by `metric` and a range of days |
| `GET /v1/admin/play-history`, `GET /v1/admin/play-history/summary` | Owner and administrators | Every play, newest first, filtered by person, library, kind, text and period; top people, busiest times and top played over a chosen range. The list is in time order only. A filter by kind or text, and the summary, read the plays of the range: an accepted cost on this page, so the summary answers from at most the newest million plays of its range and says when it stopped there |
| `GET /v1/admin/jobs`, `POST /v1/admin/jobs/{id}/retry` | Owner | Tasks running, and finished or failed in the last 30 days; run a failed one again. Cancel is `DELETE /v1/jobs/{id}` |
| `PUT /v1/admin/background/pause`, `DELETE /v1/admin/background/pause` | Owner | Pause heavy work and scheduled backups for an hour; resume. A restart ends it |
| `GET /v1/admin/hardware`, `POST /v1/admin/hardware/test` | Owner | Graphics devices, the formats each handles, tone mapping, the last test and error; test now, with no restart |
| `GET /v1/admin/storage`, `POST /v1/admin/storage/cache/empty` | Owner | What uses space, folder by folder; empty the cache of what Portico can rebuild, with no restart |
| `GET /v1/admin/backups`, `POST /v1/admin/backups`, `DELETE /v1/admin/backups/{id}` | Owner | The backups in the backup folder, and which working credentials a backup holds (the server's keys, source sign-ins, a remote source's key file); back up now (a job); delete one |
| `POST /v1/admin/backups/inspect`, `POST /v1/admin/backups/restore` | Owner, not on a TV; restore also needs the password or PIN again | Check a listed backup or a path whole before restoring; restore (a job, then a restart; everyone signs in again). One that fails its check is refused whole |
| `DELETE /v1/admin/upgrade-copy` | Owner | Remove the copy kept before the last upgrade |
| `GET /v1/admin/updates`, `POST /v1/admin/updates/check` | Owner | This version, the newest, and how to update this kind of install; check now. Nothing is installed |
| `POST /v1/admin/restart` | Owner | Restart; answers how many are playing unless `confirm=true` |
| `GET /v1/admin/logs`, `GET /v1/admin/logs/events`, `PUT /v1/admin/logs/detail` | Owner and administrators | Messages, newest first, by level and kind; follow live; start or end "Record more detail" |
| `GET /v1/admin/audit` | Owner | The activity record, kept a year |
| `GET /v1/admin/reports`, `GET /v1/admin/reports/{id}`, `PATCH /v1/admin/reports/{id}`, `DELETE /v1/admin/reports/{id}`, `GET /v1/admin/reports/{id}/log` | Owner | Problem and crash reports: read, set the status and reply (the reply arrives as a notice), delete, download the app's log |
| `GET /v1/admin/capabilities`, `POST /v1/admin/permissions/fix` | Owner | What this server can do and why not; fix the data folder's permissions |
| `POST /v1/admin/support-export` | Owner | Prepare and download a support export in one request; it is not kept |
| `POST /v1/admin/notifications` | Owner for `audience=everyone`; a key allowed Administer for `audience=admins` | Message everyone; or a tool's notice to the owner and administrators |
| `PUT /v1/admin/libraries/order` | Owner | The order of libraries everyone sees (counted with Libraries) |
| `GET /v1/admin/filesystem` | Owner | The folder explorer: folders under a path, 200 at a time, with `writable` on request (counted with Libraries) |

The owner's read of files Portico could not prepare is a library's skipped files (Libraries);
remembered playback problems are under Playback. There is no history of failed conversions
beyond the log and a failed task.

Not carried: `/v1/console/registry`, `/v1/admin/settings/configuration` and
`/v1/admin/console/settings` (the settings documents); `/v1/admin/console/schedules` and
`/v1/admin/maintenance/settings` (each timed thing is a setting where it belongs);
`/v1/admin/playback/history` and `history.csv` (one Play history); `/v1/admin/telemetry*` and
`/resource-usage` (`load` and `statistics`); `/v1/admin/attention` and `/console/alerts` with
acknowledge (alerts are on the Dashboard and go by themselves); `/v1/admin/console/panels/*`,
`/diagnostics/*`, `/exports*` (logs, the activity record, one support export);
`/v1/admin/storage-usage/{category}/cleanup` (one Empty cache); `/v1/admin/logs/settings` and
`/v1/admin/notifications/settings` (settings documents); pause and resume of a job.

### Keys and webhooks, route by route (10)

A person's own tokens and app passwords are `/v1/me/tokens` and `/v1/me/app-passwords`, while
the account's "Access tokens" switch is on; the owner never sees them.

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/admin/api-keys`, `POST /v1/admin/api-keys`, `DELETE /v1/admin/api-keys/{id}` | Owner, signed in (never a key) | Server API keys, at most 100: list, make (the secret is in this answer only), revoke |
| `GET /v1/admin/webhooks`, `POST /v1/admin/webhooks` | Owner | Webhooks, at most 20, each with whether it is in trouble and how many messages were dropped; add one (its secret is in this answer only) |
| `PATCH /v1/admin/webhooks/{id}`, `DELETE /v1/admin/webhooks/{id}` | Owner | Change the name, address or events, turn on or off (off drops what is waiting); delete with its log |
| `POST /v1/admin/webhooks/{id}/test`, `POST /v1/admin/webhooks/{id}/secret` | Owner | Send a test, also while it is off; replace the secret |
| `GET /v1/admin/webhooks/{id}/deliveries` | Owner | The last 200 deliveries: when, which event, the result, how many tries |

Not carried: showing a key or app password again later; the owner making app passwords for
other people and listing members' tokens (`/v1/admin/access-tokens*`); keys made from an app
password and tracked under it; the five webhook settings (fixed values).

## Not on the wire, on purpose

`viewerFence`, `expectedRevision`, `operationId`, a "preparing" answer on a read, query handles,
lower-bound totals (with one stated exception: the count of people in a search by a restricted profile says so with
`totalIsMinimum` when it stops at 2,000), write lookups after the fact, conflict choices for offline edits, a
revision on personal state, per-provider and per-screen route families, and step-by-step
wizard routes. Each existed for the old storage design. What a person would lose without
them is nothing: the last deliberate edit wins, a `2xx` means it happened, and reads answer.
