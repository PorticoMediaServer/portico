# server-next: seven areas, and how they fit together

The outline for the areas that had no code when it was written: setup and remote access (2),
playing together and on other devices (8), downloads (9), reading (10), Live TV and recordings
(11), the owner console (12) and outside tools (13). The product rules are the definitions in
the private repo (`design/definition/NN-*.md`); the tables are in `internal/db/schema.sql` and
`schema_areas.sql`; the routes are in `API.md`. This file says what to build where, and what
the areas share. Build inside it; if it is wrong, change it in the same pull request and say so.

Every area follows the finished ones: handlers and wire shapes in `internal/api/<area>.go`,
all SQL in `internal/db/<area>.go`, the behaviour in its own package, state that is not in a
table held in memory by that package's service.

## What the areas share (one of each)

- **Owner settings.** Package `internal/settings` (built first, by the console). An area
  declares a document: a name, a Go struct whose zero value plus defaults works, a check, and
  which fields are secrets. It is one `settings` row (JSON, `revision` is the `ETag`). Anyone
  reads it with `settings.Read[T](name)`, which answers from memory, and may subscribe to
  changes, so no setting needs a restart and no request reads the table. A secret is sealed
  with the server key and is write-only on the wire. A value set in the config file wins and
  says so. Routes: `GET /v1/admin/settings`, `GET`/`PUT /v1/admin/settings/{name}`. Other
  `settings` rows are the server's own notes and have no route: `sign_in_kind` (kept under that
  name: `portico_account`, absent means local), the link to Portico Services, which alerts
  have been announced, the last hardware test, when timed work last ran, block presets.
- **Notices.** Package `internal/notices`: `Send(to, kind, params, ref)` where `to` is an
  account, a profile, the owner, the owner and administrators, or everyone. It writes
  `notifications` rows and a change. Nothing else tells a person anything; there is no email
  and no push.
- **Events.** The feed reads `changes`. A durable thing that changes writes a `changes` row
  under a new topic code (notice, download, channel, recording, settings). Things that live in
  memory (a group, a device being asked to play, a session, a tuner countdown) must not write
  a row per pause: area 8 adds one in-memory publisher that the feed merges in, with no
  promise across a reconnect, after which the app rereads.
- **Webhooks.** Package `internal/webhooks`: any area calls `Emit(event, facts)`; it writes
  one `outbox` row per webhook that wants the event and a sender delivers them in order. Title
  events come from reading `changes` (with a cursor in a `settings` row), a minute late.
- **Prepared copies.** Package `internal/prepare`: `Want(media, recipe)` answers a
  `prepared_media` row, making it and its `prepare` job if needed; `Open(id)` gives the file.
  It converts through the existing engine under the owner's background conversion limit and
  yields to people watching. Downloads is its first caller; nothing else makes its own copies.
