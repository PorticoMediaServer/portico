# server-next: the table list

The plan is 73 tables; the budget is 90. A table is added to `internal/db/schema.sql` when its
area is built, after asking what a user would lose without it. **In schema** means the table
exists today. Columns are documented in `schema.sql` itself.

| Area | Table | In schema | What a user would lose without it |
|---|---|---|---|
| System | `schema_migrations` | yes | Upgrades that know where to start |
| | `settings` | yes | Server settings |
| | `jobs` | yes | Scans, matching and bulk actions that survive a restart and show progress |
| | `idempotency_keys` | yes | A lost reply to "add to playlist" would add the track twice |
| | `audit_log` | yes | The owner's record of admin and security actions |
| | `changes` | yes | Open screens updating by themselves; search catching up after a crash |
| | `outbox` | with webhooks | Webhook deliveries that survive a restart, and their history page |
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
| | `subtitles` | with playback | Downloaded and uploaded subtitles and timing offsets, which a rescan cannot rebuild |
| | `tags`, `item_tags` | yes | Genres, studios, labels and the filters on them |
| | `people`, `person_external_ids`, `credits` | yes | Cast and crew, and a person's other titles, without duplicate people |
| | `item_relations` | yes | Collections, extras, trailers, guest artists, books in a series |
| | `images` | yes | Artwork, and the artwork a person chose or uploaded |
| | `metadata_cache` | with metadata | Refreshing metadata without refetching everything from providers |
| | `lyrics` | with playback | Lyrics |
| | `media_artifacts` | with playback | Seek previews, waveforms and keyframe positions already computed |
| | `track_embeddings` | with music mixes | Mixes and radio from sound similarity |
| Users and access | `users`, `profiles` | yes | Accounts and viewing profiles |
| | `devices` | yes | The device list, and revoking a lost device |
| | `device_capabilities` | yes | Starting playback on another device |
| | `library_access`, `profile_restrictions` | yes | Keeping a library or a label away from a member or a child |
| | `sessions` | yes | Staying signed in; ending a sign-in |
| | `auth_codes` | yes | TV sign-in codes, password reset, two-step sign-in, recovery codes |
| | `invitations` | yes | Inviting people |
| | `auth_limits` | yes | A guessing limit that a restart does not reset |
| | `api_keys` | yes | Tools such as Sonarr and Home Assistant |
| | `preferences` | yes | Per-person and per-device settings |
| | `notifications`, `notification_reads` | with the console | The notice inbox |
| | `webhooks` | with webhooks | Outgoing webhooks |
| | `reports` | with the console | Feedback and problem reports |
| Remote access | `certificates`, `hosted_links`, `hosted_trust_keys` | with remote access | Built-in remote access and the Portico Account link |
| Per-user state | `user_item_state` | yes | Watched, progress, favourites, My List, ratings |
| | `play_history` | yes | History and play statistics |
| | `lists`, `list_entries`, `list_shares` | yes | Playlists, collections, saved views, and sharing them |
| | `removed_state` | yes | Watched marks, progress, ratings and favourites coming back when a title that was removed is added again: an upgrade by Radarr or Sonarr deletes a title and adds it again |
| | `play_queues`, `queue_entries` | yes | A queue that survives a restart and moves between devices |
| | `annotations` | with reading | Highlights, bookmarks and notes in books |
| Playback and downloads | `playback_route_failures` | yes | Not hitting the same known-bad playback route after every restart |
| | `prepared_media`, `downloads`, `download_items` | with downloads | Offline downloads |
| Live TV and DVR | `live_sources`, `guide_sources`, `channels`, `profile_channels`, `recording_rules`, `recordings`, `recording_owners`, `reminders`, `virtual_channels` | with Live TV | Live TV, the guide, recordings, library channels |
| `guide.db` | `programmes` | with Live TV | The TV guide |
| `search.db` | `search_titles` (full text), `meta` | yes (`internal/db/search.sql`) | Search by title and by name; the file is thrown away and rebuilt at will |
| | `book_text` | with reading | Search inside a book |
