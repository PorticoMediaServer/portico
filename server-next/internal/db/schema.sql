-- Portico main database.
--
-- Conventions (see server-next/RULES.md):
--   * Times are whole milliseconds since 1970 UTC, in columns ending _at. Durations end _ms.
--   * Anything the API addresses has an integer id for joins and an opaque public_id for the wire.
--   * Every reference is a foreign key with an index. Foreign keys are always enforced.
--   * Small tables are STRICT and may list allowed values in a CHECK.
--   * Large tables (marked "large") are not STRICT and carry no value lists: adding a column
--     to a STRICT table reads every row, and changing a CHECK rewrites the table. Their kind
--     and role columns are integer codes defined in internal/db/codes.go and checked in the
--     one Go function that inserts into each table.
--   * No triggers. Counts kept on a row are updated in the same transaction as their cause.
--   * Before the first release this file is edited in place. There are no migrations yet.

-- ---------------------------------------------------------------------------------------
-- A. System
-- ---------------------------------------------------------------------------------------

CREATE TABLE schema_migrations (
  version    INTEGER NOT NULL PRIMARY KEY,
  applied_at INTEGER NOT NULL
) STRICT;

-- Server settings: one row per settings document, as JSON.
CREATE TABLE settings (
  key        TEXT    NOT NULL PRIMARY KEY,
  value      TEXT    NOT NULL,
  revision   INTEGER NOT NULL DEFAULT 1,
  updated_at INTEGER NOT NULL
) STRICT, WITHOUT ROWID;

-- All background work. A job must be safe to run twice. Asking twice for the same work is one job.
CREATE TABLE jobs (
  id            INTEGER NOT NULL PRIMARY KEY,
  public_id     TEXT    NOT NULL UNIQUE,
  kind          TEXT    NOT NULL,
  target        TEXT    NOT NULL,
  state         TEXT    NOT NULL DEFAULT 'waiting' CHECK (state IN ('waiting', 'running', 'failed', 'done')),
  priority      INTEGER NOT NULL DEFAULT 0,
  run_after_at  INTEGER NOT NULL,
  attempts      INTEGER NOT NULL DEFAULT 0,
  last_error    TEXT,
  payload       TEXT,
  progress_done INTEGER NOT NULL DEFAULT 0,
  progress_total INTEGER,
  owner_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
  created_at    INTEGER NOT NULL,
  started_at    INTEGER,
  finished_at   INTEGER,
  UNIQUE (kind, target)
) STRICT;
CREATE INDEX jobs_next ON jobs (state, priority DESC, run_after_at);
CREATE INDEX jobs_owner ON jobs (owner_user_id) WHERE owner_user_id IS NOT NULL;

-- A creating request sent twice with the same Idempotency-Key creates one thing.
-- Rows are kept 35 days; clients drop an unsent edit at 30.
CREATE TABLE idempotency_keys (
  user_id       INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  key           TEXT    NOT NULL,
  request_hash  TEXT    NOT NULL,
  created_ref   TEXT    NOT NULL,
  created_at    INTEGER NOT NULL,
  PRIMARY KEY (user_id, key)
) STRICT, WITHOUT ROWID;
CREATE INDEX idempotency_keys_age ON idempotency_keys (created_at);

-- Admin and security actions. The actor's name is copied so the record outlives the account.
CREATE TABLE audit_log (
  id            INTEGER NOT NULL PRIMARY KEY,
  at            INTEGER NOT NULL,
  actor_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
  actor_name    TEXT    NOT NULL,
  action        TEXT    NOT NULL,
  target        TEXT,
  detail        TEXT,
  remote_addr   TEXT
) STRICT;
CREATE INDEX audit_log_at ON audit_log (at);
CREATE INDEX audit_log_actor ON audit_log (actor_user_id) WHERE actor_user_id IS NOT NULL;

-- large. A bounded, numbered list of what changed. Two readers: the search indexer and the
-- clients' events feed. It records deletions too, so it has no foreign keys. Old rows are pruned;
-- a reader whose cursor is older than the oldest row rereads everything.
CREATE TABLE changes (
  id         INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
  at         INTEGER NOT NULL,
  topic      INTEGER NOT NULL,
  op         INTEGER NOT NULL,
  ref_id     INTEGER NOT NULL,
  library_id INTEGER,
  profile_id INTEGER
);

-- ---------------------------------------------------------------------------------------
-- B. Libraries and files
-- ---------------------------------------------------------------------------------------

CREATE TABLE libraries (
  id                INTEGER NOT NULL PRIMARY KEY,
  public_id         TEXT    NOT NULL UNIQUE,
  name              TEXT    NOT NULL,
  kind              TEXT    NOT NULL,
  language          TEXT,
  metadata_provider TEXT,
  options           TEXT,
  position          INTEGER NOT NULL DEFAULT 0,
  scan_number       INTEGER NOT NULL DEFAULT 0,
  last_scan_started_at  INTEGER,
  last_scan_finished_at INTEGER,
  revision          INTEGER NOT NULL DEFAULT 1,
  created_at        INTEGER NOT NULL,
  updated_at        INTEGER NOT NULL
) STRICT;