- **Long work** is a row in `jobs`: `prepare`, `guide_refresh`, `channel_schedule`, `backup`,
  `restore`. **The daily tidy-up** is one timer owned by the console; each area registers a
  function for it (expired prepared copies, old notices and reports, recordings past their
  keep rule, ended reminders, finished outbox rows, deleted annotations, each Library
  Channel's next day).
- **Keys.** `internal/auth` turns a key, token or app password into a person and profile in
  one function, and refuses it there whenever that person could not sign in. Every route,
  `/v1/mcp` and the compatible routes go through it.
- **Switches.** What a profile may do is `profiles.allow_downloads`, `allow_live_tv`,
  `allow_dvr`, `allow_watch_together`, each within the same switch on `users`, which also has
  `allow_tokens`; the owner may always record. Each area adds its switch to the restrictions
  routes and its count to the profile deletion preview.

## 2. Setup, remote access and the Portico Account link

- Packages: `internal/setup` (the two setup paths, switching the kind of sign-in, changing
  owner), `internal/remote` additions (reachability check, port mapping, certificate renewal),
  `internal/hosted` (everything that talks to Portico Services, and nothing else may).
- Durable, with no new table: `sign_in_kind` and a switch in progress (`settings`);
  `users.portico_account_id` and `users.state` (waiting, left, not moved); the setup code and
  move codes (`auth_codes` kinds, hash only); the certificate, its key and the server's
  identity key (files in `Keys/`); the link to Portico Services, its trusted keys and the one
  "this server has left" message with its give-up time (`settings`, sealed where secret); the
  `remote-access` and `network` documents.
- Memory: router mappings (asked again after a restart), "reachable from outside" (always
  just checked), outside addresses refused under Enhanced since start.
- Interfaces: `hosted` is the only package with a Portico address in it, and every call
  checks `sign_in_kind` first, so a local server contacts nothing; one test runs a local
  server end to end and fails on any such request. Area 8's Cast and `POST /v1/hosted/notices`
  go through it. Sign-in asks `setup` whether an account is waiting.
- Order: (1) `remote-access` document, own address and certificate files, Secure connections
  and Remote security; (2) switch between kinds, move codes, Finish now, Start over, change
  owner, all on a local-to-local test first; (3) the Portico path of setup and switching;
  (4) Portico address, certificate and port mapping.
- `Review: full`: all of it. Every step here makes or ends sign-ins, and a certificate request
  must be signed with the server's own identity key.

## 8. Playing together and on other devices

- Packages: `internal/together` (groups), `internal/receivers` (devices open to play, requests
  and moves), Cast routes in `internal/api/cast.go` over `auth`.
- Durable: nothing of a group or a move. Only `allow_watch_together`, two `preferences`
  ("visible in Watch Together" on the profile, "play from other devices" on the device), the
  Cast application id (`settings`), a Cast pairing code (`auth_codes`) and the receiver's
  sign-in (`sessions.scope = 'cast'`). A TV remembers on the TV which devices it allowed.
- Memory: groups, members, the shared clock, the group queue, invitation codes, receivers and
  what is being asked of them. A restart ends them, and the apps say so.
- Interfaces: a group never plays anything; each member starts an ordinary playback session
  and `together` only tells it where to be. A move is a request the second device answers by
  starting its own session; the first stops when the second reports playing. Controlling a
  device is the existing session `PATCH` and `DELETE`. This area owns the in-memory event
  publisher (above). Cast exists only when `sign_in_kind` is `portico_account`.
- Order: (1) the event publisher; (2) receivers and moves; (3) groups; (4) Cast, after area
  2's Portico path.
- `Review: full`: Cast code and redeem (a sign-in), receiver requests (who may make a device
  play), and how a group shows a title a member may not see. The rest is `quick`.

## 9. Downloads and offline

- Packages: `internal/prepare` (shared, above) and `internal/downloads`.
- Durable: `prepared_media`, `downloads`, `download_items`; the `downloads` settings document;
  per-device download quality in `preferences`. Memory: nothing but running conversions.
- No table for offline permission, for what a device holds, or for offline progress: `sync`
  answers "plays until now plus 30 days", the device says what it holds, and plays made
  offline arrive through `POST /v1/items/state:batch`.
- Interfaces: the choices and sizes come from playback's quality ladder (`playback.Qualities`),
  capped by the owner's limit; an Original is fetched straight from the library file through
  the same byte route playback uses, a smaller copy through `prepare`. A title in several
  files is every part for Original and one joined file when prepared. The byte family issues a
  grant per request item and must charge the range it actually serves.
- Order: (1) `prepare` with its job; (2) options, ask, list, remove for one title at
  Original; (3) prepared qualities; (4) shows, seasons, albums, playlists and "keep the
  next"; (5) `sync`; (6) the owner's page.
- `Review: full`: the byte routes and their grants (which file the server opens), and `sync`'s
  decision of what a device must remove. The rest is `quick`.

## 10. Reading

- Package: `internal/reading` (opening EPUB, PDF, CBZ and image folders first; CBR, CB7, MOBI
  and AZW3 after, with their readers).
- Durable: `annotations`. The place is `user_item_state.position_text` (the exact spot and how
  far through, one small JSON value) with `position_at`; reader defaults are `preferences` by
  kind of device. Annotations and the place go when the book leaves the trash. Memory: open
  archives, a short while.
- Interfaces: a book is an item; `GET /v1/items/{id}/book` answers media grants and the bytes
  are served like playback's. Nothing is open on the server. Reading adds "Continue your
  books" to Home and the annotation count to the profile deletion preview.
- Decide whether a page fits before decoding it, count what is still being sent to slow
  readers, and refuse the one page, never the book.
- Order: (1) the book route and bytes for EPUB and PDF; (2) comics; (3) annotations and the
  batch route; (4) the second group of formats.
- `Review: full`: the three byte routes and archive reading (untrusted files; a part is named
  by the book's own list, never by the request). Annotations are `quick`.

## 11. Live TV, recordings and Library Channels

- Packages: `internal/live` (sources, import, the guide, tuners, shared inputs), 
  `internal/dvr` (rules, bookings, the recorder), `internal/librarychannels` (the builder and its
  schedule).
- Durable: `live_sources`, `channels`, `profile_channels`, `recording_rules`, `recordings`,
  `reminders`, `library_channels`; the `live` and `dvr` documents; block presets (one
  `settings` row); recorded files in the recordings folder. Memory: who is watching, tuners in
  use, one shared input per channel with its rewind stretch (files under the conversion
  folder, emptied at start), countdowns, clashes, a recording's progress.
- `guide.db` holds every programme, tuner listings and Library Channel schedules alike, so
  the guide reads one way. `internal/db` opens it with its own writer:

  ```sql
  CREATE TABLE programmes (
    source_id INTEGER NOT NULL, guide_key TEXT NOT NULL,   -- channels.guide_source_id, guide_key;
    starts_at INTEGER NOT NULL, ends_at INTEGER NOT NULL,  -- source 0 and the channel's public id
    programme_key TEXT NOT NULL, series_key TEXT,          -- for a Library Channel
    title TEXT NOT NULL, episode_title TEXT, season_number INTEGER, episode_number INTEGER,
    summary TEXT, rating TEXT, rating_age INTEGER, flags INTEGER, image_url TEXT,
    item_id INTEGER,                                       -- a Library Channel's title (items.id)
    PRIMARY KEY (source_id, guide_key, starts_at)
  ) WITHOUT ROWID;
  CREATE INDEX programmes_key ON programmes (source_id, guide_key, programme_key);
  CREATE INDEX programmes_series ON programmes (series_key, starts_at) WHERE series_key IS NOT NULL;
  CREATE INDEX programmes_title ON programmes (title, starts_at);
  ```

  A refresh replaces one source's rows in one transaction, so the old guide shows until the
  new one is whole. If the file is lost, listings are fetched again and each Library Channel
  is scheduled again from now, carrying on from `schedule_state`; nothing a person made is in
  it. Bookings and reminders copy the facts they need.
- A Library Channel's `builder` document: `libraries`, `kinds`, `match` (all or any),
  `conditions` (conditions and one level of groups, in the form of `POST /v1/items/query`),
  `mentions`, `addedWithinDays`, `always` and `never` (item ids, 500 each), `order`, `sort`,
  `episodes`, `marathon`, `avoidRepeats`, `whenDone`, `weights` (100), `blocks` (50, never
  overlapping: days, from, to, a whole copied selection or the channel's own, what happens at
  the end, pick up or start again, what plays if nothing matches), `quality`, `logoOnScreen`.
  Unknown conditions are kept as they are. The schedule job reads the catalogue, never the
  other way round.
- Interfaces: watching is `POST /v1/playback/sessions` with `channel`. Playback asks
  `live.Tune(channel, viewer)`: for a source's channel it answers a shared input (a growing
  local stream and its facts) that playback plans like a file with no end, or `no_tuner` with
  what is using them; the recorder is one more caller of `Tune`. For a Library Channel it
  answers the item on now and how far in, and playback starts an ordinary session there that
  saves no place and no watched mark and writes its play for the owner only. A recording is an
  ordinary title: `dvr` writes the file into the Recorded TV library's folder (made with the
  first booking) and creates its item, version and file rows through the scanner's own insert
  functions with the guide's details, so a later scan finds nothing new.
