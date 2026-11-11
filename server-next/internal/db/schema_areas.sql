-- Portico main database, second part: the tables of the areas built on the core in
-- schema.sql. Same conventions; the two files are loaded together and are one schema. It is a
-- second file only because a source file stops at 1,000 lines.
--
-- Running state has no table here: watch-together rooms, devices open to play, moves between
-- devices, live sessions, tuners in use, the rewind stretch and a recording's progress are in
-- memory. Setup, remote access and the Portico Account link need no table either: their
-- state is settings rows, columns on users, auth_codes kinds and key files.

-- ---------------------------------------------------------------------------------------
-- H. Outside tools
-- ---------------------------------------------------------------------------------------

-- Outgoing webhooks: at most 20. events is a JSON list of the event names it wants. secret
-- signs every message; it is sealed with the server key, because the server must be able to
-- read it back, and is shown to the owner once. last_error is set while the webhook is in
-- trouble and cleared by the next delivery that arrives. dropped counts messages left out
-- because 1,000 were already waiting. Read whole on the Integrations page; never grows.
CREATE TABLE webhooks (
  id               INTEGER NOT NULL PRIMARY KEY,
  public_id        TEXT    NOT NULL UNIQUE,
  name             TEXT    NOT NULL,
  url              TEXT    NOT NULL,
  events           TEXT    NOT NULL,
  secret           BLOB    NOT NULL,
  enabled          INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  dropped          INTEGER NOT NULL DEFAULT 0,
  last_delivered_at INTEGER,
  last_error       TEXT,
  revision         INTEGER NOT NULL DEFAULT 1,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL
) STRICT;

-- Webhook messages: the ones still waiting, which survive a restart, and each webhook's
-- delivery log, which is the same rows once they are finished. Messages to one webhook go out
-- oldest first. A webhook keeps at most 1,000 waiting and its newest 200 finished, so the
-- table never passes about 24,000 rows; the sender reads outbox_due and one webhook's rows.
CREATE TABLE outbox (
  id              INTEGER NOT NULL PRIMARY KEY,
  webhook_id      INTEGER NOT NULL REFERENCES webhooks(id) ON DELETE CASCADE,
  event           TEXT    NOT NULL,
  payload         TEXT    NOT NULL,
  state           TEXT    NOT NULL DEFAULT 'waiting' CHECK (state IN ('waiting', 'delivered', 'gave_up')),
  attempts        INTEGER NOT NULL DEFAULT 0,
  next_attempt_at INTEGER,
  last_status     INTEGER,
  last_error      TEXT,
  created_at      INTEGER NOT NULL,
  finished_at     INTEGER
) STRICT;
CREATE INDEX outbox_webhook ON outbox (webhook_id, id);
CREATE INDEX outbox_due ON outbox (next_attempt_at) WHERE state = 'waiting';

-- ---------------------------------------------------------------------------------------
-- I. Owner console
-- ---------------------------------------------------------------------------------------

