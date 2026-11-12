# server-next: the table list

The plan is 69 tables; the budget is 90. A table is added to the schema when its area is
built, after asking what a user would lose without it. **In schema** means the table exists
today. The schema is two files loaded as one, `internal/db/schema.sql` (the core) and
`internal/db/schema_areas.sql` (the areas built on it), because a source file stops at 1,000
lines; columns are documented there. How the areas use these tables is in `AREAS.md`. How to
change the schema, before and after the first release, is rule 9 of `RULES.md`.

| Area | Table | In schema | What a user would lose without it |
|---|---|---|---|
| System | `schema_migrations` | yes | Upgrades that know where to start, and background upgrade work that carries on after a restart |
| | `settings` | yes | Server settings |
| | `jobs` | yes | Scans, matching and bulk actions that survive a restart and show progress |
| | `idempotency_keys` | yes | A lost reply to "add to playlist" would add the track twice |
| | `audit_log` | yes | The owner's record of admin and security actions |
| | `changes` | yes | Open screens updating by themselves; search catching up after a crash |
| | `outbox` | yes | Webhook messages that survive a restart, and each webhook's delivery log |
| | `daily_totals` | yes | Statistics for the whole life of the server: plays, watch time, bandwidth, a number a day |
| Libraries and files | `libraries` | yes | Libraries |
| | `library_paths` | yes | The folders in a library |
| | `remote_sources` | yes | WebDAV and rclone storage |
| | `directories` | yes | Fast rescans: unchanged folders are skipped |
| | `files` | yes | Knowing what is on disk and what went missing |
| Catalogue | `items` | yes | Everything a person browses |
| | `item_keys` | yes | A rescan would duplicate titles, and merged titles would split again |
| | `item_external_ids` | yes | Matching to providers; tools that look titles up by TMDB or TVDB id |
| | `media` | yes | Several versions of one title |
| | `media_parts` | yes | Split films, CUE albums, two episodes in one file, discs |
| | `media_streams` | yes | Audio and subtitle choice; the playback decision |
| | `media_markers` | yes | Chapters, skip intro, skip credits |
| | `subtitles` | yes | Downloaded and uploaded subtitles, which a rescan cannot find again |
| | `tags`, `item_tags` | yes | Genres, studios, labels and the filters on them |
| | `people`, `person_external_ids`, `credits` | yes | Cast and crew, and a person's other titles, without duplicate people |
| | `item_relations` | yes | Collections, extras, trailers, guest artists, books in a series |
| | `images` | yes | Artwork, and the artwork a person chose or uploaded |
| | `metadata_history` | yes | The editor's History and "Undo to before this": a title's last 20 changes made by people |
| | `lyrics` | yes | Lyrics fetched, read from tags or pasted by the owner |
| | `media_artifacts` | with playback | Seek previews, waveforms and keyframe positions already computed |
| | `track_embeddings` | yes | Mixes and radio from sound similarity |
| Users and access | `users`, `profiles` | yes | Accounts and viewing profiles |
| | `devices` | yes | The device list, and revoking a lost device |
| | `device_capabilities` | yes | Starting playback on another device |
| | `library_access`, `profile_restrictions` | yes | Keeping a library or a label away from a member or a child |
| | `account_restrictions` | yes | The owner keeping a label away from every profile of a member's account, where the member cannot lift it |
| | `sessions` | yes | Staying signed in; ending a sign-in |
| | `auth_codes` | yes | TV sign-in codes, password reset, two-step sign-in, recovery codes |
| | `invitations` | yes | Inviting people |
| | `auth_limits` | yes | A guessing limit that a restart does not reset |
| | `api_keys` | yes | Tools such as Sonarr and Home Assistant |
| | `preferences` | yes | Per-person and per-device settings |
| | `notifications` | yes | The notice inbox. Read and archived are on the row, so there is no second table |
| | `webhooks` | yes | Outgoing webhooks |
| | `reports` | yes | "Report a problem", crash reports, and the owner's reply |
| Per-user state | `user_item_state` | yes | Watched, progress, favourites, My List, ratings |
| | `play_history` | yes | Each person's History, and the owner's Play history, kept for good |
| | `lists`, `list_entries`, `list_shares` | yes | Playlists, collections, saved views, and sharing them |
| | `play_queues`, `queue_entries` | yes | A queue that survives a restart and moves between devices |
| | `annotations` | yes | Highlights, bookmarks and notes in books |
| Playback and downloads | `playback_route_failures` | yes | Not hitting the same known-bad playback route after every restart |
| | `prepared_media` | yes | A smaller copy the server already made would be made again for every person and after every restart |
| | `downloads`, `download_items` | yes | What a device asked for ("keep the next 3"), and "3 of 9 ready" |
| Live TV and DVR | `live_sources` | yes | Sources of channels and of listings (a guide alone is a source of another kind) |
| | `channels` | yes | Channels, their numbers and listings; a Library Channel is a channel too |
| | `profile_channels` | yes | Each profile's favourite and hidden channels |
| | `library_channels` | yes | The Library Channel builder: what plays, the weekly hours, and where each show had got to |
| | `channel_schedule` | yes | Each Library Channel's week: what has aired and what is on never change, also after a restore or a lost guide file |
| | `block_presets` | yes | Blocks the owner saved to reuse on any Library Channel |
| | `recording_rules`, `recordings` | yes | Series rules; bookings, what is recording, and History. A recorded programme is an ordinary title in the one Recorded TV library |
| | `reminders` | yes | Reminders that follow a profile to every device |
| `guide.db` | `programmes` | with Live TV | The TV guide. Library Channel programmes in it are a copy of `channel_schedule` |
| `search.db` | `search_titles` (full text), `meta` | yes (`internal/db/search.sql`) | Search by title and by name; the file is thrown away and rebuilt at will |
| | `book_text` | with reading | Search inside a book |

## Counted

69 planned: 67 in the schema today, less `removed_state` (going: the scanner now forgets a
title when its trash is emptied), plus `media_artifacts` still to come (`track_embeddings` is in the schema now)
and `account_restrictions`, which is in an open pull request.

Planned once and dropped, with what does the job instead:

| Dropped | Instead |
|---|---|
| `certificates`, `hosted_links`, `hosted_trust_keys` | Key and certificate files in the data folder's `Keys/`, and `settings` rows: there is one link and one certificate |
| `notification_reads` | `read_at` and `archived_at` on the `notifications` row |
| `guide_sources` | A `live_sources` row of kind `guide` |
| `recording_owners` | One shared Recorded TV library; a recording says who booked it |
| `virtual_channels` | `library_channels`, hanging from a `channels` row |

No table, on purpose: watch-together rooms, devices open to play and moves between them, live
sessions, tuners in use, the rewind stretch, a recording's progress, server load (memory);
backups and logs (files); schedules (settings and a timer); a history of failed conversions
(the server log); what each device holds of its downloads (the device knows).