- Order: (1) sources, import, channels, the guide file and guide routes; (2) watching a
  channel; (3) bookings, rules and the recorder; (4) reminders; (5) Library Channels: storage
  and schedule job, then preview, templates, blocks and presets. (5) needs only (1).
- `Review: full`: adding a source (the server fetches an address and parses an uploaded
  file), what a profile may watch and record (audience, switches, rating), the recorder's
  writes and Delete (files on disk), and Library Channel visibility. The rest is `quick`.

## 12. Owner console and operations

- Packages: `internal/settings` and `internal/notices` (shared, above), `internal/console`
  (dashboard, alerts, load, statistics, the tidy-up timer), `internal/backup`.
- Durable: `settings`, `notifications`, `reports`, `daily_totals`, and what exists already:
  `jobs`, `audit_log`, `play_history`, `playback_route_failures`. Backups and logs are files; a
  backup's description is a small file beside it. Memory: Now playing, the load charts, alerts
  (worked out while their cause lasts), a pause of background work, "record more detail".
- Interfaces: playback, downloads and Live TV read `streaming`, `downloads`, `live` and `dvr`
  through `settings`; playback fills `play_history`'s new columns (person, library, away,
  delivery) and counts into `daily_totals` through `console.Count(metric, n)`, written when a
  day ends. "Keep play history" removes the owner's old rows only once the profile's own
  History no longer needs them; nothing else removes a play. A backup lists the working
  credentials inside it (the server's keys, source sign-ins, a remote source's key file).