-- The notice inbox: the one way the server tells a person something. One row per account
-- that receives the notice; profile_id is set when it is for one profile alone (a reply to
-- its report, a recording of its own that failed) and NULL when every profile of the account
-- sees it. kind says what happened and params carries its names and numbers as JSON, so the
-- app words it; body is text a person or a tool wrote (the owner's message to everyone, a
-- tool's notice). ref is the public id of what to open. Read and archived are on the row.
-- Rows older than the owner's keeping time (180 days) are removed. A message to everyone is
-- one row per account, so the table stays in the tens of thousands; the inbox and its unread
-- count are read through the two indexes and do not grow with it.
CREATE TABLE notifications (
  id          INTEGER NOT NULL PRIMARY KEY,
  public_id   TEXT    NOT NULL UNIQUE,
  user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  profile_id  INTEGER REFERENCES profiles(id) ON DELETE CASCADE,
  kind        TEXT    NOT NULL,
  params      TEXT,
  body        TEXT,
  ref         TEXT,
  created_at  INTEGER NOT NULL,
  read_at     INTEGER,
  archived_at INTEGER
) STRICT;
CREATE INDEX notifications_inbox ON notifications (user_id, created_at);
CREATE INDEX notifications_unread ON notifications (user_id) WHERE read_at IS NULL AND archived_at IS NULL;
CREATE INDEX notifications_profile ON notifications (profile_id) WHERE profile_id IS NOT NULL;
CREATE INDEX notifications_age ON notifications (created_at);

-- "Report a problem" and the reports an app offers to send after a crash. The reporter's
-- name is copied so a report outlives the account. context is what they were playing and on
-- what, as JSON. log_hash names the app log they agreed to attach, a file in the data folder.
-- reply is the owner's answer, which also arrives as a notification. Crash reports are
-- removed after 30 days. Hundreds of rows; the owner's list is read newest first.
CREATE TABLE reports (
  id            INTEGER NOT NULL PRIMARY KEY,
  public_id     TEXT    NOT NULL UNIQUE,
  user_id       INTEGER REFERENCES users(id) ON DELETE SET NULL,
  profile_id    INTEGER REFERENCES profiles(id) ON DELETE SET NULL,
  reporter_name TEXT    NOT NULL,
  kind          TEXT    NOT NULL CHECK (kind IN ('problem', 'crash')),
  status        TEXT    NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'in_progress', 'resolved', 'closed')),
  message       TEXT,
  context       TEXT,
  log_hash      TEXT,
  reply         TEXT,
  replied_at    INTEGER,
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
) STRICT;
CREATE INDEX reports_user ON reports (user_id, created_at) WHERE user_id IS NOT NULL;
CREATE INDEX reports_profile ON reports (profile_id) WHERE profile_id IS NOT NULL;
CREATE INDEX reports_status ON reports (status, created_at);

