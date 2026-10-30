-- The dataset schema, as the Codex brief's §4 defines it (the contract).
PRAGMA user_version = 1;   -- schema version
PRAGMA page_size = 4096;

CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT;
-- keys: schema_version, domain ('screen' | 'music' | 'books'), dataset_version (YYYY.MM.DD[.n]),
--       kind ('full' | 'delta'), base_version (delta only), vocabulary_version, built_at (UTC ISO),
--       model, license ('CC-BY-SA-4.0'), attribution (text), source_repo

CREATE TABLE kinds(id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE) STRICT;
-- fixed: 1 movie, 2 show, 3 artist, 4 album, 5 book

CREATE TABLE providers(id INTEGER PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, UNIQUE(name, kind)) STRICT;
-- fixed (append-only): 1 tmdb/movie, 2 tmdb/tv, 3 tvdb/series, 4 tvdb/movie, 5 anilist/anime, 6 imdb/title,
--   7 mal/anime, 8 musicbrainz/artist, 9 musicbrainz/release-group, 10 openlibrary/work, 11 isbn/13, 12 isbn/10

CREATE TABLE works(
  qid INTEGER PRIMARY KEY,          -- Wikidata QID number: Q47703 -> 47703. Stable across versions.
  kind INTEGER NOT NULL REFERENCES kinds(id),
  anime INTEGER NOT NULL DEFAULT 0 CHECK(anime IN (0,1)),
  audiobook INTEGER NOT NULL DEFAULT 0 CHECK(audiobook IN (0,1)),   -- reserved for books
  tagged INTEGER NOT NULL CHECK(tagged IN (0,1)),                   -- 0: identity only (a similar-title target without a usable source)
  confidence INTEGER CHECK(confidence IN (1,2)),                    -- 2 high, 1 medium; NULL when untagged; low is never published
  year INTEGER,
  sitelinks INTEGER NOT NULL,       -- Wikidata sitelink count (popularity)
  title TEXT NOT NULL               -- Wikidata English label, else the original label (for people inspecting the file)
) STRICT;

CREATE TABLE work_ids(
  provider INTEGER NOT NULL REFERENCES providers(id),
  provider_id ANY NOT NULL,         -- INTEGER for numeric providers (TMDB, TVDB, AniList, MAL, ISBN);
                                    -- TEXT as the provider writes it otherwise ('tt0113277', MusicBrainz UUIDs, 'OL45883W')
  qid INTEGER NOT NULL,
  PRIMARY KEY(provider, provider_id, qid)
) STRICT, WITHOUT ROWID;

CREATE TABLE vocabulary(
  id INTEGER PRIMARY KEY,           -- never reused
  tag TEXT NOT NULL UNIQUE,         -- 'family:slug', e.g. 'mood:cozy'
  family TEXT NOT NULL,             -- mood, tone, theme, pacing, style, setting, narrative, audience, intensity, anime, tv, music, book
  label TEXT NOT NULL,              -- US English display label
  description TEXT NOT NULL,        -- one sentence: what earns the tag
  applies_to TEXT NOT NULL,         -- JSON array of kind names
  added_in INTEGER NOT NULL,        -- vocabulary_version that introduced it
  retired_in INTEGER                -- tags are never renamed or reused; they retire
) STRICT;

CREATE TABLE work_tags(
  qid INTEGER NOT NULL,
  tag INTEGER NOT NULL REFERENCES vocabulary(id),
  strength INTEGER NOT NULL CHECK(strength BETWEEN 1 AND 3),
  PRIMARY KEY(qid, tag)
) STRICT, WITHOUT ROWID;

CREATE TABLE work_similar(
  qid INTEGER NOT NULL,
  rank INTEGER NOT NULL CHECK(rank BETWEEN 1 AND 15),   -- music artists: at most 10
  similar INTEGER NOT NULL,         -- a qid present in works
  PRIMARY KEY(qid, rank),
  CHECK(qid <> similar)
) STRICT, WITHOUT ROWID;

CREATE TABLE work_sources(
  qid INTEGER NOT NULL,
  source INTEGER NOT NULL CHECK(source IN (1,2,3)),     -- 1 Wikipedia, 2 Fandom, 3 Wikidata
  site TEXT NOT NULL,               -- Wikipedia language ('en', 'ja'); Fandom wiki subdomain; 'www' for Wikidata
  revision INTEGER NOT NULL,        -- page revision id; the link is rebuilt from it:
                                    --   https://{site}.wikipedia.org/w/index.php?oldid={revision}
                                    --   https://{site}.fandom.com/wiki/?oldid={revision}
                                    --   https://www.wikidata.org/w/index.php?oldid={revision}
  PRIMARY KEY(qid, source, site)
) STRICT, WITHOUT ROWID;

CREATE TABLE fandom_wikis(site TEXT PRIMARY KEY, license TEXT NOT NULL) STRICT;  -- license per Fandom wiki used ('CC-BY-SA-3.0' …)

CREATE TABLE redirects(old_qid INTEGER PRIMARY KEY, qid INTEGER NOT NULL) STRICT;  -- Wikidata merges
CREATE TABLE removed(qid INTEGER PRIMARY KEY) STRICT;                              -- delta files only