- Order: (1) `settings` with the `general` and `streaming` documents, and move the three
  built documents onto it; (2) `notices`, the inbox and reports; (3) sessions, Play history,
  statistics, jobs; (4) storage, logs, the activity record, hardware; (5) backup and restore;
  (6) updates, restart, support export, the folder explorer and library order.
- `Review: full`: restore (refuse a database with anything the schema does not define, bring
  back nothing that can run a command, end every sign-in), backup, the folder explorer (a path
  from a request, by design: owner only, folders only), settings that hold secrets, support
  export, restart and Fix permissions. The rest is `quick`.

## 13. Outside tools

- Packages: `internal/webhooks` (shared, above), `internal/mcp`, `internal/compat` (the
  Jellyfin-compatible routes), `internal/dlna`.
- Durable: `api_keys` (now with the profile a key acts as), `webhooks`, `outbox`, the
  `integrations` document, the DLNA switch. Memory: an assistant's connection, the request
  count that slows a busy key, DLNA's announcements.
- Interfaces: every tool call becomes an ordinary call made as the key's person and profile.
  `mcp` and `compat` hold no data and no rights of their own: each of their operations calls
  the same Go functions the `/v1` handlers call. Compatible ids are worked out from Portico's
  own, and a compatible sign-in's token is derived from its app password, so nothing is
  stored and both end together. DLNA reads the catalogue directly, serves original files to
  the home networks only, never through a proxy, and writes nothing.
- Order: (1) the key check with its limits, and the profile on a key; (2) webhooks; (3)
  "files changed" and tool notices; (4) the compatible routes; (5) the assistant address; (6)
  DLNA.
- `Review: full`: the key check, the compatible sign-in, the assistant address, and DLNA (it
  opens files with no sign-in). Webhooks are `quick`.

## Put-off work that lands here

| `DEFERRED.md` line | Home |
|---|---|
| 3, 81 Cast receiver stop; subtitles a cast device needs burned in | Area 8, with Cast |
| 7 strict parsers; 21 search inside every book | Area 10 (the special mark-all goes; every-book search stays put off) |
| 22 failed conversions | Area 12: the log only |
| 47 daily tidy-up; 48 library order, folder explorer | Area 12 |
| 57 profile switches and deletion counts | Each area, as above |
| 58 Convert when needed, away limits, graphics device; 63, 83 hardware answer kept | Area 12: the `streaming` document; a `settings` row for the last test |
| 67, 134 keys respecting limits; an app password's first use | Area 13, step 1 |
| 74 Home rows | Area 10 (Continue your books), area 11 (On now) |
| 108, 113 title data download switch; provider keys | Area 12 moves the metadata document onto `settings`; the TMDB key is built in |
| 122 a backup names the source key file; a remote source's speed limit | Area 12 (backups); the source's own settings |