-- The server's lifetime statistics: a few totals for each day of its life, never removed.
-- day is the server's local date as YYYYMMDD. metric is an open name: plays started, time
-- played and data sent at home and away, each split by kind of media and by how it was
-- delivered (plays.film.converted, played_ms.music.original, bytes_away.episode.repackaged),
-- and people (how many different people played) and streams_peak. They are totals for the
-- server, never for a person or a title, so there are a few dozen rows a day whatever the
-- size of the library or the household; a chart reads a range of days by the key. Today's
-- rows are added to every few minutes and when the server stops, so a restart loses at most
-- those minutes. (Who played what is play_history; the last day's load chart is in memory.)
CREATE TABLE daily_totals (
  day    INTEGER NOT NULL,
  metric TEXT    NOT NULL,
  value  INTEGER NOT NULL,
  PRIMARY KEY (day, metric)
) STRICT, WITHOUT ROWID;

-- ---------------------------------------------------------------------------------------
-- J. Reading
-- ---------------------------------------------------------------------------------------

-- large. A profile's bookmarks, highlights and notes in a book. They belong to the book, not
-- to one file of it. public_id is made by the app, so a repeat or an edit made out of reach
-- lands on the same row, and the later updated_at wins. kind is bookmark or highlight, as a
-- code; a note hangs from either. location is the exact spot in the file it was made on (an
-- EPUB position or a page), made_on is that file's fingerprint, and progress is how far
-- through the book it is (0 to 1), which places it when the file is replaced; text is the
-- words that were marked, so it can always be read in the list. deleted_at marks one that was
-- deleted, kept 70 days (twice the oldest edit applied) so an edit from a device that was out of
-- reach cannot bring it back. At most 5,000 per profile per book; a book's list is one range
-- of annotations_book.
-- (A profile's place in a book is user_item_state.position_text.)
CREATE TABLE annotations (
  id         INTEGER NOT NULL PRIMARY KEY,
  profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  item_id    INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  public_id  TEXT    NOT NULL,
  kind       INTEGER NOT NULL,
  color      INTEGER,
  location   TEXT    NOT NULL,
  made_on    TEXT,
  progress   REAL    NOT NULL,
  chapter    TEXT,
  text       TEXT,
  note       TEXT,
  extra      TEXT,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  deleted_at INTEGER,
  UNIQUE (profile_id, public_id)
);
CREATE INDEX annotations_book ON annotations (profile_id, item_id, progress);
CREATE INDEX annotations_item ON annotations (item_id);
CREATE INDEX annotations_deleted ON annotations (deleted_at) WHERE deleted_at IS NOT NULL;

-- ---------------------------------------------------------------------------------------
-- K. Downloads
-- ---------------------------------------------------------------------------------------

-- A smaller copy of one version that the server made, or is making: the one way to get a
-- prepared copy, for downloads in the apps, for Save file in a browser, and for anything later
-- that needs one. recipe is the quality it was made at, as one canonical word, so a second
-- person asking for the same thing waits for nothing. The copy is a file in the prepared
-- folder named by public_id, or one file per part for a version in several files. state is
-- the queue: package prepare makes the oldest waiting copy a request still wants, as many at
-- once as the owner allows, and how far it has got is in memory. content_hash is the SHA-256
-- of the file (of each part's, separated by spaces), so a device can check it arrived. A copy
-- nobody has asked for or fetched since last_used_at plus the owner's keeping time is removed,
-- row and file. The owner's download space is the sum of size here: each copy counts once.
-- Thousands of rows at most; asking for a copy is one read of the unique index.
CREATE TABLE prepared_media (
  id           INTEGER NOT NULL PRIMARY KEY,
  public_id    TEXT    NOT NULL UNIQUE,
  media_id     INTEGER NOT NULL REFERENCES media(id) ON DELETE CASCADE,
  recipe       TEXT    NOT NULL,
  state        TEXT    NOT NULL DEFAULT 'waiting' CHECK (state IN ('waiting', 'preparing', 'ready', 'failed')),
  error        TEXT,
  container    TEXT,
  size         INTEGER,
  content_hash TEXT,
  created_at   INTEGER NOT NULL,
  ready_at     INTEGER,
  last_used_at INTEGER NOT NULL,
  UNIQUE (media_id, recipe)
) STRICT;
CREATE INDEX prepared_media_idle ON prepared_media (last_used_at);

-- What a profile asked to have on one device (a browser is a device too): one title, or a
-- show, season, album, artist or playlist. quality is 'original' or a recipe. selection is
-- which of a holder's titles: all, unwatched only, or the next unwatched; keep_next (1, 3 or
-- 5) makes that a lasting rule, kept up to date each time the device syncs. media_id is the
-- version the person chose, when they chose one. Asking twice for the same thing is one row.
-- What a device actually holds is known to the device alone. A few rows per device; a device
-- reads its own through downloads_target.
CREATE TABLE downloads (
  id         INTEGER NOT NULL PRIMARY KEY,
  public_id  TEXT    NOT NULL UNIQUE,
  profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  device_id  INTEGER NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
  item_id    INTEGER REFERENCES items(id) ON DELETE CASCADE,
  list_id    INTEGER REFERENCES lists(id) ON DELETE CASCADE,
  media_id   INTEGER REFERENCES media(id) ON DELETE SET NULL,
  quality    TEXT    NOT NULL,
  selection  TEXT    NOT NULL DEFAULT 'all' CHECK (selection IN ('all', 'unwatched', 'next')),
  keep_next  INTEGER,
  paused     INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1)),
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  CHECK ((item_id IS NOT NULL) + (list_id IS NOT NULL) = 1)
) STRICT;
CREATE UNIQUE INDEX downloads_target ON downloads (profile_id, device_id, ifnull(item_id, 0), ifnull(list_id, 0), quality);
CREATE INDEX downloads_device ON downloads (device_id);
CREATE INDEX downloads_item ON downloads (item_id) WHERE item_id IS NOT NULL;
CREATE INDEX downloads_list ON downloads (list_id) WHERE list_id IS NOT NULL;
CREATE INDEX downloads_media ON downloads (media_id) WHERE media_id IS NOT NULL;

