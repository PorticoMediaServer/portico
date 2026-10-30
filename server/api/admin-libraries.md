# Administration pages

These are the owner administration surfaces: deleting media, configuring a
library, configuring a live source, DVR defaults, library channel presets and
server maintenance. Use `portico.openapi.json` for source-registered operations and the current
handlers for routes not yet reflected there. The hand-written YAML is historical.

Everything here is owner-only except `GET /v1/library-channels/{id}/logo`,
because a channel logo is part of what a viewer sees. A Hosted account carrying
the owner role is not this server's owner: administration always resolves through
the locally managed owner and the server's installed membership policy.

## Configuration document conventions

- The older configuration document family uses the response envelope: `{protocolVersion, serverId, result}`.
- One document per page, carrying a `revision` and a `digest`.
- One write envelope: `{expectedRevision, operationId, settings}`. A write whose
  `expectedRevision` does not match the stored revision is refused with
  `409 administration_conflict` and nothing is stored.
- `operationId` makes a write idempotent. Replaying the same identifier with the
  same body returns the first result rather than applying the change twice;
  replaying it with a different body is refused. Identifiers are 8–128 characters
  of `[A-Za-z0-9-_.]`.
- Configuration lists page by an opaque `cursor`. Limits are per operation;
  many default to 40, with a published maximum. A cursor is fenced to the listing that minted it, so replaying
  one against a different listing is refused with `409 stale_continuation`.
- Owner authority is checked before the work starts, again inside the same
  transaction as the write, and again before the response is written. A session
  revoked mid-request cannot complete a mutation.
- A validation refusal names the exact fields: `{"error": {"code":
  "invalid_administration_input", "fields": ["settings.folderTemplate"]}}`.
- Unknown query parameters are refused. A typo is never a silently ignored
  filter.

Storage: one table of documents keyed by scope (`library:<id>`,
`live-source:<id>`, `live`, `dvr`, `maintenance`), one table of operation
receipts, and one small table per listable thing (recording
groups, block presets, channel logos, channel number overrides, backups). No
domain state is duplicated: each page reads the domain's own tables and writes
only its configuration document.

## Deleting media

A delete is three steps, because it is the one page action that destroys data.

1. **Preview** — `POST /v1/items/{id}/delete/preview`, or
   `POST /v1/admin/media/delete/preview` with up to 200 `itemIds`. The preview
   lists every file with its size and whether it is still present, marks files
   another item also uses (those bytes stay), counts everything that refers to
   the item (collections, playlists, saved positions, library channel slots,
   published recordings), reports the library's `allowMediaDeletion`, and
   returns the `confirmation` text and the `revision`. A show, season, album,
   artist or book lists the files of everything that plays beneath it, and
   `titles` says how many titles that is.
2. **Confirm** — `confirmation` is the item's title for a single delete and the
   target count as a decimal string for a bulk one. `confirmationKind` says
   which. A mismatch is `409 confirmation_mismatch`.
3. **Delete** — `POST /v1/items/{id}/delete` or `POST /v1/admin/media/delete`
   with `{deleteFiles, confirmation, expectedRevision, operationId}`.

`expectedRevision` is the deletion fence: the sum of the `library_revisions`
revisions used by the deletion preview. Send exactly the preview revision;
a changed deletion fence refuses the operation.

`allowMediaDeletion` is off on a fresh library. Until an owner turns it on, every
delete against that library is `403 administration_denied`, and a bulk delete
that touches even one such library is refused whole rather than half-applied.

`deleteFiles: true` deletes each file from disk. Nothing is moved aside and
nothing is kept: there is no trash and no restore. Storage is touched with no
database writer held, then the titles are removed in short transactions.

- A file another title also uses stays (`filesKept`).
- A file that is no longer the one the library knew (another file has taken its
  path since the last scan) is left alone, and its title stays in the library
  with `code: file_changed`. A file storage refuses to remove gives
  `code: file_not_removed`. `failed` counts those titles.
- A file already gone from disk is simply done.

`deleteFiles: false` removes the titles and leaves the files where they are;
`filesRetained` says so, and the next scan finds them again.

Either way each profile's state for a removed title (watched, progress, rating,
favourite, My List, play count) is kept under the title's identity and given
back if the same title is added again, to this library or another.

