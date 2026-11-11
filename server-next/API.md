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
| `POST /v1/auth/portico/challenge`, `POST /v1/auth/portico/exchange` | Sign in with a Portico Account: the server's challenge out (the request is sent as `application/json` with `{}`; anything else is `415`, so a page on another site cannot ask for one unasked); then the challenge, the sign-in note Portico Services signed for it, and an invitation's code for a person joining, in, and tokens out. The note is checked against the root key the server is built with, for this server, this link and this challenge, once. A local server answers `409 local_sign_in` |
| `POST /v1/auth/codes`, `GET /v1/auth/codes/{id}`, `POST /v1/auth/codes/{id}/approve`, `POST /v1/auth/codes/{id}/redeem` | Sign in on a TV by approving a code on a phone |
| `GET /v1/auth/invitations/{id}`, `POST /v1/auth/invitations/{id}/accept` | Preview and accept an invitation |
| `POST /v1/auth/password-reset/confirm` | Choose a new password with a reset code. The owner or an admin makes the code in People; the server sends no email, so there is no route that asks for one |
| `POST /v1/auth/owner-reset`, `POST /v1/auth/owner-reset/confirm` | An owner who cannot sign in. The first makes the server write a one-time code to `reset-code.txt` in its data folder and answers only the file's name, the same every time and whatever username was sent. While a code is live no new one is written, and after one is used or cancelled a new one waits a minute. The second chooses the owner's new password with that code, and can turn the owner's two-step sign-in off. The code works once, for 15 minutes; five wrong tries cancel it. Nothing sent over the network alone resets the owner: it takes reading the file on the machine |
| `POST /v1/auth/password` | Change your password |
| `GET /v1/auth/remembered`, `DELETE /v1/auth/remembered/{id}` | Accounts remembered on this device's sign-in screen |

### Me, profiles and devices (34)

| Route | Purpose |
|---|---|
| `GET /v1/me`, `PATCH /v1/me` | Your account, current profile, role and features; edit account details |
| `GET /v1/profiles`, `POST /v1/profiles` | List and create profiles (at most eight per account). With `member=<id>` an admin manages another person's. |
| `GET /v1/profiles/{id}`, `PATCH /v1/profiles/{id}`, `DELETE /v1/profiles/{id}` | Read, edit, delete (`?preview=true` says what would be lost) |
| `POST /v1/profiles/{id}/select` | Choose a profile, with its PIN if it has one; answers tokens bound to it: the same sign-in with a new access token and no `refreshToken`, on purpose (a pick is not a renewal), so the app keeps the refresh token it already has. With `rememberPin` the answer carries a `pinToken` this device sends in place of the PIN for as long as the owner allows (`pinRememberDays` in the `sign-in` settings; 0 is always ask), until the PIN changes. A main profile entered with a `pinToken`, or more than an hour after its PIN was typed on a device that remembers it, opens every page; changing a sensitive setting (a profile's PIN and limits, people and invitations, a change of owner, the sign-in, network and remote access settings, keys, tokens and webhooks, restoring or deleting a backup, removing a library) answers 403 `profile_pin_required` until the profile is chosen again with the PIN. A person turns that check off for themselves with the setting `security.askPinForSensitiveSettings` |
| `PUT /v1/profiles/order` | Put the account's profiles in order: `ids` names every one once; the main profile stays first |
| `PUT /v1/profiles/{id}/avatar`, `GET /v1/profiles/{id}/avatar`, `DELETE /v1/profiles/{id}/avatar` | A profile's picture: the body of `PUT` is a JPEG, PNG, GIF or WebP of at most 4 MB; `GET` needs a sign-in to the same account (or `member=<id>` from the owner or an admin) and takes `size=small` or `large` |
| `PUT /v1/profiles/{id}/pin`, `DELETE /v1/profiles/{id}/pin` | Set, change or remove a PIN. With the account's password, `PUT` replaces a forgotten PIN from any profile. |
| `GET /v1/profiles/{id}/restrictions`, `PUT /v1/profiles/{id}/restrictions` | Rating limit, Allow unrated titles, blocked labels, library access; the read also says what the whole account is held to. A save changes only the fields it names, so two devices saving different things do not undo each other: `maxRatingAge: null` lifts the rating; a list, when sent, is the whole list, and `null` or `[]` empties it; a switch sent as `null` stays as it is. A save that changes nothing writes nothing and leaves the profile's access marker alone |
| `PATCH /v1/admin/members/{id}` with `limits` | The owner or an administrator holds a person's whole account to limits that person cannot lift: home network only, viewing hours, a rating limit with Allow unrated, blocked labels, a bitrate cap away from home, how many profiles (1, 2 or 4; the owner alone lowers it), and Watch Together and tokens (the account's Downloads switch is `allowDownloads` on the member itself). `limits` changes only the fields it names; `null` lifts a limit that can be lifted. Viewing hours always name a timezone. Away from home or outside the hours, signing in, renewing, choosing a profile, keys, and the addresses of downloads and books answer 403 `away_from_home` or `outside_viewing_hours`, and the device keeps its sign-in. A title playing when the hours end stops within about 17 minutes: the 15 a token lasts and the 2 a stream waits for its player. |
| `GET /v1/me/two-factor`, `POST /v1/me/two-factor`, `POST /v1/me/two-factor/confirm`, `DELETE /v1/me/two-factor`, `POST /v1/me/two-factor/recovery-codes` | Two-step sign-in |
| `GET /v1/devices`, `PATCH /v1/devices/{id}`, `DELETE /v1/devices/{id}` | Your devices: list, rename, forget (ends its sign-ins) |
| `GET /v1/devices/{id}/capabilities`, `PUT /v1/devices/{id}/capabilities` | What a device can play |
| `GET /v1/me/sessions`, `DELETE /v1/me/sessions/{id}`, `DELETE /v1/me/sessions` | Your sign-ins: list, end one, end all others |
| `GET /v1/admin/members/{id}/devices`, `DELETE /v1/admin/members/{id}/devices/{deviceId}` | The owner or an administrator: the devices of a person they manage, and sign one out |
| `GET /v1/admin/devices` | People's Devices group: every account's devices for the owner, only the members' for an administrator (never the owner's, another administrator's or its own), a device once per account signed in on it, with the account (`porticoAccount` for the Local / Portico label) and its live sign-ins, most recently seen first, by cursor. Signing one out is the route above, or `DELETE /v1/devices/{id}` for the caller's own |
| `GET /v1/preferences`, `PATCH /v1/preferences` | A profile's own settings: the list of settings with their defaults, what the profile saved for everywhere, for this kind of device and for this device, and the values in force. The last save wins. A list setting says the most items it holds (`maxItems`). The `pins` setting is the profile's pins in its order, at most 200, each `playlist:`, `collection:`, `view:` or `library:` and an id; an app names them from `GET /v1/lists` and `GET /v1/libraries` and leaves out a pin neither lists |
| `/v1/me/tokens`, `/v1/me/app-passwords` | Your personal tokens and app passwords: list, make, revoke. A key's secret is shown once and not kept, so a repeat of the same `Idempotency-Key` answers the key made the first time: with a new secret while that key has never been used (the unseen first one stops working), and with no secret and a `note` once it has been used; this holds for `/v1/admin/api-keys` too. |

### Browse, search and Home (21)