-- large. Each title in a request: "3 of 9 ready, 1 couldn't be prepared". media_id is the
-- version to fetch. prepared_id is the smaller copy it waits for or uses, and that copy's
-- state is read from prepared_media, never copied here; with quality 'original' there is
-- none and the title can be fetched at once. state is this row's own, as a code: wanted,
-- paused by the person, or could not be included, with the reason. A prepared copy that
-- expired leaves prepared_id NULL, which reads as Expired. One request's titles are one range
-- of the primary key, however many requests there are.
CREATE TABLE download_items (
  download_id INTEGER NOT NULL REFERENCES downloads(id) ON DELETE CASCADE,
  item_id     INTEGER NOT NULL REFERENCES items(id) ON DELETE CASCADE,
  media_id    INTEGER REFERENCES media(id) ON DELETE SET NULL,
  prepared_id INTEGER REFERENCES prepared_media(id) ON DELETE SET NULL,
  state       INTEGER NOT NULL DEFAULT 0,
  reason      TEXT,
  position    INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (download_id, item_id)
) WITHOUT ROWID;
CREATE INDEX download_items_item ON download_items (item_id);
CREATE INDEX download_items_media ON download_items (media_id) WHERE media_id IS NOT NULL;
CREATE INDEX download_items_prepared ON download_items (prepared_id) WHERE prepared_id IS NOT NULL;

-- ---------------------------------------------------------------------------------------
-- L. Live TV, recordings and Library Channels
-- ---------------------------------------------------------------------------------------
-- The guide itself (programmes) is in guide.db, which can be deleted and is filled again from
-- the sources and from channel_schedule. Who is watching, tuners in use, the rewind stretch
-- and a recording's progress are in memory.

-- Where channels and listings come from: a playlist (by address or uploaded), an Xtream
-- sign-in, an HDHomeRun tuner, or a guide alone that supplies listings for another source's
-- channels. config is JSON: the addresses, the refresh interval, which groups or channels come
-- in and how they are numbered. secret is the sign-in, sealed with the server key and never
-- shown again. audience is who may watch its channels. tuner_count is how many channels it
-- delivers at once. state is the plain word shown for the source, and report is what the
-- last refresh brought in and left out, with examples, as JSON. A handful of rows.
CREATE TABLE live_sources (
  id           INTEGER NOT NULL PRIMARY KEY,
  public_id    TEXT    NOT NULL UNIQUE,
  kind         TEXT    NOT NULL CHECK (kind IN ('playlist', 'xtream', 'hdhomerun', 'guide')),
  name         TEXT    NOT NULL,
  config       TEXT    NOT NULL,
  secret       BLOB,
  audience     TEXT    NOT NULL DEFAULT 'everyone' CHECK (audience IN ('everyone', 'owner')),
  tuner_count  INTEGER NOT NULL DEFAULT 1,
  paused       INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1)),
  state        TEXT    NOT NULL DEFAULT 'new',
  last_error   TEXT,
  report       TEXT,
  refreshed_at INTEGER,
  revision     INTEGER NOT NULL DEFAULT 1,
  created_at   INTEGER NOT NULL,
  updated_at   INTEGER NOT NULL
) STRICT;

