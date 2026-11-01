-- The dataset transport schema v3: a copy of portico-internal recommendations/schema/transport.sql (the contract).
PRAGMA user_version = 3;
PRAGMA page_size = 4096;

CREATE TABLE meta(key TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT;

CREATE TABLE kinds(id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE) STRICT;

CREATE TABLE providers(id INTEGER PRIMARY KEY, name TEXT NOT NULL, kind TEXT NOT NULL, UNIQUE(name, kind)) STRICT;

CREATE TABLE works(
  qid INTEGER PRIMARY KEY,
  kind INTEGER NOT NULL REFERENCES kinds(id),
  anime INTEGER NOT NULL DEFAULT 0 CHECK(anime IN (0,1)),
  audiobook INTEGER NOT NULL DEFAULT 0 CHECK(audiobook IN (0,1)),
  tagged INTEGER NOT NULL CHECK(tagged IN (0,1)),
  confidence INTEGER CHECK(confidence IN (1,2)),
  year INTEGER,
  sitelinks INTEGER NOT NULL,
  title TEXT NOT NULL
) STRICT;

CREATE TABLE work_ids(
  provider INTEGER NOT NULL REFERENCES providers(id),
  provider_id ANY NOT NULL,
  qid INTEGER NOT NULL,
  PRIMARY KEY(provider, provider_id, qid)
) STRICT, WITHOUT ROWID;

CREATE TABLE vocabulary(
  id INTEGER PRIMARY KEY,
  tag TEXT NOT NULL UNIQUE,
  family TEXT NOT NULL,
  label TEXT NOT NULL,
  description TEXT NOT NULL,
  applies_to TEXT NOT NULL,
  added_in INTEGER NOT NULL,
  retired_in INTEGER,
  -- row_label: the tag's row name in running text ("comfort watches"); NULL for
  -- families that make no row.
  row_label TEXT
) STRICT;

CREATE TABLE work_tags(
  qid INTEGER NOT NULL,
  tag INTEGER NOT NULL REFERENCES vocabulary(id),
  strength INTEGER NOT NULL CHECK(strength BETWEEN 1 AND 3),
  PRIMARY KEY(qid, tag)
) STRICT, WITHOUT ROWID;

-- relation: why the title is similar. 0 unstated, 1 tone, 2 theme, 3 premise, 4 style, 5 creator.
CREATE TABLE work_similar(
  qid INTEGER NOT NULL,
  rank INTEGER NOT NULL CHECK(rank BETWEEN 1 AND 15),
  similar INTEGER NOT NULL,
  relation INTEGER NOT NULL CHECK(relation BETWEEN 0 AND 5),
  PRIMARY KEY(qid, rank),
  CHECK(qid <> similar)
) STRICT, WITHOUT ROWID;

CREATE TABLE work_sources(
  qid INTEGER NOT NULL,
  source INTEGER NOT NULL CHECK(source IN (1,2,3)),
  site TEXT NOT NULL,
  revision INTEGER NOT NULL,
  PRIMARY KEY(qid, source, site)
) STRICT, WITHOUT ROWID;

CREATE TABLE fandom_wikis(site TEXT PRIMARY KEY, license TEXT NOT NULL) STRICT;

CREATE TABLE redirects(old_qid INTEGER PRIMARY KEY, qid INTEGER NOT NULL) STRICT;

CREATE TABLE removed(qid INTEGER PRIMARY KEY) STRICT;