| Route | Purpose |
|---|---|
| `GET /v1/libraries`, `GET /v1/libraries/{id}` | Libraries you may see; one library with its views, sorts and filters |
| `GET /v1/libraries/{id}/landing` | The library's landing page (its Discover tab), composed by the server in Home's shape: Home's rows for this library alone (Continue, Recently Added); a row with nothing in it is left out. A row's See all is `GET /v1/items` (Recently Added: the library's titles by `sort=added`) |
| `GET /v1/items` | **The one list.** By `library`, `parent`, `person`, `tag`, `list`, `collection`, `series` (a series' volumes, in volume order unless sorted); `kind`; filters; `sort`; `cursor`, `limit`, `total`. `at=<position>` opens the list at any position in place of a cursor, for a scroll bar or a letter. `artist=` takes a person or an artist's own id (an artist's page: `artist={id}&kind=track` is every song crediting the artist). `sort=popular`, for one artist's or one album's songs only: most played on the server in the last seven days first, then by plays ever, counting only the plays of profiles that share their activity (`privacy.showActivityToMembers`, off by default) and not made with history paused, and only songs so played at least once, so an artist nobody has played has none (an artist's Popular row: `&sort=popular&limit=10`); the counts are never sent |
| `POST /v1/items/query` | The same list with a structured filter body, for filters too long for a query string |
| `GET /v1/items/{id}` | Detail, with your state |
| `GET /v1/items/{id}/credits`, `GET /v1/items/{id}/related`, `GET /v1/items/{id}/files` | Cast and crew; related titles; where each file is on the server, for the owner and admins only (everyone sees the format facts on the detail). For a film or show, `related` ends with the page's rows in a fixed order: `cast` (one group per lead actor, up to three, with `person`), `creator` (a film's first director, a show's first writer, with `person`; left out when that person already has a cast row), `genre` (its first genre, with `tag`); each up to 20 other titles the profile may see, never the title itself; an empty group is left out. See all is `GET /v1/items?person=` or `GET /v1/items?library=&kind=&tag=&sort=communityRating` |
| `GET /v1/facets` | Counted filter choices for a list, asked for when someone opens the filter and never with the page (see "Lists and their counts"). `facet=letter` is the letter rail: each first letter in title order with its count, so a letter's position is the sum of the counts before it |
| `GET /v1/search` | Search titles and people you may see, by `q`. Results come in groups by kind, each with its total. With `kind`, one group a page at a time (`cursor`, `limit`), best match first or by `sort` and `order`. `record=true` also keeps the search in your recent searches. `503 search_unavailable` (retry) while the index is being built. |
| `GET /v1/search/suggestions` | The few best titles and names for what has been typed so far |
| `GET /v1/search/history`, `DELETE /v1/search/history` | Your recent searches: read; clear, or remove one with `query` |
| `GET /v1/home`, `GET /v1/home/rows/{id}` | Home, composed by the server in the profile's order without the rows it hid; one row paged. Customize Home saves the settings `home.rowOrder` and `home.hiddenRowIds` with `PATCH /v1/preferences`; the Continue rows cannot be hidden. `hidden=include` (for the Customize Home editor) also sends each hidden row in its place, with `hidden: true`, its title and no items. Saved views put on Home with the setting `home.savedViews` (at most 20) are rows `view:{id}` named after the view, with its `library` and `query`: the row's titles, and more of them, are `GET /v1/items?library={library}&{query}`; a row Home read carries `nextCursor`, to load more from where it stopped in the same order (a random one included). Home reads the titles of the first 4 of them it shows, for at most 40 ms; the others come `deferred: true` with no items, for the app to read as they come near the screen. A deferred row may turn out empty, and is then not drawn |
| `GET /v1/people`, `GET /v1/people/{id}` | A library's people by one role, by name, a page at a time: `library` and `role` (`author` or `narrator`), `cursor`, `limit`, `total`; each with `titleCount`, the titles of that library the profile may see that credit them in that role (an audiobook or book library's Authors tab; an author's books are `GET /v1/items?library=&author={id}`). One person, with `roles`: each role they have on titles the profile may see and `titleCount` for it, most first (a person page's groups). Their titles are `GET /v1/items?person={id}`, one card per title, each with `credits`: every role they have on it, with the character, in billing order; `&role=actor` (any credit role) is one group's titles |
| `GET /v1/tags` | Tags by kind and then name, paged (`kind`, `cursor`, `limit`): every tag in use for the owner and admins, otherwise only Tags on titles the profile may see and never a Tag it is kept from |
| `GET /v1/genres` | The genres of the films and shows you may see, across every library you may see, by name, at most 200, each with one cover (a backdrop of one of its titles you may see, the same one each read); opening one is `GET /v1/items?tag={value}`. `counts=true` adds each one's count, which reads in proportion to the library: an app draws the genres from the plain read and fills the counts from a second read, like totals. With `library`, that one library's genres instead, whatever its kind (a music library's Genres tab): by name, each always with its count, the number of the library's albums (films, shows, books) it opens to with `GET /v1/items?library=&genre={value}`, and with the cover of one of them |
| `GET /v1/music/mixes` | Mixes built from your library |
| `GET /v1/images/{id}` | Artwork bytes: the stored small size when it covers `w` and `h`, else the large |

### Your state and lists (26)

| Route | Purpose |
|---|---|
| `PUT /v1/items/{id}/state` | **The one state route.** Watched, position, favourite, My List, rating, Not interested, and removing a title from Continue Watching until it is played again. On a show, season or album it sets every leaf and answers an undo token. |
| `POST /v1/items/state:batch` | The same for up to 500 items (what a device queued while out of reach, or several selected titles), answered title by title. Written in order, in writes of a few hundred titles each, counting the episodes a marked show writes; with `plays`, the plays a device made out of reach (a download played with no server), each counted once by its `playId` and answered on its own |
| `POST /v1/items/state:undo` | Undo a container change within about a minute |
| `GET /v1/history`, `DELETE /v1/history/{id}`, `DELETE /v1/history` | Your finished plays, newest first, by `period` (`24h`, `7d`, `30d`, `90d`, `all`): list, remove one, clear. The owner's play record keeps every play. Removing an entry or clearing sends a `history` event to the profile's other devices |
| `GET /v1/lists`, `POST /v1/lists` | Playlists, collections and saved views (`kind`) |
| `GET /v1/lists/{id}`, `PATCH /v1/lists/{id}`, `DELETE /v1/lists/{id}` | Read, rename, delete |
| `POST /v1/lists/{id}/entries`, `PATCH /v1/lists/{id}/entries/{entryId}`, `DELETE /v1/lists/{id}/entries/{entryId}` | Add, move, remove entries. Entries are read with `GET /v1/items?list=`. |
| `GET /v1/lists/{id}/shares`, `PUT /v1/lists/{id}/shares` | Who a list is shared with |
| `GET /v1/jobs/{id}`, `POST /v1/jobs`, `DELETE /v1/jobs/{id}` | Run a bulk action over a selection, watch it, cancel it |
| `POST /v1/jobs` kind `set_state` | Any profile: set watched, favourite, My List, Not interested or rating on every title in a view it sees. `target.filter` is the view's `GET /v1/items` query; `params` are the state fields |

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
| `GET /v1/playback/sessions` | What the account is playing on all its devices, and its stream limit. A title the asking profile may not see is unnamed. Each one playing from a queue says what plays next (`next`: `itemId`, `title`, only a title the asking profile may see), for Next up; Play on's device list carries the same in `playing` |
| `GET /v1/playback/sessions/{id}` | The device's own session. An ended one can be read for two minutes and says why it ended. |
| `PATCH /v1/playback/sessions/{id}` | Pause, play, seek, change version, audio, subtitles or quality, or report that the player could not play it |
| `PUT /v1/playback/sessions/{id}/progress` | Where the player is. Answers how long the session now lives and when to report next (10 seconds while playing). `410` when the session is over. |
| `DELETE /v1/playback/sessions/{id}` | Stop, with the final position. Safe to repeat; an unknown session stops fine. Also stops another device of the same account. |
| `POST /v1/queues`, `GET /v1/queues` | Build a queue from a show, season, album, artist, playlist, collection, library view, selection or radio; the queues this device has. A device keeps one music queue and one video queue. The app starts the current entry with `POST /v1/playback/sessions` and `queue`. |
| `GET /v1/queues/{id}`, `PATCH /v1/queues/{id}`, `DELETE /v1/queues/{id}` | The queue with its `ETag`; repeat and shuffle; clear |
| `GET /v1/queues/{id}/entries` | A run of entries: `from` and `limit`, or `around=current` with `before` and `after` |
| `POST /v1/queues/{id}/entries`, `DELETE /v1/queues/{id}/entries/{entryId}`, `POST /v1/queues/{id}/entries/{entryId}/move` | Play next, play later, remove, move. These need `If-Match`; a stale one is `412`. |
| `POST /v1/queues/{id}/advance` | The entry ended, Next, Previous, or jump to an entry. "Ended" names the playback `sessionId` that ended, so a resent one counts once towards "Still watching?", with repeat one too |
| `POST /v1/queues/{id}/prepare-next`, `POST /v1/queues/{id}/commit-next` | Get the next song ready before this one ends, then make it the session, for a join without a gap |
| `POST /v1/queues/{id}/playlist` | Save the queue as a playlist |
| `GET /v1/media/{grant}/{file}` | The bytes a session plays, at the address the session gave: the file itself by byte range (`original`), or a conversion's playlists and segments (`master.m3u8` and what it names). No sign-in header: the grant is the permission, and it dies when the session stops or changes stream. Anything else is `404`. |
| `GET /v1/media/subtitles/{grant}/{track}/{n}.vtt` | A text subtitle of the session's title as WebVTT, at the address the session gave in `presentation.subtitles`; `?segment=N` is one minute of it, which is what the stream's own playlist names. Same permission and same `404` as above. |
| `GET /v1/items/{id}/subtitles`, `PUT /v1/items/{id}/subtitles/offset` | A title's subtitles (in the file, or beside it) and the timing nudge this profile gave them, which is kept for that title |
| `GET /v1/items/{id}/subtitles/search?languages=`, `POST /v1/items/{id}/subtitles` | Search OpenSubtitles for a title the profile may play, in up to ten languages; the answer carries the credit "Subtitles provided by OpenSubtitles.com". Download one result for a version (`versionId`, else the first): it is then offered to everyone who may play that version. What is sent back is checked (a language code, the result's ref, a release name of at most 200 characters that can be shown). `409 subtitle_search_not_set_up` until the owner sets up OpenSubtitles; `429` for a used-up daily allowance or a pause |
| `DELETE /v1/admin/items/{id}/subtitles/{subtitleId}` | The owner and administrators remove a downloaded subtitle (`d…` ids in the title's list) |
| `GET /v1/items/{id}/lyrics` | A song's lyrics from files beside it, timed lines in order |
| `GET /v1/admin/playback/stream-limits`, `PUT /v1/admin/playback/stream-limits` | The owner's stream limits: in total, per person, and for one person |
| `GET /v1/admin/playback/failures`, `DELETE /v1/admin/playback/failures/{id}` | Remembered "this way of playing this file failed on this device", and forgetting one |

### Device capability document

A device says once what its players can play, and the server plans every start from that.
This is the whole of how a device is described: the server has no table per platform and
never looks at a device's name. Portico's own apps send exactly this document over exactly
these routes; a client written by anyone else is planned by the same rules.

`PUT /v1/devices/current/capabilities` with the document as the body (at most 64 KB) answers
`204` and replaces what was stored. Send it again when something changes: a window moved to
an HDR screen, a receiver plugged in, a setting changed. `GET` reads it back as it was sent.
`GET /v1/items/{id}/playback-options` then shows how each version of a title would reach
this device and why.

**Sending nothing is allowed.** A device with no stored document is planned from the
**baseline**: MP4, M4V or MOV with 8-bit H.264 up to 60 frames a second and stereo AAC or MP3
plays as it is; AAC and MP3 music files play as they are; anything else is repackaged or
converted into MPEG-TS HLS carrying H.264 with stereo AAC or MP3; text subtitles are sent
beside the stream for the app to draw; the screen is taken to be standard dynamic range. The baseline is the file
`internal/streaming/profiles/baseline.json`, and the documents Portico's apps send are
recorded beside it as examples, each with the routes expected for 25 kinds of file.

**Version.** `version` is `2`. Within a version fields are only added, and the server
ignores a field it does not know, so a newer app keeps working with an older server. A
document with any other version number is stored and the device is planned from the baseline
until the server knows that version: it plays, with more converted than it needs. A device
is never refused for its document's version.

**What is refused.** Only a body that is not a document: not a JSON object, no whole-number
`version` of 1 or more, larger than 64 KB, or (for version 2) a field of the wrong type or
outside its bounds. The answer is `400 invalid_request` and its message names the part at
fault. What was stored before stays, and the device still plays.

**Names.** Codecs, containers and the like are short lower-case names (letters, digits and
`_ . + -`, at most 32 characters). Codecs are named as ffprobe names them: `h264`, `hevc`,
`av1`, `vp9`, `mpeg2video`, `vc1`; `aac`, `mp3`, `ac3`, `eac3`, `dts`, `truehd`, `flac`, `alac`,
`opus`, `vorbis`, `pcm`. Common other spellings (`h265`, `avc`, `ec-3`, `dts-hd`) are read as
the same codec. A name the server has never met is kept and simply matches no file.

Top level. Only `version` and `client.family` are required.

| Field | Meaning | When absent |
|---|---|---|
| `version` | `2` | Refused (see above) |
| `client.family` | A short name for the kind of client, such as `web` or `roku`. Kept with each decision so a wrong one can be traced. No rule reads it. | Refused |
| `client.form`, `.platform`, `.platformVersion`, `.os`, `.osVersion`, `.model`, `.app`, `.appVersion` | More of the same, for tracing only. `form` is a short name (`tv`, `phone`, `tablet`, `desktop`, `browser`, `cast`, `speaker`); the rest are text up to 64 characters. | Nothing changes |
| `engines.native` | The platform's own player: see the next table | The device is planned from the baseline |
| `engines.mpv` | A second player the app carries, in the same shape. With `engineSetting` `automatic` the server uses the platform's player whenever it can play the file as it is or repackaged, and the second player only when that avoids converting the picture, converting sound to a lossy codec, or burning subtitles in. | The device has one player |
| `engineSetting` | The viewer's choice of player: `automatic`, `native` or `mpv` | `automatic` |
| `display.dynamicRanges` | What the screen shows now: any of `sdr`, `hdr10`, `hdr10plus`, `hlg`, `dolby_vision` | Not stated: the screen is taken to show whatever a player decodes |
| `audioOutput.route` | Where sound leaves the device now: `hdmi`, `arc`, `earc`, `optical`, `spdif`, `speaker`, `headphones`, `bluetooth`, `airplay` or another short name. Passthrough needs one of the first five. | No passthrough |
| `audioOutput.maxChannels` | Channels that output carries | Not limited by the output |
| `maxBitrateBps` | The device's own ceiling on a stream | None |
| `overrides.forceStereo`, `.disablePassthrough`, `.disableHdr` | The viewer's "something looks or sounds wrong" switches. Each narrows what the device is planned as. | Off |
| `audioDecode` | For music the app decodes itself: entries of `codec`, `containers`, and optionally `maxSampleRate`, `sampleRates`, `maxChannels`, `maxBitDepth`, `via` | Music is planned from the player's tables like any other file |

One player (`engines.native` or `engines.mpv`):

| Field | Meaning | When absent |
|---|---|---|
| `name`, `version` | The player's own name and version, for tracing | Nothing changes |
| `evidence` | How the device knows what follows: `declared` (the app's own table), `probed` (asked of the device while running) or `mixed`. Both are trusted the same; it is kept so a wrong decision can be traced to its source. Codec entries may carry their own. | `declared` |
| `video` | The pictures it decodes: see the next table | It decodes none: every picture is refused |
| `audio` | The sound it decodes itself: entries of `codec`, `maxChannels`, `maxSampleRate`, `sampleRates`, `maxBitDepth`, `variants`, `objectAudio` | It decodes none |
| `audioPassthrough` | Sound it hands undecoded to a receiver: entries of `codec`, `maxChannels`, `variants`, `objectAudio`. Used only while `audioOutput.route` carries it and the viewer has not turned passthrough off. | None |
| `transports` | How streams reach it. Each entry has `transport` (`direct`: the file itself by byte range; `hls_ts`: HLS in MPEG-TS; `hls_fmp4`: HLS in fragmented MP4), the `containers` it takes that way, and the `video` and `audio` codecs it takes in them. A file plays as it is only if a `direct` entry lists its container and both codecs. An entry with an empty `video` list is for music files. | Nothing can be sent: every title is refused |
| `subtitleRendering` | Subtitle kinds the player draws itself: `text`, `ass`, `pgs`, `vobsub`, `dvb` | It draws none |
| `toneMapsHdr` | The player shows HDR it decodes acceptably on a screen that is not HDR | `false`: the server converts HDR for a screen that cannot show it |
| `embeddedAudioSwitching` | The player can choose between the audio tracks inside a file | `false`: a file whose wanted track is not its default is not played as it is |
| `liveChannels` | For `engines.mpv` only: it plays live channels | `false`: channels go to the platform's player |

One entry of `video`. A limit that is absent or `0` means "not limited", and an absent list
means "not stated", which never rejects a file, except where the table says otherwise.

| Field | Meaning |
|---|---|
| `codec` | Required |
| `profiles` | Codec profiles decoded, lower-case with `_` for spaces (`high`, `main_10`) |
| `maxLevel` | The highest level, as ffprobe numbers it (H.264 4.1 is `41`, HEVC 5.1 is `153`) |
| `bitDepths` | Bit depths decoded, such as `[8, 10]` |
| `maxWidth`, `maxHeight`, `maxFrameRate`, `maxBitrateBps` | Ceilings |
| `limits` | Operating points where one ceiling cannot say it ("4K at 30 but 1080p at 60"): entries of `maxWidth`, `maxHeight`, `maxFrameRate`, and optionally `bitDepths` and `maxLevel`. A picture must also fit one that applies to its bit depth. |
| `dynamicRanges` | Ranges it decodes. Absent means standard dynamic range only. Whether the result can be shown is `display.dynamicRanges` or `toneMapsHdr`. |
| `dolbyVisionProfiles` | Dolby Vision profile numbers it plays as Dolby Vision. A profile not listed plays from its base layer (HDR10, HLG or SDR) where it has one. |
| `dolbyVisionProfile8Compatibility` | Narrows profile 8 to exact base layers: `1` HDR10, `2` SDR, `4` HLG |
| `interlaced` | It plays interlaced pictures. Absent means it does not. |
| `chromaSubsampling`, `frameRateModes` | `4:2:0`, `4:2:2`, `4:4:4`, `4:0:0`; `constant`, `variable` |
| `codecTags` | The only sample entries it plays from a file as it is (`hvc1` but not `hev1`). Streams the server repackages are tagged by the server. |

For sound, `variants` narrows a codec to exact kinds (`dts_core`, `dts_hd_hra`, `dts_hd_ma`,
`dtsx`, `dts_express`, `truehd`, `truehd_atmos`, `ac3`, `eac3`, `eac3_joc`, `ac4`, `lpcm`);
without it the DTS-HD kinds, DTS Express and DTS:X are not taken. `objectAudio` is `atmos` or `dtsx`.

The smallest useful document, for a player that takes MP4 and HLS with H.264 and AAC:

```json
{"version": 2, "client": {"family": "my-client"},
 "engines": {"native": {
  "video": [{"codec": "h264", "bitDepths": [8], "maxWidth": 1920, "maxHeight": 1080}],
  "audio": [{"codec": "aac", "maxChannels": 2}],
  "transports": [
   {"transport": "direct", "containers": ["mp4"], "video": ["h264"], "audio": ["aac"]},
   {"transport": "hls_ts", "containers": ["mpegts"], "video": ["h264"], "audio": ["aac"]}],
  "subtitleRendering": ["text"]}}}
```

**A document that says something untrue.** The server takes the document at its word; it
cannot check it. A client that claims less than it can do gets more converted than it needs.
A client that claims more is sent streams it cannot play: when its player fails it reports
that with `PATCH /v1/playback/sessions/{id}` and `failure`, and the server moves that file on
that device to the next way of playing it and remembers. What a document can never do is
widen a limit: the owner's settings (conversion on or off, stream limits) and a member's
limits (quality away from home, what the member may see) are applied after the document, so
no claim gets a stream those limits forbid.

### Setup and the kind of sign-in (5 new)

The Console half is under "Setup, remote access and sign-in kind" below. Setup holds nothing
until it finishes: the first to finish with the setup code is the owner.

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/system` | Anyone | Listed above. Adds `signIn` (`portico` or `local`, from the `sign_in_kind` setting), `move` (`to_portico`, `to_local` or absent: a switch has people waiting) and `atHome` |
| `POST /v1/setup/owner` | Anyone with the setup code, while there is no owner | Exists. Local sign-in: server name, username, password, and the remote access step (`remoteAccess`, `on` by default); answers tokens. Remote access Off chosen from outside the home networks needs `confirmRefusal` |
| `POST /v1/setup/portico`, `POST /v1/setup/portico/finish` | Anyone with the setup code, while there is no owner | Portico Accounts: name and code in, the approval out (its address, id, the code to match when approving on another device, and the poll token for `POST /v1/hosted/approvals/{id}/poll`); then the approval that came back in, with the remote access step, tokens out. Nothing is saved between the two; the finish makes the owner and the link to that Portico Account in one commit. Without Portico Services: `503 services_unreachable`, "Portico Services can't be reached, so this server can't be set up with Portico Accounts right now. Nothing has been saved. Try again in a moment, or set it up with local sign-in instead." |
| `POST /v1/setup/finish` | Owner | The last step: Remote security (`enhanced` by default, or `standard`). Enhanced is checked against the connection in use: `409 would_refuse_this_route` unless `allowThisAddress` (approve this host and port) or `confirmRefusal`. Answers what is in force |

Setup is four steps, in the old onboarding flow's order: (1) the server's name and the kind of sign-in (Portico Accounts preselected, local sign-in beside it); (2) remote access (On preselected) and Remote security (Enhanced preselected, with the old flow's explanation); (3) the owner (local: username and password; Portico: the approval); (4) the first library, which can be skipped. Remote access is saved with the owner; Remote security is sent with `POST /v1/setup/finish`, which closes the flow and runs the check of the connection in use (the old flow's "This address won't work after setup." panel); it takes effect only then, and then only once there is an address it can allow (and once its certificate is usable: the Portico certificate has been issued, or the owner's own certificate files are loaded and valid for the address; a reverse proxy at the own address needs none).
| `POST /v1/auth/move/check`, `POST /v1/auth/move/complete` | A person whose account is waiting to move | An old password or a move code in, the username and the next step out; then, to move to a Portico Account, `challenge` (from `POST /v1/auth/portico/challenge`) and `identity` (the sign-in note Portico Services answered it with), or a new username and password, in and tokens out. A move is a sign-in, never an approval. The old password is checked before the note, so a mistyped password does not use the note up. A wrong, used or expired code has one answer |
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
| `PUT /v1/groups/{id}/me`, `DELETE /v1/groups/{id}/me` | A member | Where this member is and whether it is ready, every 5 seconds, which also keeps it present (`here` moves the member to this device); leave |
| `GET /v1/receivers` | Any signed-in profile | The account's open devices that accept playback, at home or away, and what each is playing. Cast and AirPlay targets are found by the app, not listed here |
| `PUT /v1/receivers/current`, `DELETE /v1/receivers/current` | A signed-in device | This device accepts playback from the account's other devices, or stops |
| `POST /v1/receivers/{id}/requests`, `GET /v1/receivers/{id}/requests/{requestId}`, `DELETE /v1/receivers/{id}/requests/{requestId}` | A device of the same account | Ask a device to play (title, place, audio and subtitle picks, queue) or to be controlled; see allowed, declined, playing or failed; cancel. The first device keeps playing until the second is, and a move not playing in two minutes has failed |
| `GET /v1/receivers/current/requests`, `PATCH /v1/receivers/current/requests/{requestId}` | The receiving device | What is being asked; allow, decline, report playing or failed. The device itself remembers which devices it allowed |
| `GET /v1/cast/config` | Any signed-in profile, on a Portico Accounts server | The Cast application id (the `cast.application_id` settings row). A local server, or one without the id, answers `404` and has no `cast` capability |
| `POST /v1/cast/codes` | Any signed-in profile, on a Portico Accounts server | A pairing code for the TV the sender is casting to |
| `POST /v1/cast/redeem` | The Cast receiver, with the code | A sign-in that can play what the casting profile may see and nothing else (session scope `cast`, role `cast` in the token: playback, queues, the title and the feed; anything else is `403 playback_only`), renewed by the ordinary refresh; it ends when the account signs it out or after a day unused. The answer is the session only, nothing of the account or its profiles |

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

### Downloads (11)

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/items/{id}/download-options` | A profile allowed to download, for a title it may play | The real choices with the size of each ("about" when estimated), which need the server to prepare a copy first, and why a choice is not offered; with `selection` and `keepNext`, for a show, season, album, artist or playlist as a whole; the room left in the owner's download space |
| `GET /v1/downloads`, `POST /v1/downloads` | A profile allowed to download | This device's requests, or with `scope=profile` all of the profile's (the browser page), each with its titles as `GET /v1/downloads/{id}` gives them, in a fixed few reads; ask for one. `201`, or `200` for the same thing asked twice. A person limited to home can ask only at home |
| `GET /v1/downloads/{id}` | The profile that asked | One request with its titles and the state of each: waiting, preparing with progress, ready, paused, expired, could not be prepared or included, with the reason. Each title the profile may see also says where it sits: an episode's show (`parentTitle`), `seasonNumber` and `episodeNumber`; a track's album (`parentTitle`) and `artist` |
| `PATCH /v1/downloads/{id}` | The profile that asked | `paused`, `retry`, `keepNext` (0 removes the rule and keeps what is there). A copy nobody waits for any more stops being made |
| `DELETE /v1/downloads/{id}` | The profile that asked | Remove the request. The server's prepared copy stays for anyone else who asked for it |
| `DELETE /v1/downloads/{id}/items/{itemId}` | The profile that asked | Take one title out of a show, season, album, artist or playlist download: it leaves the list, the device is told to remove it (`removed`) at its next contact, and it does not come back, under "keep the next" either. Safe to repeat. Its copy stays for anyone else who asked for it |
| `GET /v1/downloads/{id}/items/{itemId}` | The profile that asked | What to fetch for one title: its details, and the addresses, sizes and hashes of its file or files, artwork, subtitles, chapters and markers, and lyrics |
| `POST /v1/downloads/sync` | A signed-in device | The device says which titles it holds. The answer says which to remove (the profile may no longer see them, or may no longer download, or the person took them out), which to add or drop under "keep the next", and until when everything plays offline (30 days from now) |
| `GET /v1/downloads/usage` | A profile allowed to download | Space the profile's prepared copies take |
| `GET /v1/media/{grant}/file/{n}`, `…/subtitles/{trackId}` | Whoever holds the address | The bytes of the title's nth file, or a subtitle file beside it, by range with `If-Range`, so a transfer carries on where it stopped; `?save=1` asks a browser to save it under the title's name. The grant is in the answer of the items route, works for a day after its last use, and is checked against the profile's access on every request |

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

A place is the state's `position`: a JSON object of at most 4 KB that the reader writes, with
`progression` (0 to 1) and `fingerprint` (the file it was made in) read by the server and the
exact spot in the reader's own notation. The later place wins. `"atEnd": true` (the end of the
main text, or the last page) also marks the book read. With Pause history on neither is kept;
a mark by hand still is, and clears the place. The book route answers the place with
`placeExact`, false when it was made in another file: the reader opens at its progression.
Home has a "Continue your books" row (`continue_reading`); books are not in Continue Watching.

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/items/{id}/book` | A profile that may see the book | How to read it: the version chosen (or `version=`), its kind, contents and page list, the addresses of its parts, the fingerprint of the file, and whether the caller may save the file. A book the server cannot open says why |
| `GET /v1/media/{grant}/pages/{n}` | Whoever holds the address | One comic or image page, no wider than `w`. A page too large to prepare safely is `422` and the rest of the book reads |
| `GET /v1/media/{grant}/parts/{path}` | Whoever holds the address | One part of an EPUB, named in the book's own list of parts and nothing else |
| `GET /v1/media/{grant}/source` | Whoever holds the address; saving needs a profile allowed to download | The file's own bytes by range: a PDF read in the app, and Save the file at any size |
| `GET /v1/items/{id}/annotations` | The profile, for a book it may see | Its bookmarks, highlights and notes in the book, in book order, a page at a time. Deleted ones are not listed: an app removes those it sent before and no longer finds (a `since=` would miss edits made out of reach, which carry the time they were made) |
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
| `GET /v1/live/programmes/{id}` | A profile allowed Live TV | One programme, its other airings (each with its episode where the guide says, and its `channelId` beside the channel's name), and whether it is booked or has a reminder. One the profile's rating limit hides says so and cannot be watched or recorded |
| `PUT /v1/live/channels/{id}/state` | A profile allowed Live TV | Favourite and hide, for this profile |
| `GET /v1/live/reminders`, `PUT /v1/live/reminders/{id}`, `DELETE /v1/live/reminders/{id}` | A profile allowed Live TV | Reminders; `{id}` is the programme. The app shows them |
| `GET /v1/dvr` | A profile allowed Live TV | Counts (upcoming, recorded, unwatched, conflicts), space used and free, about how many hours are left, the keep rule in force in words and as `keepDays` / `keepNewest`, the owner's `floorBytes` and `capBytes`, `everyonesRecordings` (the profile may see the Recorded TV library, so Everyone's shows more than its own), and the forecast of every booking of the next 14 days (`upcomingBytes`, `upcomingHours`; space, not titles). There is no separate "reserved": `floorBytes` is the space recording always leaves free |
| `GET /v1/dvr/recordings`, `POST /v1/dvr/recordings` | Read: a profile allowed Live TV. Book: a profile allowed to record | Upcoming (conflicts first), History, and with `state=recorded` the recordings that were made (by title, then newest first, for the Recorded tab) by `state`, the profile's own or with `scope=everyone` all of them, each row saying who booked it, naming its channel (`channel`, the name, and `channelId`), and saying what this profile may do with it by the same rules as the routes (`mayChange`: change, stop, delete what was recorded; `mayCancel`: cancel a booking not yet started); a conflict carries `tuners` (how many the source has) and `tunersUsedBy` (the account's own recordings by title, anyone else's as "A recording"); book an airing. An airing someone else already booked answers that booking and adds this profile to it, so it is still recorded if the first person cancels |
| `GET /v1/dvr/recordings/{id}`, `PATCH /v1/dvr/recordings/{id}`, `DELETE /v1/dvr/recordings/{id}` | Read: as above. Change and delete: the profile that booked it, the owner and administrators | One booking with what it clashes with, its size on disk (`bytes`: the file and the guide's details beside it, what Delete removes), and `playingNow`, how many Delete would stop; lists carry `bytes` too, and with the title (`itemId`, `libraryId`) this profile's `positionMs`, `durationMs` and `watched`; change padding or priority, Keep, "Record this instead" or "Skip this one"; cancel (the booking passes to the next profile that asked for it, if any), stop (what was captured is kept), or delete the recording and its file |
| `GET /v1/dvr/rules`, `POST /v1/dvr/rules` | A profile allowed to record; the owner for any profile with `profile=` | Series rules with their `source`, `channel` and `channelName`, the profile's own or with `scope=everyone` all of them for the owner; make one (`?preview=true` answers the rule with `preview`: how many airings it would record over the next 14 days and the first 10, each saying whether it is booked already, and makes nothing) |
| `PATCH /v1/dvr/rules/{id}`, `DELETE /v1/dvr/rules/{id}` | The profile that made it; the owner | Edit (options, a keep choice cleared with `0`, and the channel: `channel` for one channel of the rule's source, which lets go of its bookings not yet started on other channels as a cancel does; `anyChannel` for all of them where the guide has series ids), pause, resume; `?preview=true` answers the edited rule's `preview` and saves nothing; delete, with `bookings=keep` or `cancel` |

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
| Setup and remote access | 14 | Listed below (10); `GET`/`PUT /v1/admin/settings/network` (exists now: the owner's home networks, which alone decide local and remote, the reverse proxies whose `X-Forwarded-For` is believed, and the web origins besides the server's own whose pages may use it from a browser); `GET /v1/admin/blocked-networks` and `DELETE /v1/admin/blocked-networks/{id}` (exist now: the networks blocked for wrong guesses, and unblocking one). First-run setup is in the Client API (`POST /v1/setup/owner` exists now, needs the setup code the server logs and writes to `setup-code.txt` in its data folder, and works from any network) |
| Libraries, storage, files | 33 | Libraries, folders, scans and their issues; storage sources; folder browser; missing titles; delete; merge and split |
| Metadata and matching | 18 | `GET`/`PATCH /v1/admin/items/{id}/metadata`; match; refresh; artwork; needs-attention; provider settings |
| Playback and analysis | 10 | Stream limits; remembered playback failures; intro and credits review; analysis jobs. Active sessions, Play history and hardware are listed with Server operations |
| Live TV and DVR setup | 27 | Listed below: sources, channels, tuners, Library Channels |
| Server operations | 42 | Listed below: the settings documents (3), the Dashboard and operations (37), downloads by person (2) |
| Keys, tools, webhooks | 10 | Listed below: server API keys and webhooks. The assistant address and "files changed" are in the Client API. A person's own tokens and app passwords are under `/v1/me` |

### Metadata and matching, route by route (16 of 18)

For the owner and administrators. Provider keys and a library's source are the owner's alone.
A library's provider, language and region are options of the library, set on its own routes.
Refreshing or editing a selection of titles is a bulk action, `POST /v1/jobs`, with these kinds
(owner and administrators; a title or library hidden from the caller's main profile is `404`):

| Kind | Target | What it does |
|---|---|---|
| `match_library` | library | Look up every title no provider has been asked about |
| `refresh_library` | library | Ask the provider again about every matched title |
| `refresh_items` | items | Refresh metadata on a selection; a title with no match is looked up again |
| `edit_items` | items of one kind | Bulk edit. `params`: `year`, `edition`, `contentRating`, and `add` / `remove` of `genres`, `studios`, `networks`, `countries`, `labels` (a list is never replaced); `lock` (default true) locks what is edited. Ends `failed` naming each title not saved and why; the same request again saves them |

After every scan the server starts `metadata_after_scan` for the library itself: new titles are
looked up, then what is due is refreshed.

On a server with Portico's sound model installed, the server starts `music_analysis` for a music
library itself, hourly, when it has songs not yet read: each song is read once by the model, in the
`heavyWork` hours, for Mixes and radio by sound. Without the model nothing is started.

| Route | Purpose |
|---|---|
| `GET /v1/admin/items/{id}/metadata`, `PATCH /v1/admin/items/{id}/metadata` | The editor's document with its `ETag`: each field and list with its value, where it came from (provider, file, edit) and whether it is locked. Edit fields and lists, which locks them; lock or unlock a field, a list, the match or everything. Needs `If-Match`; a stale one is `412`. A value out of range is `422` naming the field and its limit |
| `GET /v1/admin/items/{id}/metadata/history`, `POST /v1/admin/items/{id}/metadata/history/{entryId}/undo` | The last 20 changes people made, newest first; undo one: what that change made is put back, where nobody has changed it since (newer changes stay) |
| `POST /v1/admin/items/{id}/metadata/refresh` | Ask the provider again now. Replaces only what is not locked and did not come from the files. A title with no match is looked up again from its name and year ("Look it up again") |
| `GET /v1/admin/items/{id}/matches` | Possible matches: the closest by themselves, a search by `q` and `year`, or the one a provider id or address in `ref` names (TMDB, TheTVDB, AniList or IMDb). Asks the provider; `502 provider_unavailable` when it does not answer, `422 unknown_ref` for an id it does not have. Posters come with the picture store |
| `PUT /v1/admin/items/{id}/match` | Use a match, with a show's episode order and number offset; or Keep as is. A show whose provider has no episode order of the chosen kind gets aired order, kept as its choice, and the answer's `notice` says so in a sentence. The title keeps its id, everyone's state, locked fields and chosen pictures. Answers when the title's own details are in; a show's episodes follow in the background. A match the library already has merges the two |
| `POST /v1/admin/items/{id}/merge`, `POST /v1/admin/versions/{id}/split` | Merge the title named in the body into this one (same kind, same library); split a version off into a title of its own, named from its file. Both hold through every scan (counted with Libraries, storage, files) |
| `GET /v1/admin/libraries/{id}/unmatched` | Unmatched items: the library's titles looked up without a confident answer, by title, each with a plain reason and up to three closest matches. No count |
| `GET /v1/admin/items/{id}/artwork` | The pictures a title could use for a `role`: beside the media, inside the file, the provider's, uploads, and a frame from the video |
| `POST /v1/admin/items/{id}/artwork` | Upload a JPEG, PNG, GIF (its first frame) or WebP of at most 10 MB for a `role`, sent as the body itself and judged by its contents (`415` otherwise, HEIC included; `413` over 10 MB), stored in the two sizes every picture has; it is used and locked. A matched title's best provider picture of each role is fetched by the server when it is matched or refreshed |
| `PUT /v1/admin/items/{id}/artwork/{role}` | Use this picture for the role, which locks it; or unlock the role |
| `DELETE /v1/admin/items/{id}/artwork/{imageId}` | Remove an upload; the next best picture is used |
| `POST /v1/admin/items/{id}/artwork/find` | Find more: every picture the provider has for a `role`, fetched by the server and kept to choose from. Making an episode's picture from the video comes with the preview work |
| `GET /v1/admin/metadata/settings`, `PUT /v1/admin/metadata/settings` | The owner's alone. `revision` moves on with every save; a save sends the one it read as `expectedRevision`, and one saved since answers `412 changed_elsewhere`. `includeAdult` (off by default). The owner's own TMDB read access token, `tmdbReadToken`: write-only (the answer says only `tmdbCredentialConfigured`), empty removes it, and it takes precedence over Portico's key built into the server, which is used without one. `tmdbCredentialUnreadable`: a key the owner saved that this server can no longer open (its key file was replaced); Portico's key is used until it is entered again. The OpenSubtitles key and account subtitle search needs, `openSubtitlesApiKey`, `openSubtitlesUsername`, `openSubtitlesPassword`: the key and password are write-only (`openSubtitlesKeyConfigured`, `openSubtitlesPasswordConfigured`), a new username drops the saved password, and `openSubtitles` says what the last Test connection found (`account`, `problem`, the downloads left) |
| `POST /v1/admin/metadata/settings/opensubtitles/check` | Test connection: OpenSubtitles is asked now, with the saved key and account, whether they work and how many downloads are left today; answers the `openSubtitles` status. With no complete key and account nothing is asked |

### Remote sources, route by route (8 of the 33 for libraries, storage and files)

The owner's alone. Adding, changing and disconnecting a source, and starting a Drive sign-in, take the owner signed in: a key is refused with `403 session_required`, while an admin-scope key still reads, browses and tests. A source's password or key is never sent back; it can only be replaced.

| Route | Purpose |
|---|---|
| `GET /v1/admin/remote-sources`, `GET /v1/admin/remote-sources/{id}` | The sources with their kind, settings, preset, limits (connections, requests a second, and `downloadBytesPerSecond`, 0 for none) and state (connected, unreachable, sign_in, waiting, setup) |
| `POST /v1/admin/remote-sources` | Add one: the connection is tested first and nothing is saved unless it works. Takes an `Idempotency-Key` |
| `PATCH /v1/admin/remote-sources/{id}` | Rename, change the preset or limits, change the address or replace the password (tested first; a new host needs the password again) |
| `DELETE /v1/admin/remote-sources/{id}` | Disconnect, once no library folder is on it (`409 source_in_use` otherwise) |
| `POST /v1/admin/remote-sources/{id}/test` | Test the saved source now, asking the source itself (for rclone and Drive, rclone lists it): a refused sign-in answers `422 source_sign_in` and the source needs its sign-in again; a source that asked to slow down answers `422 source_waiting` |
| `GET /v1/admin/remote-sources/{id}/folders?path=` | The folders in one folder of the source, for choosing a library's folders |
| `POST /v1/admin/remote-sources/drive-sign-in` | Start a Google sign-in with the owner's own OAuth client; the address the browser is sent to is pasted into the Drive source's settings |

A source is `webdav` (an address, user and password), `rclone` (one remote pasted from rclone's configuration, or a new one given as `type` and `options` as rclone names them, and a folder in it; OneDrive and Dropbox take `{"token": …}` as `rclone authorize` printed it, and a OneDrive drive is looked up with Microsoft when no `drive_id` is given) or `drive` (Google Drive with a service account key or the sign-in above, and a folder in it). rclone and Drive are served by the rclone that comes with Portico; when the server cannot start it, a test answers `422 rclone_unavailable`. An rclone or Drive folder that is not on the source is refused on its field (`rclone.path`, `drive.path`) when the source is added or changed, and a saved source whose folder has gone answers `422 source_folder_missing`. A link-local address (169.254.0.0/16, fe80::/10, cloud metadata addresses and names) is refused as a source's address and anywhere in an rclone option's value (WebDAV checks the address it dials; before rclone starts, a name it will use is refused when any answer is a metadata service's address or every answer is link-local), and Portico's WebDAV reads answered from a public address do not follow a redirect to a home or link-local one: `422 source_address_refused`. A home address the owner typed is an ordinary source. An sftp remote with no `host_keys` is accepted, and the source carries a `warning` that the server's identity is not checked.

### Libraries, scans and files (15 so far)

Only the owner. A key made for a tool needs the `library` scope to scan and delete.

| Route | Purpose |
|---|---|
| `GET /v1/admin/libraries`, `POST /v1/admin/libraries` | The libraries with their folders and options; make one (kind, name, folders, language, options), which starts its first scan. `remoteFolders` (`source`, `path`) are folders on remote sources, listed through the source before they are kept; a folder on one says its `source` |
| `GET /v1/admin/libraries/{id}`, `PATCH /v1/admin/libraries/{id}` | One library with its `ETag`; rename it, change its options, give the whole list of its folders (an added folder is scanned, a removed folder's titles leave). An edit may send `If-Match`; a stale one is `412` |
| `DELETE /v1/admin/libraries/{id}?confirm=<name>` | Remove the library; answers `202` with a job. Files on disk are not touched |
| `POST /v1/admin/libraries/{id}/scan`, `DELETE …/scan`, `GET …/scan` | Scan now, or Scan all files with `allFiles` (`202` with a job; joins a scan already asked for), cancel, and the scan's state, counts and folders |
| `GET /v1/admin/libraries/{id}/skipped` | Files the last scan could not use, 40 at a time, each with a reason |
| `GET /v1/admin/libraries/{id}/missing`, `DELETE …/missing?confirm=<count>` | The library's trash, with when each title will be removed; Empty trash |
| `GET /v1/admin/items/{id}/deletion`, `DELETE /v1/admin/items/{id}?confirm=<title>` | What deleting a title would take; delete its files from disk and remove it |
| `DELETE /v1/admin/versions/{id}` | Delete one version's files from disk |
| `POST /v1/admin/items/deletion`, `POST /v1/admin/items/delete?confirm=<count>` | What deleting a selection of titles in one library would take; delete their files from disk and remove them. At most 200 titles at once, never split |
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
| `sign-in` document | Owner | `staySignedInDays` (7, 30, 90, 365), `requireTwoStep` (`nobody`, `admins`, `everyone`) and `pinRememberDays` (0, 7, 30, 90). A person two-step is required of, who has not set it up, signs in to a session with `twoStepSetupRequired`: it can use only `GET /v1/me` and the two-step setup routes (anything else is 403 `two_step_setup_required`) until it is set up, then renews and carries on; they cannot turn it off (403 `two_step_required`) On a server that uses Portico Accounts `requireTwoStep` does not apply: the document says so in `notApplicable` with the line to show ("Two-step verification is set on each person's Portico Account."), a save that turns it on is refused with that line, and switching to Portico Accounts turns it off |

Documents: `general` (server name, when heavy work runs (`heavyWork`: `hours` `any`, `overnight` for 1 to 6 am, or `chosen` with `days`, `from` and `to` on the server's clock; sent whole), background speed, update check),
`streaming` (Convert when needed, quality and music away from home, upload speed, which
playback reads when a play starts away from home and may refuse it for, the
conversion folder, the graphics device, hardware decoding, conversion limits, End paused video
after), `storage` (Keep generated files, the free-space warnings), `backups`, `logs`,
`notices` (who gets server notices, keeping times for notices and Play history), `downloads`
(on or off, highest quality, space, how long prepared copies are kept), `title_data`
(Enhanced Recommendations on or off, and a copy's address), `live` and `dvr`,
`remote-access` (remote access on or off, Remote security, Secure connections, port mapping,
the public port, IPv6 (not built yet: `ipv6` reads as off, `ipv6Note` carries "IPv6 is not available yet." for the screen, and a save that turns it on is refused `400` with that sentence), your own address and certificate files, who may sign in from outside,
Find this server at home, and the DLNA switch), `integrations` (Assistants, the compatible
routes, the cap on members' tokens). Already built under their own addresses and kept there: `network`
(`/v1/admin/settings/network`), provider keys (`/v1/admin/metadata/settings`), stream limits
(`/v1/admin/playback/stream-limits`).

### Setup, remote access and sign-in kind, route by route (10)

| Route | Who | Purpose |
|---|---|---|
| `GET /v1/admin/sign-in` | Owner | The kind of sign-in, the owner, a switch in progress and who is waiting |
| `POST /v1/admin/sign-in/preview` | Owner, from the main profile, on the web | What a switch or Start over would do, with this server's real numbers. Changes nothing. For a switch to Portico Accounts with a `returnUrl`, `approval` carries the approval in full, as setup's does |
| `POST /v1/admin/sign-in/switch` | Owner, from the main profile, on the web | Confirm: a username and password for local sign-in, or the approval that came back for Portico Accounts; the server's name typed for Start over. Everyone is signed out |
| `POST /v1/admin/sign-in/finish` | Owner | Finish now: accounts still waiting are turned off as "Didn't move" |
| `POST /v1/admin/members/{id}/move-code`, `POST /v1/admin/sign-in/move-codes` | Owner and administrators | A single-use code or link for one waiting person, valid 7 days and shown once; for everyone waiting |
| `POST /v1/admin/owner` | Owner, with the password or a new Portico approval | Change owner; the previous owner becomes an administrator, a member, or is removed |
| `GET /v1/admin/remote-access` | Owner | What is true now: the Portico address, the certificate and its expiry, the last error, reachable from outside or not as just checked, outside addresses refused under Enhanced, this device at home or away, the identity code, and the addresses the server publishes for its apps (home first, each with its port and, with a Portico Account, its Portico name `<IP with dashes>.<name space>.portico.direct`). The switches are the `remote-access` settings document |
| `POST /v1/admin/remote-access/check` | Owner | Check now; Try again |
| `POST /v1/admin/remote-access/route-check` | Owner | The dry run: a proposed `remote-access` document in; whether the connection making this request would be refused under it (`reason`: `remote_access_off`, `http` or `address`), its host and port as the server sees them, the fixes (`allow`, `standard`, `preferred`) and the Remote security it would put in force. Changes nothing |
| `POST /v1/hosted/notices` | Portico Services through the server's open route, or any app carrying the note | `{note}` in, a signed `portico.server-note`; `{ack}` out, the server's signed word on what it has now applied. "These accounts left" and "this server was removed from its owner's account" count only when the note's revision is higher than the last applied, so a repeat, a late note and a note of an earlier link change nothing. No sign-in: the note proves itself. A local server answers `404` |
| `POST /v1/hosted/approvals/{id}/poll` | The page that started an approval, with the `pollToken` it was given | Whether the approval has been given, for a person approving on another device: `waiting`, or `approved` with the approval to hand to the step being finished. The server asks Portico Services no faster than it was told to, and only about approvals it started |
| `POST /v1/setup/local-sign-in` | Anyone with the setup code, after the owner created `reset-sign-in` in the data folder and restarted | The way back for an owner who cannot sign in to a Portico Accounts server: the code, a username and a password in, tokens out. The server switches to local sign-in: everyone is signed out, other people wait to move as after any switch, nothing is deleted, and Portico Services is told once that the server left. Needs nobody but the server |
| `GET /v1/admin/portico` | Owner | The link to Portico Services: `local`, `linked` or `unlinked`, the owning Portico Account, the last contact, whether Portico Services has the current name and members and what is wrong in plain words |
| `POST /v1/admin/portico/left/{id}/let-back-in` | Owner | Turn a person who left back on (a member whose `state` is `left` in People), so they can sign in with their Portico Account again; nothing they had is changed. It undoes a departure Portico Services reported, rightly or wrongly. `204`; a repeat changes nothing |
| `POST /v1/admin/portico/link`, `POST /v1/admin/portico/link/finish` | Owner, on a Portico Accounts server | Link again: the approval address, code and poll token out; then the approval that came back in. Only the Portico Account that owns the server can approve; nobody is signed out |

Remote security in force (`remoteSecurity` in `GET /v1/admin/remote-access`): `chosen`, `active` and, while Enhanced is chosen but not active, `waiting` with its `message`. `setup_not_finished`; `no_approved_address` (Enhanced never refuses everything: with no Portico address, own address or added address it works as Standard); `no_home_networks` (the server knows no home networks, so everyone would count as outside: Enhanced never turns on by itself there, and the owner sets home networks or sends `enhancedWithoutHome`); `certificate_not_ready` (the Portico certificate, or the owner's own, is not usable yet), whose message is the owner's wording: "Enhanced Security requires an SSL certificate, which may take a few minutes to be issued to your server. This feature will be enabled automatically once that certificate is available." It turns on by itself when the certificate arrives, and `problem` says why while it cannot be issued. Every `PUT` of the document that would refuse the connection making it answers `409 would_refuse_this_route` unless it carries `confirmRefusal`; `allowThisAddress` approves that connection's host and port in the same save. A `PUT` that changes `signInFromOutside` to `owner` while the server knows no home networks answers `409 would_refuse_sign_in` unless it carries `confirmOwnerOnly` (its own question: `confirmRefusal` does not answer it), and the warning `owner_only_no_home` shows while that holds.

Risky choices are the owner's to make (the owner's principle: steer away from them, never block them). Each is warned about at the moment it is made (the `409` above, then saved with `confirmRefusal`) and stays listed in `warnings` of `GET /v1/admin/remote-access` while it holds: `remote_access_off`, `enhanced_without_address` (Enhanced kept in force with no address to allow, the document's `enhancedWithoutAddress`, which only an owner sets: the default path waits as Standard instead), `plain_http_outside` and `no_home_networks`. `PUT /v1/admin/settings/network` warns the same way when the new home list would refuse the connection making it.

Shut out anyway (remote access Off, Enhanced or Required with no way in, or home networks that no longer hold where the owner is): create an empty file named `reset-remote-access` in the data folder and restart. The `remote-access` and `network` documents go back to safe defaults (remote access On, Remote security Standard, Secure connections Preferred, the server's own home networks) and the file is removed. The 409 above names the file.

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
| `GET /v1/admin/block-presets`, `POST /v1/admin/block-presets` | Owner | Saved block presets (at most 1,000), a page at a time; make one |
| `PATCH /v1/admin/block-presets/{id}`, `DELETE /v1/admin/block-presets/{id}` | Owner | Change one (needs `If-Match`); delete one. Adding a block from a preset copies it: changing or deleting a preset never changes a channel |

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
| `POST /v1/admin/backups/inspect`, `POST /v1/admin/backups/restore` | Owner, not on a TV; restore also needs the password or PIN again | Check a listed backup or a path whole before restoring; restore (a job, then a restart; everyone signs in again). One that fails its check is refused whole. On a server linked to a Portico Account the owner has no password, so `approval` takes its place: a fresh approval by the owning Portico Account, started with `POST /v1/admin/portico/link` and sent here. The server links again with it as "Link again" does, so it works once and only for the owning account; without one the answer is `403 portico_approval_required`. The link the approval made is carried across the restore: the restored server starts on it and sends its document at once. A backup from before the server used Portico Accounts puts back a server with local sign-in, which tells Portico Services once that it has left |
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

### Library collections (3)

For the owner and administrators. A library's collection is an item of kind `collection` that
everyone with the library sees, read like any title (`GET /v1/items?library=…&kind=collection`,
`GET /v1/items/{id}`, `GET /v1/items?collection={id}`). A person's own collections are lists.

| Route | Purpose |
|---|---|
| `POST /v1/admin/libraries/{id}/collections` | Make one with a title and its titles in order (films, shows and other top-level titles of that library). Takes an `Idempotency-Key` |
| `PATCH /v1/admin/collections/{id}` | Rename, `add`, `remove` and `order` its titles. Safe to repeat: adding a title it holds changes nothing. What it changes holds against automatic grouping |
| `DELETE /v1/admin/collections/{id}` | Remove it; its titles stay. A collection whose last title leaves the server goes with it |

With the library option `groupSeries` (Group films from the same series, on unless turned off) a
collection appears by itself once two films of a series are matched in the library, named
without "Collection", and later films join it. One the owner deletes is not made again; a film
the owner takes out of one stays out, and later films still join. Turning the option off removes
the automatic collections nobody changed; one the owner renamed, reordered or edited stays as an
ordinary collection.

## Not on the wire, on purpose

`viewerFence`, `expectedRevision`, `operationId`, a "preparing" answer on a read, query handles,
lower-bound totals (with one stated exception: the count of people in a search by a restricted profile says so with
`totalIsMinimum` when it stops at 2,000), write lookups after the fact, conflict choices for offline edits, a
revision on personal state, per-provider and per-screen route families, and step-by-step
wizard routes. Each existed for the old storage design. What a person would lose without
them is nothing: the last deliberate edit wins, a `2xx` means it happened, and reads answer.