-- large. Every channel: a source's (kind 1), or a Library Channel (kind 2, no source, with
-- its builder in library_channels). One table, so the guide, On now, numbering, logos,
-- favourites, hiding and reminders treat both alike. source_key is the source's own name for
-- the channel, so a refresh updates the row and a profile's favourite survives. number is
-- the number shown, as major * 1000 + minor (5.1 is 5001); source_number is what the source
-- sent; a Library Channel's number is its place in the owner's order. state is a code: in
-- use, hidden by the owner for everyone, not brought in, or turned off. edited
-- is a JSON list of what the owner set by hand (number, name, listings), which a refresh
-- leaves alone. guide_source_id and guide_key say which listings are this channel's.
-- locator is where the stream is; it never reaches an app. The logo is a file in the data
-- folder named by logo_hash. Up to 50,000 a source. A page of the guide is a run of one of
-- the four ordered indexes (all channels by number or name, one source's, one group's) and
-- costs the same however many channels there are.
CREATE TABLE channels (
  id              INTEGER NOT NULL PRIMARY KEY,
  public_id       TEXT    NOT NULL UNIQUE,
  kind            INTEGER NOT NULL,
  source_id       INTEGER REFERENCES live_sources(id) ON DELETE CASCADE,
  source_key      TEXT,
  name            TEXT    NOT NULL,
  sort_name       TEXT    NOT NULL,
  number          INTEGER NOT NULL,
  source_number   INTEGER,
  group_name      TEXT,
  state           INTEGER NOT NULL DEFAULT 1,
  edited          TEXT,
  guide_source_id INTEGER REFERENCES live_sources(id) ON DELETE SET NULL,
  guide_key       TEXT,
  locator         TEXT,
  logo_url        TEXT,
  logo_hash       TEXT,
  missing_since_at INTEGER,
  updated_at      INTEGER NOT NULL
);
CREATE UNIQUE INDEX channels_source ON channels (source_id, source_key);
CREATE INDEX channels_by_number ON channels (state, number, id);
CREATE INDEX channels_by_name ON channels (state, sort_name, id);
CREATE INDEX channels_of_source ON channels (source_id, state, number, id) WHERE source_id IS NOT NULL;
CREATE INDEX channels_of_group ON channels (group_name, state, number, id) WHERE group_name IS NOT NULL;
CREATE INDEX channels_guide ON channels (guide_source_id, guide_key) WHERE guide_source_id IS NOT NULL;

-- large. A profile's own favourite channels and the channels it hid for itself. A row exists
-- only for a channel the profile marked.
CREATE TABLE profile_channels (
  profile_id INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  favorite   INTEGER NOT NULL DEFAULT 0 CHECK (favorite IN (0, 1)),
  hidden     INTEGER NOT NULL DEFAULT 0 CHECK (hidden IN (0, 1)),
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (profile_id, channel_id)
) WITHOUT ROWID;
CREATE INDEX profile_channels_channel ON profile_channels (channel_id);