-- WebDAV, rclone and similar. The secret is encrypted with the server key.
CREATE TABLE remote_sources (
  id         INTEGER NOT NULL PRIMARY KEY,
  public_id  TEXT    NOT NULL UNIQUE,
  kind       TEXT    NOT NULL,
  name       TEXT    NOT NULL,
  config     TEXT    NOT NULL,
  secret     BLOB,
  state      TEXT    NOT NULL DEFAULT 'ok',
  last_error TEXT,
  revision   INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

CREATE TABLE library_paths (
  id               INTEGER NOT NULL PRIMARY KEY,
  library_id       INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  remote_source_id INTEGER REFERENCES remote_sources(id) ON DELETE RESTRICT,
  path             TEXT    NOT NULL,
  state            TEXT    NOT NULL DEFAULT 'ok',
  last_error       TEXT,
  last_scan_at     INTEGER
) STRICT;
CREATE UNIQUE INDEX library_paths_place ON library_paths (library_id, ifnull(remote_source_id, 0), path);
CREATE INDEX library_paths_remote ON library_paths (remote_source_id) WHERE remote_source_id IS NOT NULL;

-- large. Every folder seen, with its modified time.
CREATE TABLE directories (
  id              INTEGER NOT NULL PRIMARY KEY,
  library_path_id INTEGER NOT NULL REFERENCES library_paths(id) ON DELETE CASCADE,
  parent_id       INTEGER REFERENCES directories(id) ON DELETE CASCADE,
  rel_path        TEXT    NOT NULL,
  modified_ns     INTEGER NOT NULL,
  seen_scan       INTEGER NOT NULL,
  UNIQUE (library_path_id, rel_path)
);
CREATE INDEX directories_parent ON directories (parent_id);

-- large. Every file found: media, subtitle, artwork, NFO, lyrics. A disc title is the playlist
-- file inside the disc folder. Probe facts about the whole file live here; its tracks are
-- media_streams rows. seen_scan is the scan that added or last changed the row. problem is why
-- the file is in the library's Files skipped list, or NULL.
CREATE TABLE files (
  id            INTEGER NOT NULL PRIMARY KEY,
  directory_id  INTEGER NOT NULL REFERENCES directories(id) ON DELETE CASCADE,
  name          TEXT    NOT NULL,
  role          INTEGER NOT NULL,
  size          INTEGER NOT NULL CHECK (size >= 0),
  modified_ns   INTEGER NOT NULL,
  fingerprint   TEXT,
  seen_scan     INTEGER NOT NULL,
  missing_since_at INTEGER,
  media_id      INTEGER REFERENCES media(id) ON DELETE SET NULL,
  probed_at     INTEGER,
  probe_version INTEGER NOT NULL DEFAULT 0,
  container     TEXT,
  duration_ms   INTEGER,
  bitrate       INTEGER,
  extra         TEXT,
  problem       INTEGER,
  UNIQUE (directory_id, name)
);
CREATE INDEX files_fingerprint ON files (fingerprint) WHERE fingerprint IS NOT NULL;
CREATE INDEX files_sidecar ON files (media_id) WHERE media_id IS NOT NULL;
CREATE INDEX files_unprobed ON files (probe_version, id) WHERE probed_at IS NULL;
-- A media file found at a new place with the size and modified time of one that is gone is the
-- same file, moved.
CREATE INDEX files_same ON files (size, modified_ns) WHERE role = 1;
CREATE INDEX files_problem ON files (directory_id) WHERE problem IS NOT NULL;

-- ---------------------------------------------------------------------------------------
-- C. Catalogue
-- ---------------------------------------------------------------------------------------

-- large. Everything a person can browse: film, show, season, episode, artist, album, track,
-- audiobook, book, comic, issue, series, collection. A new media type is a new kind code.
CREATE TABLE items (
  id               INTEGER NOT NULL PRIMARY KEY,
  public_id        TEXT    NOT NULL UNIQUE,
  library_id       INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  kind             INTEGER NOT NULL,
  parent_id        INTEGER REFERENCES items(id) ON DELETE CASCADE,
  root_id          INTEGER REFERENCES items(id) ON DELETE CASCADE,
  index_number     INTEGER,
  parent_index     INTEGER,
  title            TEXT    NOT NULL,
  sort_title       TEXT    NOT NULL,
  original_title   TEXT,
  year             INTEGER,
  release_date     TEXT,
  added_at         INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL,
  content_rating   TEXT,
  rating_age       INTEGER,
  community_rating REAL,
  critic_rating    REAL,
  duration_ms      INTEGER,
  summary          TEXT,
  tagline          TEXT,
  edition          TEXT,
  match_state      INTEGER NOT NULL DEFAULT 0,
  locked_fields    TEXT,
  refreshed_at     INTEGER,
  child_count      INTEGER NOT NULL DEFAULT 0,
  leaf_count       INTEGER NOT NULL DEFAULT 0,
  last_child_added_at INTEGER,
  available        INTEGER NOT NULL DEFAULT 1 CHECK (available IN (0, 1)),
  missing_since_at INTEGER,
  merged_into_id   INTEGER REFERENCES items(id) ON DELETE SET NULL,
  revision         INTEGER NOT NULL DEFAULT 1,
  extra            TEXT
);
CREATE INDEX items_by_title ON items (library_id, kind, sort_title, id);
CREATE INDEX items_by_added ON items (library_id, kind, added_at, id);
CREATE INDEX items_by_year ON items (library_id, kind, year, id);
CREATE INDEX items_by_release ON items (library_id, kind, release_date, id);
CREATE INDEX items_children ON items (parent_id, parent_index, index_number, id);
CREATE INDEX items_descendants ON items (root_id, kind, parent_index, index_number, id);
CREATE INDEX items_merged ON items (merged_into_id) WHERE merged_into_id IS NOT NULL;

-- large. "This folder, file or embedded ID means that item." Unique, so a rescan or a repeated
-- job cannot create a second copy of a title, and a merged title stays merged.
CREATE TABLE item_keys (
  library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  kind       INTEGER NOT NULL,
  key        TEXT    NOT NULL,
  item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  PRIMARY KEY (library_id, kind, key)
) WITHOUT ROWID;
CREATE INDEX item_keys_item ON item_keys (item_id);

-- large. TMDB, TVDB, IMDb, MusicBrainz, ISBN and similar ids, for matching.
CREATE TABLE item_external_ids (
  item_id     INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  PRIMARY KEY (item_id, provider)
) WITHOUT ROWID;
CREATE INDEX item_external_ids_lookup ON item_external_ids (provider, external_id);

-- large. One playable version of an item: a 4K copy and a 1080p copy are two rows.
-- The summary columns are copied from the probe so lists can filter and show badges.
CREATE TABLE media (
  id          INTEGER NOT NULL PRIMARY KEY,
  public_id   TEXT    NOT NULL UNIQUE,
  item_id     INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  position    INTEGER NOT NULL DEFAULT 0,
  name        TEXT,
  edition     TEXT,
  container   TEXT,
  duration_ms INTEGER,
  bitrate     INTEGER,
  size        INTEGER,
  width       INTEGER,
  height      INTEGER,
  video_codec TEXT,
  audio_codec TEXT,
  audio_channels INTEGER,
  hdr         TEXT,
  added_at    INTEGER NOT NULL,
  extra       TEXT
);
CREATE INDEX media_item ON media (item_id, position);

-- large. Which file, or which range of a file, makes up each part of a version: split films,
-- CUE tracks in one file, two episodes in one file, disc clips.
CREATE TABLE media_parts (
  media_id INTEGER NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  position INTEGER NOT NULL,
  file_id  INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  start_ms INTEGER CHECK (start_ms IS NULL OR start_ms >= 0),
  end_ms   INTEGER CHECK (end_ms IS NULL OR start_ms IS NULL OR end_ms >= start_ms),
  PRIMARY KEY (media_id, position)
) WITHOUT ROWID;
CREATE INDEX media_parts_file ON media_parts (file_id);

-- large. Video, audio and subtitle tracks. Attached to the file, not the part: a 20-track CUE
-- album in one FLAC file has one set of streams. An external subtitle file has one row here.
CREATE TABLE media_streams (
  file_id        INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  stream_index   INTEGER NOT NULL,
  kind           INTEGER NOT NULL,
  codec          TEXT,
  profile        TEXT,
  language       TEXT,
  title          TEXT,
  is_default     INTEGER NOT NULL DEFAULT 0 CHECK (is_default IN (0, 1)),
  is_forced      INTEGER NOT NULL DEFAULT 0 CHECK (is_forced IN (0, 1)),
  is_hearing_impaired INTEGER NOT NULL DEFAULT 0 CHECK (is_hearing_impaired IN (0, 1)),
  bitrate        INTEGER,
  width          INTEGER,
  height         INTEGER,
  frame_rate     REAL,
  bit_depth      INTEGER,
  hdr            TEXT,
  channels       INTEGER,
  channel_layout TEXT,
  sample_rate    INTEGER,
  extra          TEXT,
  PRIMARY KEY (file_id, stream_index)
) WITHOUT ROWID;

-- large. Chapters, intros, credits, recaps and adverts, per version because timings differ.
CREATE TABLE media_markers (
  id       INTEGER NOT NULL PRIMARY KEY,
  media_id INTEGER NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  kind     INTEGER NOT NULL,
  source   INTEGER NOT NULL,
  start_ms INTEGER NOT NULL CHECK (start_ms >= 0),
  end_ms   INTEGER NOT NULL,
  title    TEXT,
  CHECK (end_ms >= start_ms)
);
CREATE INDEX media_markers_media ON media_markers (media_id, start_ms);

-- Genres, studios, networks, countries, labels, moods.
CREATE TABLE tags (
  id        INTEGER NOT NULL PRIMARY KEY,
  public_id TEXT    NOT NULL UNIQUE,
  kind      TEXT    NOT NULL,
  name      TEXT    NOT NULL,
  norm_name TEXT    NOT NULL,
  UNIQUE (kind, norm_name)
) STRICT;

-- large.
CREATE TABLE item_tags (
  item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  tag_id  INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (item_id, tag_id)
) WITHOUT ROWID;
CREATE INDEX item_tags_tag ON item_tags (tag_id, item_id);

-- large. Actors, directors, authors, narrators, artists' members.
CREATE TABLE people (
  id           INTEGER NOT NULL PRIMARY KEY,
  public_id    TEXT    NOT NULL UNIQUE,
  name         TEXT    NOT NULL,
  sort_name    TEXT    NOT NULL,
  biography    TEXT,
  birth_date   TEXT,
  death_date   TEXT,
  birthplace   TEXT,
  refreshed_at INTEGER,
  updated_at   INTEGER NOT NULL,
  extra        TEXT
);
CREATE INDEX people_by_name ON people (sort_name, id);

-- large. One person per provider id, so matching never makes a duplicate person.
CREATE TABLE person_external_ids (
  provider    TEXT    NOT NULL,
  external_id TEXT    NOT NULL,
  person_id   INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  PRIMARY KEY (provider, external_id)
) WITHOUT ROWID;
CREATE INDEX person_external_ids_person ON person_external_ids (person_id);

-- large. Who did what on an item, in display order.
CREATE TABLE credits (
  item_id   INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  position  INTEGER NOT NULL,
  person_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
  role      INTEGER NOT NULL,
  character TEXT,
  PRIMARY KEY (item_id, position)
) WITHOUT ROWID;
CREATE INDEX credits_person ON credits (person_id, role, item_id);

-- large. Typed links between items: member of a collection, extra or trailer of a film,
-- guest artist on a track, book in a series.
CREATE TABLE item_relations (
  item_id         INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  kind            INTEGER NOT NULL,
  related_item_id INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  position        INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (item_id, kind, related_item_id)
) WITHOUT ROWID;
CREATE INDEX item_relations_reverse ON item_relations (related_item_id, kind, item_id);

-- large. Artwork choices. Each row belongs to exactly one owner. The picture itself is a file
-- on disk named by its content hash.
CREATE TABLE images (
  id         INTEGER NOT NULL PRIMARY KEY,
  item_id    INTEGER REFERENCES items(id) ON DELETE CASCADE,
  person_id  INTEGER REFERENCES people(id) ON DELETE CASCADE,
  profile_id INTEGER REFERENCES profiles(id) ON DELETE CASCADE,
  role       INTEGER NOT NULL,
  source     INTEGER NOT NULL,
  file_hash  TEXT,
  source_url TEXT,
  width      INTEGER,
  height     INTEGER,
  language   TEXT,
  placeholder TEXT,
  position   INTEGER NOT NULL DEFAULT 0,
  selected   INTEGER NOT NULL DEFAULT 0 CHECK (selected IN (0, 1)),
  locked     INTEGER NOT NULL DEFAULT 0 CHECK (locked IN (0, 1)),
  CHECK ((item_id IS NOT NULL) + (person_id IS NOT NULL) + (profile_id IS NOT NULL) = 1)
);
CREATE INDEX images_item ON images (item_id, role, selected) WHERE item_id IS NOT NULL;
CREATE INDEX images_person ON images (person_id, role, selected) WHERE person_id IS NOT NULL;
CREATE INDEX images_profile ON images (profile_id) WHERE profile_id IS NOT NULL;

-- ---------------------------------------------------------------------------------------
-- D. Users, sign-in and access
-- ---------------------------------------------------------------------------------------

-- two_factor_secret is set when enrolment starts and counts once two_factor_enabled_at is set.
-- two_factor_last_step is the last authenticator time step accepted, so one code works once.
-- viewing_hours is the times of the week the account may be used, as JSON; NULL is any time.
CREATE TABLE users (
  id                  INTEGER NOT NULL PRIMARY KEY,
  public_id           TEXT    NOT NULL UNIQUE,
  username            TEXT    NOT NULL UNIQUE,
  email               TEXT,
  password_hash       TEXT,
  password_changed_at INTEGER,
  role                TEXT    NOT NULL CHECK (role IN ('owner', 'admin', 'member')),
  all_libraries       INTEGER NOT NULL DEFAULT 1 CHECK (all_libraries IN (0, 1)),
  two_factor_secret   BLOB,
  two_factor_enabled_at INTEGER,
  two_factor_last_step INTEGER,
  disabled_at         INTEGER,
  viewing_hours       TEXT,
  notes               TEXT,
  extra               TEXT,
  revision            INTEGER NOT NULL DEFAULT 1,
  created_at          INTEGER NOT NULL,
  updated_at          INTEGER NOT NULL
) STRICT;

-- Viewing profiles. Each account has one main profile (is_primary), made with the account: it
-- cannot be deleted, and only it carries the account's owner or admin role. access_changed_at moves whenever what the profile may see changes, and
-- clients throw their cache away. history_cleared_at drops older offline progress.
CREATE TABLE profiles (
  id                 INTEGER NOT NULL PRIMARY KEY,
  public_id          TEXT    NOT NULL UNIQUE,
  user_id            INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name               TEXT    NOT NULL,
  color              TEXT    NOT NULL DEFAULT 'blue',
  is_primary         INTEGER NOT NULL DEFAULT 0 CHECK (is_primary IN (0, 1)),
  position           INTEGER NOT NULL DEFAULT 0,
  pin_hash           TEXT,
  max_rating_age     INTEGER,
  all_libraries      INTEGER NOT NULL DEFAULT 1 CHECK (all_libraries IN (0, 1)),
  allow_downloads    INTEGER NOT NULL DEFAULT 1 CHECK (allow_downloads IN (0, 1)),
  allow_live_tv      INTEGER NOT NULL DEFAULT 1 CHECK (allow_live_tv IN (0, 1)),
  allow_dvr          INTEGER NOT NULL DEFAULT 1 CHECK (allow_dvr IN (0, 1)),
  access_changed_at  INTEGER NOT NULL,
  history_cleared_at INTEGER,
  extra              TEXT,
  created_at         INTEGER NOT NULL,
  updated_at         INTEGER NOT NULL
) STRICT;
CREATE INDEX profiles_user ON profiles (user_id, position);
CREATE UNIQUE INDEX profiles_primary ON profiles (user_id) WHERE is_primary = 1;

-- One row per app install or browser. client_id is the id the app made for itself.
CREATE TABLE devices (
  id           INTEGER NOT NULL PRIMARY KEY,
  public_id    TEXT    NOT NULL UNIQUE,
  client_id    TEXT    NOT NULL UNIQUE,
  name         TEXT    NOT NULL,
  platform     TEXT,
  app          TEXT,
  app_version  TEXT,
  created_at   INTEGER NOT NULL,
  last_seen_at INTEGER NOT NULL
) STRICT;

-- What a device's players can do, as the app last published it: one JSON document that can
-- describe more than one player on the device. Not a column on devices, because that row is
-- rewritten whenever the device is seen and this document is several kilobytes.
CREATE TABLE device_capabilities (
  device_id  INTEGER NOT NULL PRIMARY KEY REFERENCES devices(id) ON DELETE CASCADE,
  document   TEXT    NOT NULL,
  updated_at INTEGER NOT NULL
) STRICT;

-- Which libraries a member, a profile or a not-yet-accepted invitation may see.
-- Only read when the owner's all_libraries flag is 0.
CREATE TABLE library_access (
  id            INTEGER NOT NULL PRIMARY KEY,
  library_id    INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  user_id       INTEGER REFERENCES users(id) ON DELETE CASCADE,
  profile_id    INTEGER REFERENCES profiles(id) ON DELETE CASCADE,
  invitation_id INTEGER REFERENCES invitations(id) ON DELETE CASCADE,
  CHECK ((user_id IS NOT NULL) + (profile_id IS NOT NULL) + (invitation_id IS NOT NULL) = 1)
) STRICT;
CREATE UNIQUE INDEX library_access_user ON library_access (user_id, library_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX library_access_profile ON library_access (profile_id, library_id) WHERE profile_id IS NOT NULL;
CREATE UNIQUE INDEX library_access_invitation ON library_access (invitation_id, library_id) WHERE invitation_id IS NOT NULL;
CREATE INDEX library_access_library ON library_access (library_id);

-- Labels a profile must never be shown. (Blocked channels join this table with Live TV.)
CREATE TABLE profile_restrictions (
  profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  tag_id     INTEGER NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
  PRIMARY KEY (profile_id, tag_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX profile_restrictions_tag ON profile_restrictions (tag_id);

-- One row per sign-in. The refresh token rotates on each renewal. The previous token's hash and
-- the request id it was renewed with are kept until the next renewal, so a renewal whose reply
-- was lost can be repeated; any other use of an old token revokes the session. profile_id is
-- NULL until a profile is selected. scope is 'app'; 'member' for a sign-in that never has
-- more than a member's rights (a TV approved from a profile other than the main one); or
-- 'enrol' for a sign-in that can only set up two-step sign-in, which the owner requires.
-- manage_until_at is when a main profile entered with a remembered PIN stops being able to
-- manage the account until the PIN is typed again; NULL is no such limit.
CREATE TABLE sessions (
  id                    INTEGER NOT NULL PRIMARY KEY,
  public_id             TEXT    NOT NULL UNIQUE,
  user_id               INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  profile_id            INTEGER REFERENCES profiles(id) ON DELETE CASCADE,
  device_id             INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  scope                 TEXT    NOT NULL DEFAULT 'app',
  refresh_hash          TEXT    NOT NULL UNIQUE,
  previous_refresh_hash TEXT,
  previous_request_id   TEXT,
  previous_at           INTEGER,
  token_generation      INTEGER NOT NULL DEFAULT 1,
  created_at            INTEGER NOT NULL,
  expires_at            INTEGER NOT NULL,
  last_used_at          INTEGER NOT NULL,
  revoked_at            INTEGER,
  revoked_reason        TEXT,
  remote_addr           TEXT,
  manage_until_at       INTEGER
) STRICT;
CREATE INDEX sessions_user ON sessions (user_id);
CREATE INDEX sessions_profile ON sessions (profile_id) WHERE profile_id IS NOT NULL;
CREATE INDEX sessions_device ON sessions (device_id);

-- Every short-lived single-use secret: password reset, TV sign-in code, two-step challenge,
-- recovery code. Redeeming is one UPDATE ... WHERE used_at IS NULL, so exactly one caller wins.
CREATE TABLE auth_codes (
  code_hash  TEXT    NOT NULL PRIMARY KEY,
  kind       TEXT    NOT NULL,
  user_id    INTEGER REFERENCES users(id) ON DELETE CASCADE,
  payload    TEXT,
  attempts   INTEGER NOT NULL DEFAULT 0,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  used_at    INTEGER
) STRICT, WITHOUT ROWID;
CREATE INDEX auth_codes_user ON auth_codes (user_id, kind) WHERE user_id IS NOT NULL;
CREATE INDEX auth_codes_expiry ON auth_codes (expires_at);

-- Invitations an admin can list, edit and revoke. Library grants are library_access rows.
CREATE TABLE invitations (
  id                 INTEGER NOT NULL PRIMARY KEY,
  public_id          TEXT    NOT NULL UNIQUE,
  code_hash          TEXT    NOT NULL UNIQUE,
  created_by_user_id INTEGER REFERENCES users(id) ON DELETE SET NULL,
  accepted_user_id   INTEGER REFERENCES users(id) ON DELETE SET NULL,
  role               TEXT    NOT NULL DEFAULT 'member' CHECK (role IN ('admin', 'member')),
  state              TEXT    NOT NULL DEFAULT 'open' CHECK (state IN ('open', 'accepted', 'revoked')),
  all_libraries      INTEGER NOT NULL DEFAULT 1 CHECK (all_libraries IN (0, 1)),
  note               TEXT,
  created_at         INTEGER NOT NULL,
  expires_at         INTEGER NOT NULL,
  accepted_at        INTEGER
) STRICT;
CREATE INDEX invitations_creator ON invitations (created_by_user_id) WHERE created_by_user_id IS NOT NULL;
CREATE INDEX invitations_accepted ON invitations (accepted_user_id) WHERE accepted_user_id IS NOT NULL;

-- Failed-attempt counts that survive a restart. A limit slows a source down; it never locks
-- an account. Unknown and known usernames are counted the same way.
CREATE TABLE auth_limits (
  bucket          TEXT    NOT NULL,
  key_hash        TEXT    NOT NULL,
  failures        INTEGER NOT NULL DEFAULT 0,
  last_failure_at INTEGER NOT NULL,
  blocked_until_at INTEGER,
  PRIMARY KEY (bucket, key_hash)
) STRICT, WITHOUT ROWID;

-- Long-lived credentials for tools: API keys, personal tokens and app passwords.
CREATE TABLE api_keys (
  id           INTEGER NOT NULL PRIMARY KEY,
  public_id    TEXT    NOT NULL UNIQUE,
  user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  kind         TEXT    NOT NULL CHECK (kind IN ('api_key', 'personal_token', 'app_password')),
  name         TEXT    NOT NULL,
  key_hash     TEXT    NOT NULL UNIQUE,
  scopes       TEXT    NOT NULL,
  created_at   INTEGER NOT NULL,
  last_used_at INTEGER,
  expires_at   INTEGER,
  revoked_at   INTEGER
) STRICT;
CREATE INDEX api_keys_user ON api_keys (user_id);

-- A profile's own settings, as key and JSON value. A row with no device_class and no device
-- applies everywhere; one with a device_class to that kind of device (web, mobile, tablet,
-- television); one with a device to that device alone. The narrowest row wins.
CREATE TABLE preferences (
  id           INTEGER NOT NULL PRIMARY KEY,
  profile_id   INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  device_class TEXT    NOT NULL DEFAULT '',
  device_id    INTEGER REFERENCES devices(id) ON DELETE CASCADE,
  key          TEXT    NOT NULL,
  value        TEXT    NOT NULL,
  updated_at   INTEGER NOT NULL,
  CHECK (device_class = '' OR device_id IS NULL)
) STRICT;
CREATE UNIQUE INDEX preferences_key ON preferences (profile_id, device_class, ifnull(device_id, 0), key);
CREATE INDEX preferences_device ON preferences (device_id) WHERE device_id IS NOT NULL;

-- ---------------------------------------------------------------------------------------
-- F. Per-user state
-- ---------------------------------------------------------------------------------------

-- large. One row per profile per item. Each deliberate choice carries the time it was made,
-- so when devices sync the later edit wins field by field. A progress report never undoes a
-- later deliberate watched or unwatched.
CREATE TABLE user_item_state (
  profile_id       INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  item_id          INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  position_ms      INTEGER,
  position_text    TEXT,
  position_at      INTEGER,
  played           INTEGER NOT NULL DEFAULT 0 CHECK (played IN (0, 1)),
  played_at        INTEGER,
  play_count       INTEGER NOT NULL DEFAULT 0,
  last_played_at   INTEGER,
  favorite         INTEGER NOT NULL DEFAULT 0 CHECK (favorite IN (0, 1)),
  favorite_at      INTEGER,
  in_list          INTEGER NOT NULL DEFAULT 0 CHECK (in_list IN (0, 1)),
  in_list_at       INTEGER,
  hidden           INTEGER NOT NULL DEFAULT 0 CHECK (hidden IN (0, 1)),
  hidden_at        INTEGER,
  rating           INTEGER,
  rating_at        INTEGER,
  audio_language   TEXT,
  subtitle_language TEXT,
  subtitle_offset_ms INTEGER,
  updated_at       INTEGER NOT NULL,
  PRIMARY KEY (profile_id, item_id)
) WITHOUT ROWID;
CREATE INDEX user_item_state_item ON user_item_state (item_id);
CREATE INDEX user_item_state_recent ON user_item_state (profile_id, last_played_at) WHERE last_played_at IS NOT NULL;
CREATE INDEX user_item_state_favorites ON user_item_state (profile_id, favorite_at) WHERE favorite = 1;
CREATE INDEX user_item_state_my_list ON user_item_state (profile_id, in_list_at) WHERE in_list = 1;

-- large. One row per finished play. The title is copied so history survives deletion.
-- client_event_id makes a repeated report count once. hidden_from_profile is 1 for a play
-- made while the profile had history paused: the owner's play record has it, and nothing
-- the profile itself is shown does.
CREATE TABLE play_history (
  id              INTEGER NOT NULL PRIMARY KEY,
  profile_id      INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  item_id         INTEGER REFERENCES items(id) ON DELETE SET NULL,
  device_id       INTEGER REFERENCES devices(id) ON DELETE SET NULL,
  client_event_id TEXT    NOT NULL,
  played_at       INTEGER NOT NULL,
  watched_ms      INTEGER,
  item_kind       INTEGER NOT NULL,
  title           TEXT    NOT NULL,
  subtitle        TEXT,
  hidden_from_profile INTEGER NOT NULL DEFAULT 0 CHECK (hidden_from_profile IN (0, 1)),
  UNIQUE (profile_id, client_event_id)
);
CREATE INDEX play_history_profile ON play_history (profile_id, played_at);
CREATE INDEX play_history_item ON play_history (item_id) WHERE item_id IS NOT NULL;
CREATE INDEX play_history_device ON play_history (device_id) WHERE device_id IS NOT NULL;

-- Playlists, collections people make, and saved views. rules holds a saved filter or the
-- rule of a smart list.
CREATE TABLE lists (
  id               INTEGER NOT NULL PRIMARY KEY,
  public_id        TEXT    NOT NULL UNIQUE,
  kind             TEXT    NOT NULL CHECK (kind IN ('playlist', 'collection', 'saved_view')),
  owner_profile_id INTEGER REFERENCES profiles(id) ON DELETE CASCADE,
  library_id       INTEGER REFERENCES libraries(id) ON DELETE CASCADE,
  name             TEXT    NOT NULL,
  sort_name        TEXT    NOT NULL,
  description      TEXT,
  rules            TEXT,
  pinned           INTEGER NOT NULL DEFAULT 0 CHECK (pinned IN (0, 1)),
  position         INTEGER NOT NULL DEFAULT 0,
  entry_count      INTEGER NOT NULL DEFAULT 0,
  revision         INTEGER NOT NULL DEFAULT 1,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
) STRICT;
CREATE INDEX lists_owner ON lists (owner_profile_id, kind) WHERE owner_profile_id IS NOT NULL;
CREATE INDEX lists_library ON lists (library_id, kind) WHERE library_id IS NOT NULL;

-- large. Ordered entries. Positions leave gaps so a move rewrites one row.
CREATE TABLE list_entries (
  id       INTEGER NOT NULL PRIMARY KEY,
  list_id  INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
  item_id  INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  position INTEGER NOT NULL,
  added_at INTEGER NOT NULL
);
CREATE INDEX list_entries_order ON list_entries (list_id, position);
CREATE INDEX list_entries_item ON list_entries (item_id);

-- Who a list is shared with, and whether they may edit it.
CREATE TABLE list_shares (
  list_id    INTEGER NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
  profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  can_edit   INTEGER NOT NULL DEFAULT 0 CHECK (can_edit IN (0, 1)),
  created_at INTEGER NOT NULL,
  PRIMARY KEY (list_id, profile_id)
) STRICT, WITHOUT ROWID;
CREATE INDEX list_shares_profile ON list_shares (profile_id);

-- A profile's play queue on one device: one for music and audiobooks and one for video, so
-- music comes back with its queue and place after a video. A queue outlives a restart; one
-- nobody has played for 30 days is removed. selection says what a queue too long to hold at
-- once was built from and how far into it the queue has got, so it is refilled as the
-- listener nears the end. shuffle_seed is NULL while shuffle is off.
CREATE TABLE play_queues (
  id               INTEGER NOT NULL PRIMARY KEY,
  public_id        TEXT    NOT NULL UNIQUE,
  profile_id       INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  device_id        INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  medium           TEXT    NOT NULL CHECK (medium IN ('audio', 'video')),
  current_entry_id INTEGER REFERENCES queue_entries(id) ON DELETE SET NULL,
  repeat           TEXT    NOT NULL DEFAULT 'off' CHECK (repeat IN ('off', 'one', 'all')),
  shuffle_seed     INTEGER,
  selection        TEXT,
  revision         INTEGER NOT NULL DEFAULT 1,
  created_at       INTEGER NOT NULL,
  played_at        INTEGER NOT NULL
) STRICT;
CREATE INDEX play_queues_profile ON play_queues (profile_id);
CREATE INDEX play_queues_device ON play_queues (device_id);
CREATE INDEX play_queues_current ON play_queues (current_entry_id) WHERE current_entry_id IS NOT NULL;
CREATE INDEX play_queues_idle ON play_queues (played_at);

-- large. A queue's entries. position is the order they play in, with gaps so a move rewrites
-- one row; source_position is the order they were added in, which turning shuffle off goes
-- back to. added is 1 for what a person added by hand ("Added by you").
CREATE TABLE queue_entries (
  id              INTEGER NOT NULL PRIMARY KEY,
  queue_id        INTEGER NOT NULL REFERENCES play_queues(id) ON DELETE CASCADE,
  item_id         INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  position        INTEGER NOT NULL,
  source_position INTEGER NOT NULL,
  added           INTEGER NOT NULL DEFAULT 0 CHECK (added IN (0, 1))
);
CREATE INDEX queue_entries_order ON queue_entries (queue_id, position);
CREATE INDEX queue_entries_item ON queue_entries (item_id);

-- ---------------------------------------------------------------------------------------
-- G. Playback
-- ---------------------------------------------------------------------------------------

-- "This way of delivering this file failed on this device's player." Planning starts with the
-- next way until expires_at, then tries this one again. It belongs to the device and the file,
-- so it holds for every profile on the device and outlives a sign-out and a restart.
CREATE TABLE playback_route_failures (
  id         INTEGER NOT NULL PRIMARY KEY,
  public_id  TEXT    NOT NULL UNIQUE,
  device_id  INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  file_id    INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
  player     TEXT    NOT NULL,
  route      TEXT    NOT NULL,
  code       TEXT    NOT NULL,
  detail     TEXT,
  failed_at  INTEGER NOT NULL,
  expires_at INTEGER NOT NULL,
  UNIQUE (device_id, file_id, player, route)
) STRICT;
CREATE INDEX playback_route_failures_file ON playback_route_failures (file_id);
CREATE INDEX playback_route_failures_expiry ON playback_route_failures (expires_at);

-- large. What each profile had on a title that was removed (its trash was emptied, it was
-- deleted, its folder or its library left), so watched, progress, rating and favourite return
-- when the title is added again, to any library. identity is the title's provider id with its
-- kind when it was matched, else its kind, folded title and year; an episode or a track is its
-- show's or album's identity and its numbers. The other columns are user_item_state's. Rows
-- older than a year are pruned.
CREATE TABLE removed_state (
  profile_id       INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  identity         TEXT    NOT NULL,
  removed_at       INTEGER NOT NULL,
  position_ms      INTEGER,
  position_text    TEXT,
  position_at      INTEGER,
  played           INTEGER NOT NULL DEFAULT 0,
  played_at        INTEGER,
  play_count       INTEGER NOT NULL DEFAULT 0,
  last_played_at   INTEGER,
  favorite         INTEGER NOT NULL DEFAULT 0,
  favorite_at      INTEGER,
  in_list          INTEGER NOT NULL DEFAULT 0,
  in_list_at       INTEGER,
  hidden           INTEGER NOT NULL DEFAULT 0,
  hidden_at        INTEGER,
  rating           INTEGER,
  rating_at        INTEGER,
  audio_language   TEXT,
  subtitle_language TEXT,
  subtitle_offset_ms INTEGER,
  updated_at       INTEGER NOT NULL,
  PRIMARY KEY (profile_id, identity)
) WITHOUT ROWID;
CREATE INDEX removed_state_identity ON removed_state (identity);
CREATE INDEX removed_state_age ON removed_state (removed_at);