There is no bulk delete job: a selection is deleted through the same preview
and confirmation, 200 titles at a time.

## Missing titles

A file absent from a complete scan is judged by the scanner’s missing-file
observations and safety rules. It is no longer offered or played. A title
left with no file stays in the library, shown as unavailable, with
`missingSince` on its detail; a file that is the same title (the same provider
identity, or read as the same title) brings it back with its id, metadata and
everyone's state.

A folder that cannot be read, or that is suddenly empty where it held media, is
an unmounted disk and not a list of deleted files: its scan stops and nothing
is marked.

- `GET /v1/admin/missing-titles?libraryId=&cursor=&limit=` lists missing titles,
  oldest first, with `missingSince`, `removedAt` (when the owner's rule will
  remove it) and the rule itself (`rule`, `days`).
- `POST /v1/admin/missing-titles/empty` (`{libraryId?}`) removes missing titles
  and missing files now. It works in short passes and answers
  `{titles, files, more}`; call again while `more` is true.
- The rule is two owner settings: `library.missingTitles`
  (`immediately | after_days | never`, default `after_days`) and
  `library.missingTitlesDays` (1 to 3650, default 30).

## Titles and their files

- `POST /v1/admin/titles/merge` (`{fromId, intoId}`) says two titles of one
  library are the same title. The first becomes part of the second: its files
  are versions, everyone's state for both is combined, and a show takes the
  other's seasons and episodes. Titles a provider matches to the same identity
  are merged the same way without being asked.
- `POST /v1/admin/libraries/{id}/inventory/{object}/separate` undoes a wrong
  grouping for one file.