-- A Library Channel as the owner built it; its name, logo, on or off and place in the list
-- are its channels row. At most 1,000. builder is one JSON document, saved whole and read
-- whole with an ETag, at most 2 MB, described in AREAS.md: the libraries it draws from, films
-- or shows or both, the conditions and groups (in the item list's own filter form), the words
-- to find, "added in the last", the titles always and never included (500 each), the play
-- order and how a show's episodes run, "avoid repeats within", "play more or less often", up
-- to 500 weekly blocks each with its own copied selection, the quality choice and the
-- on-screen logo. Titles and libraries are named in it by public id: a channel is never
-- looked up by what is in it, and a title that has gone is skipped. libraries is a copy, as a
-- JSON list of public ids, of every library the channel and its blocks draw from, written
-- with each save: it is all that deciding who may see the channel needs, so the builder is
-- read only to edit or to schedule and is never held in memory. schedule_state is where the
-- schedule in channel_schedule had got to at scheduled_through_at, saved in the same
-- transaction as the rows it describes: for the channel's own selection and for each block, a
-- shuffle as a seed and a cursor (never a list of titles), the last titles played for "avoid
-- repeats" (at most 1,000), and the place in each show that is part-way through. state is
-- the word on the channel's row (on air, building, needs a new schedule, needs attention) and
-- problem the one sentence why.
CREATE TABLE library_channels (
  channel_id           INTEGER NOT NULL PRIMARY KEY REFERENCES channels(id) ON DELETE CASCADE,
  description          TEXT,
  time_zone            TEXT    NOT NULL,
  builder              TEXT    NOT NULL,
  libraries            TEXT    NOT NULL,
  schedule_state       TEXT,
  scheduled_through_at INTEGER,
  state                TEXT    NOT NULL DEFAULT 'building',
  problem              TEXT,
  revision             INTEGER NOT NULL DEFAULT 1,
  created_at           INTEGER NOT NULL,
  updated_at           INTEGER NOT NULL
) STRICT;

-- large. What each Library Channel plays and when, from yesterday to a week ahead: the
-- schedule itself. It is here, in the main database, so that what has aired and what is on
-- never change, it is saved together with schedule_state, and it travels in a backup; the
-- guide reads a copy of it in guide.db, which is filled again from here whenever it is
-- missing. item_id NULL is the slate: a title that has gone, or a stretch nothing fits.
-- Under a million small rows at 1,000 channels; each day about a seventh is added and removed,
-- one channel to a transaction. "What is on this channel now" is one read of the key.
CREATE TABLE channel_schedule (
  channel_id INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  starts_at  INTEGER NOT NULL,
  ends_at    INTEGER NOT NULL,
  item_id    INTEGER REFERENCES items(id) ON DELETE SET NULL,
  PRIMARY KEY (channel_id, starts_at)
) WITHOUT ROWID;
CREATE INDEX channel_schedule_item ON channel_schedule (item_id) WHERE item_id IS NOT NULL;

-- A block the owner saved to use again on any Library Channel: at most 1,000. recipe is one
-- JSON document: the selection (clauses that include or exclude), the order, the length, an
-- optional start and the days. A preset draws from one library and goes with it. Adding a
-- block from a preset copies the recipe into the channel, so changing or deleting a preset
-- never changes a channel. Each is made, changed and deleted by itself, with its own ETag.
CREATE TABLE block_presets (
  id          INTEGER NOT NULL PRIMARY KEY,
  public_id   TEXT    NOT NULL UNIQUE,
  name        TEXT    NOT NULL,
  description TEXT,
  library_id  INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
  recipe      TEXT    NOT NULL,
  revision    INTEGER NOT NULL DEFAULT 1,
  created_at  INTEGER NOT NULL,
  updated_at  INTEGER NOT NULL
) STRICT;
CREATE INDEX block_presets_library ON block_presets (library_id);

-- Record series. It follows the guide's series id (series_key), or where the guide has none
-- the same title on the same channel. channel_id NULL is any channel of the source. keep_newest
-- and keep_days are the rule's own keep choice, both NULL to keep every episode; the paddings
-- are NULL to use the owner's defaults. priority is -1, 0 or 1. Tens of rows.
CREATE TABLE recording_rules (
  id            INTEGER NOT NULL PRIMARY KEY,
  public_id     TEXT    NOT NULL UNIQUE,
  profile_id    INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  source_id     INTEGER NOT NULL REFERENCES live_sources(id) ON DELETE CASCADE,
  channel_id    INTEGER REFERENCES channels(id) ON DELETE CASCADE,
  series_key    TEXT,
  title         TEXT    NOT NULL,
  new_only      INTEGER NOT NULL DEFAULT 0 CHECK (new_only IN (0, 1)),
  keep_newest   INTEGER,
  keep_days     INTEGER,
  priority      INTEGER NOT NULL DEFAULT 0,
  pad_before_ms INTEGER,
  pad_after_ms  INTEGER,
  paused        INTEGER NOT NULL DEFAULT 0 CHECK (paused IN (0, 1)),
  created_at    INTEGER NOT NULL,
  updated_at    INTEGER NOT NULL
) STRICT;
CREATE INDEX recording_rules_profile ON recording_rules (profile_id);
CREATE INDEX recording_rules_source ON recording_rules (source_id);
CREATE INDEX recording_rules_channel ON recording_rules (channel_id) WHERE channel_id IS NOT NULL;

-- One airing someone asked to record: a booking, then what is recording, then either a
-- recorded title or a line of History. One row however many people or rules ask. The
-- programme's own facts are copied, so the row reads the same after the guide moves on, and
-- programme_key finds it again when the guide moves it; in_guide is 0 while the guide no
-- longer lists it. profile_id and booked_by say who booked it; also_wanted_by is a JSON list
-- of the other profiles that asked for the same airing, in order, so when the booker cancels
-- the booking passes to the next of them and is cancelled only when nobody is left. choice is
-- the person's answer to a tuner clash, which holds for this airing. reason says why a
-- recording is partial or failed. Once recording starts, item_id is its title in the one Recorded TV library, an
-- ordinary item with its file in the recordings folder; keep protects that title from every
-- automatic deletion. A clash is worked out from the upcoming rows, never stored. Failed,
-- cancelled and skipped rows are removed after 30 days, and a recorded row goes when its
-- title is deleted. Upcoming is one range of recordings_upcoming.
CREATE TABLE recordings (
  id             INTEGER NOT NULL PRIMARY KEY,
  public_id      TEXT    NOT NULL UNIQUE,
  channel_id     INTEGER REFERENCES channels(id) ON DELETE SET NULL,
  rule_id        INTEGER REFERENCES recording_rules(id) ON DELETE SET NULL,
  profile_id     INTEGER REFERENCES profiles(id) ON DELETE SET NULL,
  item_id        INTEGER REFERENCES items(id) ON DELETE SET NULL,
  booked_by      TEXT    NOT NULL,
  also_wanted_by TEXT,
  channel_name   TEXT    NOT NULL,
  programme_key  TEXT    NOT NULL,
  series_key     TEXT,
  title          TEXT    NOT NULL,
  episode_title  TEXT,
  season_number  INTEGER,
  episode_number INTEGER,
  details        TEXT,
  starts_at      INTEGER NOT NULL,
  ends_at        INTEGER NOT NULL,
  pad_before_ms  INTEGER NOT NULL,
  pad_after_ms   INTEGER NOT NULL,
  priority       INTEGER NOT NULL DEFAULT 0,
  state          TEXT    NOT NULL DEFAULT 'scheduled'
                 CHECK (state IN ('scheduled', 'recording', 'recorded', 'partial', 'failed', 'cancelled', 'skipped')),
  reason         TEXT,
  choice         TEXT    CHECK (choice IN ('record', 'skip')),
  in_guide       INTEGER NOT NULL DEFAULT 1 CHECK (in_guide IN (0, 1)),
  keep           INTEGER NOT NULL DEFAULT 0 CHECK (keep IN (0, 1)),
  created_at     INTEGER NOT NULL,
  updated_at     INTEGER NOT NULL
) STRICT;
CREATE UNIQUE INDEX recordings_airing ON recordings (channel_id, programme_key);
CREATE INDEX recordings_upcoming ON recordings (starts_at) WHERE state IN ('scheduled', 'recording');
CREATE INDEX recordings_finished ON recordings (state, updated_at);
CREATE INDEX recordings_series ON recordings (series_key, season_number, episode_number) WHERE series_key IS NOT NULL;
CREATE INDEX recordings_rule ON recordings (rule_id) WHERE rule_id IS NOT NULL;
CREATE INDEX recordings_profile ON recordings (profile_id) WHERE profile_id IS NOT NULL;
CREATE INDEX recordings_item ON recordings (item_id) WHERE item_id IS NOT NULL;

-- "Remind me" on an upcoming airing, for a profile on all its devices. The app shows it; the
-- server only keeps it. The times follow the programme when the guide moves it, and the row
-- goes when the programme ends. A few rows per profile.
CREATE TABLE reminders (
  profile_id    INTEGER NOT NULL REFERENCES profiles(id) ON DELETE CASCADE,
  channel_id    INTEGER NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
  programme_key TEXT    NOT NULL,
  title         TEXT    NOT NULL,
  starts_at     INTEGER NOT NULL,
  ends_at       INTEGER NOT NULL,
  created_at    INTEGER NOT NULL,
  PRIMARY KEY (profile_id, channel_id, programme_key)
) STRICT, WITHOUT ROWID;
CREATE INDEX reminders_channel ON reminders (channel_id, programme_key);
CREATE INDEX reminders_end ON reminders (ends_at);