- `DELETE /v1/admin/libraries/{id}` (`{confirmation, expectedRevision}`, the
  confirmation being the library's name) removes a library. It disappears at
  once and is taken apart in the background (`library-removal` in the console's
  jobs). Files on disk are not touched and personal state is kept as above.
- Removing a folder from a library marks the files under it missing; their
  titles then follow the rule for missing titles.

## Libraries

**Folder picking** — `GET /v1/admin/filesystem`. With no `path` it returns the
platform roots: present drive letters and the user profile on Windows, plus a UNC
hint because network neighbours cannot be enumerated portably and a
`\\server\share` path is typed; `/`, `/Volumes` and the home folder on macOS; `/`
plus every real mount point from `/proc/self/mountinfo` (pseudo filesystems
filtered) on Linux. With a `path` it returns one page of that directory, sorted
case-insensitively. Dotfiles, the Windows system folders (`$Recycle.Bin`, `System
Volume Information`, `Recovery`) and the kernel trees (`/proc`, `/sys`, `/dev`,
`/run`) are never listed, and browsing into a kernel tree is refused. At most
20,000 names are read from one directory; `truncated` says when there were more.
`writable` is probed by creating and removing a file, because mode bits answer
the wrong question on a network share. Files appear only with
`includeFiles=true`, for the pickers that choose a file rather than a folder.

**Providers** — choose a provider and language per library, or local metadata
only. `GET /v1/library-kinds/{kind}/metadata-agents` publishes the curated provider registry.
TMDB is the default for video; TheTVDB and AniList are additional choices where
the registry permits them. Music uses MusicBrainz and audiobooks use local data.
Credentials are server-wide Metadata settings (`tmdbReadToken`, `tvdbProjectKey`),
not per-library API-key overrides; a nonempty per-library key is refused.
See [library guidance](../../docs/libraries.md).

**The analysis matrix** — `GET /v1/admin/analysis-operations` publishes every
operation the server knows, grouped by what it costs:

| Cost class | Meaning | Operations |
| --- | --- | --- |
| `metadata-only` | One small read per file | `probe`, `local_metadata`, `artwork_extraction`, `subtitle_discovery`, `chapter_read` |
| `single-pass-read` | Reads the file once, no video decode | `deep_stream_analysis`, `subtitle_extraction`, `loudness`, `audio_fingerprint` |
| `decode` | Decodes frames; sustained CPU or a busy encoder | `trickplay`, `chapter_images`, `video_preview` |
| `network` | Contacts a provider off this machine | `subtitle_download`, `lyrics` |

Each operation publishes the media kinds it applies to, whether it is on by
default, whether it writes artifacts (which is why the storage page has a
category for it), and the operations it `requires`. A selection that enables an
operation without its dependency is refused by name, because the operation would
otherwise silently never run.

Intro and credits detection is not in the matrix: it is its own background
work under the server's mode, and each library's settings carry
`segmentDetection` — `server` (follow the server, the default), `off`,
`standard` or `thorough`; the choices are published as
`enumerations.segmentDetection`. See [Intro and credits detection](segments.md).

**Generated navigation** — trickplay interval, tile width and maximum tile count;
the chapter thumbnail mode (`none`, `embedded`, `generated`); and whether a video
preview is produced and how long it is. Numeric values clamp into their published
range rather than failing a save; enumerations reject.

## Live sources

Server-wide values live on `GET/PUT /v1/admin/live/settings`: stream buffer
seconds, retry window, user agent, guide days, whether logos are imported and
whether tuner discovery may send packets. Per-source values live on
`GET/PUT /v1/admin/live-sources/{id}/configuration`, and the response carries
`effective` — the defaults with this source's overrides applied — so no client
recomputes them. A null override inherits.

A source's `kind` is `playlist`, `hdhomerun`, or `xmltv-guide`: a **standalone
guide** that publishes programmes for channels that come from somewhere else and
has no stream of its own. A guide-only source therefore refuses stream tuning and
refuses to name a guide source, and a streaming source names its guide with
`guideSourceId`.

Filters are three include/exclude lists — categories, countries, keywords — each
folded for duplicates and case. An empty value list means the filter is not
applied whatever the mode says.

**Renumbering and guide mapping** — `GET/PUT
/v1/admin/live-sources/{id}/channel-map` pages the active generation's channels.
`sourceNumber` is what the source published; `number` is what the server serves —
the sequential numbering when that mode is chosen, or a per-channel override.
Overrides also carry `guideChannelId` and `hidden`. An override with an empty
number, no mapping and `hidden` false clears the row, and two overrides may not
claim the same number. The write is fenced on the source configuration's
revision, so a renumber cannot land on top of a numbering-mode change the caller
never saw.

**Discovery** — `POST /v1/admin/live-sources/discover` sends two requests from
one socket, because a device may answer either and an owner should not have to
know which: the HDHomeRun discovery packet (type `0x0002`, the tuner-type and
wildcard-device TLVs, the protocol's little-endian CRC-32) to every interface
broadcast address and the global broadcast on UDP 65001, and an SSDP `M-SEARCH`
to `239.255.255.250:1900`. Broadcasting per interface rather than setting a
platform socket option is what makes this behave the same on Windows, macOS and
Linux. A binary reply is accepted when it carries the discovery reply type; an
SSDP reply is accepted only when it identifies itself as a tuner, since every
device on the network answers `ssdp:all`. Each responder is reported with the
address its `discover.json` and `lineup.json` are served from, and `configured`
says whether a live source already points at it. The sweep waits at most ten
seconds and does nothing while `discoveryEnabled` is off, which it says.

**Logos** — `POST /v1/admin/live-sources/{id}/logos` imports the logos a source
advertises, validating each image exactly as an upload is validated. Nothing is
fetched unless the composition root supplied a fetcher and the source's effective
`logoImport` is on; otherwise the result explains why it did nothing.

## DVR

`GET/PUT /v1/admin/dvr/settings` reads and writes normalized runtime padding and retention defaults. Supported retention is `keep-all`, `keep-count` or `keep-days`. New groups snapshot these defaults for null options; direct recordings omit `options` or use `useDefaults: true`; direct rules use `useDefaults: true`. Existing intent keeps its saved options. Conversion is `none`, profile is `original`, tuner preference is `first-available`. Folder templates must be empty, sidecars and keep-until-watched false; unsupported controls are refused. The folder-token vocabulary is empty.

`GET/PUT /v1/admin/dvr/recording-permissions` is server-owner-only. A grant identifies an explicit `{authority, accountId, profileId}`, `enabled`, `inheritProfiles`, and current `revision` (zero on create). Enabling validates current target membership. Server ownership grants no implicit recording permission. A child profile inherits only when explicitly enabled by the owner; explicit profile denial overrides inherited permission. Revocation affects admission and durable capture checks without deleting existing recordings.

`GET /v1/admin/dvr/tuners` shows, per source, the tuner count, what is recording
now, and what is scheduled in the next 48 hours. `conflicts` is how many upcoming
recordings cannot all fit, computed by sweeping the start and end points against
the tuner count — so the page reports a real over-subscription rather than a
guess. A server with no live channel store reports an empty view, not a failure.

`GET/POST/PUT/DELETE /v1/admin/dvr/recording-groups` writes real DVR series rules. Creation requires explicit `owner` and `anchor` (`sourceId`, `channelId`, `generation`, `programmeId`) from the current guide; `match` is that programme's actual series ID. The selected owner needs recording permission. Keyword-only groups and unsupported folder choices are rejected. `newEpisodesOnly` requires reliable guide evidence. Null options snapshot current defaults when saved. Removing a group stops further scheduling and preserves previously scheduled recordings. Historical ownerless rows are shown disabled with `status: needs-owner-and-guide-selection`; they never acquire an inferred owner.

## Library channels

`GET /v1/admin/library-channels/criteria` publishes the selection vocabulary from
the catalog's own attribute projection: with no `field`, the fields the catalog
actually holds and how many distinct values each has; with a `field`, that field's
values with the number of items carrying each. The page therefore never offers a
filter the library cannot answer.

`GET/POST/PUT/DELETE /v1/admin/library-channels/block-presets` are named,
reusable block definitions: criteria clauses (`includes` / `excludes` over a
field), an ordering (`shuffle`, `sequential`, `recent`, `least-played`), a
duration, an optional placement in the day and an optional day set. Names are
unique after case and whitespace folding. Removing a preset leaves channels
already built from it alone.

`POST /v1/admin/library-channels/{id}/logo` takes `multipart/form-data` with a
`file` part. The container is identified from the bytes, never from a declared
content type or filename: JPEG, PNG and WebP, up to 10 MiB, 16 to 4096 pixels on
each side. The image is proved decodable before anything is written — a hostile
image never holds a write lock — and is stored under its own digest, so an
identical upload costs nothing. `GET /v1/library-channels/{id}/logo` serves it to
any signed-in viewer with a strong `ETag`.

`GET /v1/admin/library-channels/{id}/health` reports whether a channel has a
published generation, how far its schedule reaches, how many slots and candidates
it has, whether a generation is queued, and a sentence saying what is wrong: off,
never generated, a reported failure, or a schedule that came out empty because
the criteria matched nothing.

## Maintenance

**Backups.** The default destination is `<data>/Backups`. Owner-only
`GET/PUT /v1/admin/backups/settings` reads or updates `backupFolder`,
`includeArtwork` and `passphraseProtected`. The write accepts an optional
`passphrase` (12–1024 bytes); reads return only `passphraseSet`. Turning protection
off erases the stored sealed derived key material. Scheduled and manual backups read the
same live policy. A chosen destination must be an existing absolute directory
outside the data folder; the default `Backups` child is allowed. Changing it keeps
previous destinations discoverable and preserves their copies.

Backups always contain the consistent scrubbed database, `portico.toml`, `Keys`
and owner metadata, including uploaded/selected artwork and its undo history.
Downloaded artwork and previews are included only when `includeArtwork` is true.
Cache, logs, temporary work, conversions, original media and recordings are never
included. Manual copies remain until deleted; scheduled copies use the existing
keep count. Pre-restore rollback copies remain beside the live database.
Live sign-ins are scrubbed; API credentials remain subject to the existing restore
rules.

Protected backups contain `archive.pba` and a public listing summary. The archive
uses an Argon2id-derived 32-byte master (three passes, 64 MiB, four lanes,
random public 16-byte KDF salt), fresh per-archive HKDF keys, and AES-256-GCM
authenticated chunks, with ordered nonces, bound headers and a required
authenticated terminal record. Only the derived master is sealed for scheduled
backups; passphrase text is never persisted. Encrypted v1 archives and old
sealed-passphrase policy blobs are refused without conversion. Creation snapshots in private `Temp`,
then flushes and atomically publishes ciphertext on the destination. Restore
requires the passphrase; it authenticates the complete stream and verifies the
embedded manifest and database before staging. An exported `.pba` file restores
without the original server or listing summary. Unprotected backups are private
plain files and contain readable server keys. Keep the passphrase separately;
the API does not recover or return it. Rollback safety copies use the existing
private local plain-file format so interrupted restores can resume at startup.

`POST /v1/admin/backups {operationId}` answers 202 with `jobId`. Poll
`GET /v1/admin/backups` for `running`, finished `backups`, `restore` and
`lastRestore`. The current process also reports `lastBackup` with its job ID,
completion/failure outcome and stable error code, so a failed manual job is not
mistaken for success when `running` clears. `POST /v1/admin/backups/restore {operationId, source, passphrase?}` also answers
202 with a job, rather than running copying in the request. Source identifies
`backupId` or an absolute server `path`. Validation and staging run in the
background, followed by restart. Restore needs the single-instance lock and
refuses incompatible prerelease/newer schemas. Startup preserves rollback
state and refuses unresolved partial restores. See
[installation and recovery](../../INSTALLING.md) for owner steps.

**Storage care.** `GET /v1/admin/storage-usage` measures the state directory by
category: artwork, trickplay, prepared media, conversion working files,
subtitles, live buffer, downloads, logs, backups and recordings. Each
category names its directories, whether the page may clean it, whether the bytes
are regenerable, and which retention setting governs it. Recordings and backups
are content and are never cleanable. `POST
/v1/admin/storage-usage/{category}/cleanup` removes files older than the
category's retention (or an explicit `olderThanDays`), takes the category
identifier as its confirmation.

**Updates.** `GET /v1/admin/updates` reports the running build and, when the
`updates.feedUrl` owner setting names a feed, the newest build that feed lists on
the `updates.releaseChannel`. Versions compare as dotted numbers with any
`-suffix` sorting below the same numbers without one, so a release candidate
never reads as newer than the release. Nothing is installed: `autoInstall` is
always false.

**Windows and retention.** `GET/PUT /v1/admin/maintenance/settings` holds the
maintenance windows — a day set, a start minute, a duration, an optional IANA
timezone and the tasks the window admits — with cadence presets (`nightly`,
`weekdays`, `weekends`, `weekly`, `custom`) published alongside the day set each
means, so a client never asks an owner to tick seven boxes. Retention is per
category with deliberately long maxima (ten years for artwork, trickplay,
prepared media and subtitles), because an owner with a large disk
should be able to keep them effectively forever. Backups are kept by count
(`backupKeepCount`), not by age.

**Moving a server.** Stop Portico, copy the whole state folder to the new
machine, point Portico at it (`PORTICO_DATA_DIR`), and start the same build first. Verify the resulting library and network state.
Portico's own files are stored absolute and rebased to the new folder on first
start. Media library paths are absolute too and still point at the old
machine's mounts, so change them in the console when the new machine mounts
media elsewhere.

## Settings registry

Four server-wide values live in the owner settings registry rather than in these
documents, because they are server policy that other surfaces read:
`updates.releaseChannel`, `updates.feedUrl`, `library.missingTitles` and
`library.missingTitlesDays`. They are read and written through the console
settings endpoints like every other registry field. `updates.releaseChannel`
falls back to `stable`, and the missing-titles rule to thirty days, when a
client omits them.

`GET /v1/admin/dvr/recording-owners?limit=100&cursor=…` supplies current account/profile choices for the owner-only recording-permission editor. The result has `items` (`owner`, `accountName`, `profileName`) and `nextCursor`; limit is 1–200. Continue through empty filtered pages until the cursor is empty. All candidates pass current membership/claim checks; this endpoint neither grants permission nor contacts Hosted. Stale, revoked or deleted identities are not returned.

Scans accept `?mode=full` or `?mode=integrity` on the existing scan endpoints.
Without an explicit mode, a new source uses first-import mode; subsequent scans
use bounded source traversal. Local scans read file facts every time, because
directory timestamps do not prove that files were unchanged. Managed adapters
use their own paged listing protocol. Full/integrity modes retain their
explicit source checks. All modes retain the existing
root-identity, cancellation, and stable-coverage missing-file safeguards. Missing is immediate after a
complete admitted scan; an inaccessible root or protected subtree does not
authorize absence.
