-- Portico pre-release schema. Edit this baseline directly; reset development state after changes.
CREATE TABLE catalog_kinds(
 id INTEGER PRIMARY KEY CHECK(id BETWEEN 1 AND 99),
 name TEXT NOT NULL UNIQUE,
 playable INTEGER NOT NULL CHECK(playable IN(0,1)),
 container INTEGER NOT NULL CHECK(container IN(0,1)),
 searchable INTEGER NOT NULL CHECK(searchable IN(0,1)),
 browsable INTEGER NOT NULL CHECK(browsable IN(0,1)),
 listening INTEGER NOT NULL CHECK(listening IN(0,1))) STRICT;
INSERT INTO catalog_kinds VALUES
 (1,'movie',1,0,1,1,0),(2,'show',0,1,1,1,0),(3,'season',0,1,0,1,0),(4,'episode',1,0,1,1,0),
 (5,'artist',0,1,1,1,1),(6,'album',0,1,1,1,1),(7,'song',1,0,1,1,1),(8,'book',0,1,1,1,1),
 (9,'audiobook_file',1,0,0,1,1),(10,'collection',0,1,1,1,0),(11,'extra',1,0,0,1,0),
 (12,'disc',0,1,0,0,1),(13,'author',0,1,1,1,1),
 (20,'ebook',0,0,1,1,0),(21,'comic',0,0,1,1,0);

PRAGMA journal_mode=WAL;
PRAGMA synchronous=FULL;
PRAGMA wal_autocheckpoint=1000;
CREATE TABLE catalog_libraries(id INTEGER PRIMARY KEY,name TEXT NOT NULL) STRICT;
INSERT INTO catalog_libraries VALUES(1,'main mixed library'),(2,'small mixed library');
CREATE TABLE catalog_entities(
 id INTEGER PRIMARY KEY,
 public_id BLOB NOT NULL UNIQUE CHECK(length(public_id)=16),
 library_id INTEGER NOT NULL REFERENCES catalog_libraries(id),
 kind INTEGER NOT NULL REFERENCES catalog_kinds(id),
 parent_id INTEGER,
 title TEXT NOT NULL,
 sort_title TEXT NOT NULL DEFAULT '',
 metadata_language TEXT NOT NULL DEFAULT '',
 sort_key TEXT NOT NULL COLLATE NOCASE,
 sort_head TEXT NOT NULL,
 sort_key_keep_articles TEXT NOT NULL COLLATE NOCASE,
 sort_head_keep_articles TEXT NOT NULL,
 random_key INTEGER NOT NULL CHECK(random_key BETWEEN 0 AND 4294967295),
 year INTEGER NOT NULL DEFAULT 0,
 added_at TEXT,
 last_content_added_at TEXT,
 retired INTEGER NOT NULL DEFAULT 0 CHECK(retired IN(0,1)),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 overview TEXT NOT NULL DEFAULT '',
 original_title TEXT NOT NULL DEFAULT '',
 edition TEXT NOT NULL DEFAULT '',
 tagline TEXT NOT NULL DEFAULT '',
 release_date TEXT NOT NULL DEFAULT '',
 content_rating TEXT NOT NULL DEFAULT '',
 studio TEXT NOT NULL DEFAULT '',
 network TEXT NOT NULL DEFAULT '',
 country TEXT NOT NULL DEFAULT '',
 duration_key REAL NOT NULL DEFAULT 0,
 community_rating_key REAL NOT NULL DEFAULT 0,
 critic_rating_key REAL NOT NULL DEFAULT 0,
 file_size_key INTEGER NOT NULL DEFAULT 0,
 resolution_key INTEGER NOT NULL DEFAULT 0,
 release_key TEXT NOT NULL DEFAULT '',
 episode_count INTEGER NOT NULL DEFAULT 0 CHECK(episode_count>=0),
 latest_aired_key TEXT NOT NULL DEFAULT '',
 track_key INTEGER NOT NULL DEFAULT 0,
 local_key TEXT NOT NULL DEFAULT '',
 source_edition TEXT NOT NULL DEFAULT '',
 edition_key TEXT NOT NULL DEFAULT '',
 show_id INTEGER,
 season_id INTEGER,
 season_number INTEGER,
 numbering TEXT,
 episode_number INTEGER,
 local_identity_status TEXT,
 ordering_basis TEXT,
 air_date INTEGER,
 artist_id INTEGER,
 album_id INTEGER,
 book_id INTEGER,
 disc_number INTEGER,
 track_number INTEGER,
 part_number INTEGER,
 author TEXT NOT NULL DEFAULT '',
 narrator TEXT NOT NULL DEFAULT '',
 series TEXT NOT NULL DEFAULT '',
 series_position TEXT NOT NULL DEFAULT '',
 record_label TEXT NOT NULL DEFAULT '',
 release_country TEXT NOT NULL DEFAULT '',
 collection_name_key TEXT,
 collection_revision INTEGER NOT NULL DEFAULT 1 CHECK(collection_revision>0),
 hide_seasons INTEGER NOT NULL DEFAULT 0 CHECK(hide_seasons IN(0,1)),
 absolute_numbering INTEGER NOT NULL DEFAULT 0 CHECK(absolute_numbering IN(0,1)),
 show_ranges TEXT NOT NULL DEFAULT '',
 newest_first INTEGER NOT NULL DEFAULT 0 CHECK(newest_first IN(0,1)),
 show_settings_revision INTEGER NOT NULL DEFAULT 1 CHECK(show_settings_revision>0),
 kept_apart INTEGER NOT NULL DEFAULT 0 CHECK(kept_apart IN(0,1)),
 missing_since_ms INTEGER,
 enrichment_domain TEXT NOT NULL DEFAULT '',
 enrichment_qid INTEGER,
 metadata_incarnation BLOB NOT NULL CHECK(length(metadata_incarnation)=16),
 source_revision INTEGER NOT NULL DEFAULT 1 CHECK(source_revision>0),
 identity_revision INTEGER NOT NULL DEFAULT 1 CHECK(identity_revision>0),
 metadata_revision INTEGER NOT NULL DEFAULT 1 CHECK(metadata_revision>0),
 tv_incarnation BLOB CHECK(length(tv_incarnation)=16),
 tv_source_revision INTEGER,
 tv_hierarchy_revision INTEGER,
 tv_selection_revision INTEGER,
 tv_descriptive_revision INTEGER,
 music_incarnation BLOB CHECK(length(music_incarnation)=16),
 music_revision INTEGER,
 music_membership_revision INTEGER,
 artwork_incarnation BLOB NOT NULL CHECK(length(artwork_incarnation)=16),
 artwork_revision INTEGER NOT NULL DEFAULT 1 CHECK(artwork_revision>0),
 artwork_dirty INTEGER NOT NULL DEFAULT 0 CHECK(artwork_dirty IN(0,1)),
 artwork_job_revision INTEGER NOT NULL DEFAULT 1 CHECK(artwork_job_revision>0),
 artwork_lease_owner TEXT NOT NULL DEFAULT '',
 artwork_lease_generation INTEGER NOT NULL DEFAULT 0,
 artwork_lease_expires_ms INTEGER NOT NULL DEFAULT 0,
 artwork_attempts INTEGER NOT NULL DEFAULT 0,
 artwork_next_attempt_ms INTEGER,
 artwork_due_ms INTEGER GENERATED ALWAYS AS
 (CASE WHEN artwork_next_attempt_ms IS NULL THEN NULL ELSE max(artwork_next_attempt_ms,artwork_lease_expires_ms) END) VIRTUAL,
 artwork_claim_source_revision INTEGER,
 artwork_claim_artwork_revision INTEGER,
 artwork_claim_incarnation BLOB,
 artwork_after_provider TEXT NOT NULL DEFAULT '',
 artwork_error_code TEXT NOT NULL DEFAULT '',
 credit_owner_revision INTEGER NOT NULL DEFAULT 1 CHECK(credit_owner_revision>0),
 credit_owner_locked INTEGER NOT NULL DEFAULT 0 CHECK(credit_owner_locked IN(0,1)),
 credit_owner_actor TEXT NOT NULL DEFAULT '',
 credit_owner_observed_at TEXT NOT NULL DEFAULT '',
 screen_attention_reason TEXT,
 screen_attention_confidence REAL,
 screen_attention_generation INTEGER,
 screen_attention_dismissed_generation INTEGER,
 listening_attention_reason TEXT,
 listening_attention_confidence REAL,
 listening_attention_generation INTEGER,
 listening_attention_dismissed_generation INTEGER,
 playback_origin_incarnation BLOB CHECK(playback_origin_incarnation IS NULL OR length(playback_origin_incarnation)=16),
 playback_origin_revision INTEGER CHECK(playback_origin_revision IS NULL OR playback_origin_revision>0),
 subtitle_collection_revision INTEGER NOT NULL DEFAULT 1 CHECK(subtitle_collection_revision>0),
 has_reading_attribution_members INTEGER NOT NULL DEFAULT 0 CHECK(has_reading_attribution_members IN(0,1)),
 reading_publication_type TEXT CHECK(reading_publication_type IN('book','magazine','newspaper')),
 reading_publication_type_source TEXT CHECK(reading_publication_type_source IN('owner','file-metadata')),
 reading_issue_date TEXT CHECK(reading_issue_date IS NULL OR length(reading_issue_date) IN(7,10)),
 reading_issue_date_source TEXT CHECK(reading_issue_date_source='file-name'),
 reading_author_value TEXT,
 reading_author_key TEXT,
 reading_author_source TEXT CHECK(reading_author_source IN('owner','file-metadata','file-name','folder')),
 reading_series_value TEXT,
 reading_series_key TEXT,
 reading_series_id TEXT,
 reading_series_source TEXT CHECK(reading_series_source IN('owner','file-metadata','file-name','folder')),
 reading_numbered_order INTEGER CHECK(reading_numbered_order IN(0,1)),
 reading_number_value REAL CHECK(reading_number_value>=0 AND reading_number_value<=1.7976931348623157e308),
 CHECK((playback_origin_incarnation IS NULL)=(playback_origin_revision IS NULL)),
 UNIQUE(library_id,id),
 FOREIGN KEY(library_id,parent_id) REFERENCES catalog_entities(library_id,id),
 FOREIGN KEY(library_id,show_id) REFERENCES catalog_entities(library_id,id),
 FOREIGN KEY(library_id,season_id) REFERENCES catalog_entities(library_id,id),
 FOREIGN KEY(library_id,artist_id) REFERENCES catalog_entities(library_id,id),
 FOREIGN KEY(library_id,album_id) REFERENCES catalog_entities(library_id,id),
 FOREIGN KEY(library_id,book_id) REFERENCES catalog_entities(library_id,id),
 CHECK(kind<>3 OR (show_id IS NOT NULL AND season_number IS NOT NULL)),
 CHECK(kind<>4 OR (show_id IS NOT NULL AND numbering IN('seasonal','absolute') AND episode_number IS NOT NULL)),
 CHECK(kind<>6 OR artist_id IS NOT NULL),
 CHECK(kind<>7 OR album_id IS NOT NULL),
 CHECK(kind<>9 OR book_id IS NOT NULL),
 CHECK((playback_origin_incarnation IS NULL)=(playback_origin_revision IS NULL)),
 CHECK((reading_publication_type IS NULL)=(reading_publication_type_source IS NULL)),
 CHECK((reading_issue_date IS NULL)=(reading_issue_date_source IS NULL)),
 CHECK((reading_author_value IS NULL AND reading_author_key IS NULL AND reading_author_source IS NULL) OR (reading_author_value IS NOT NULL AND reading_author_key IS NOT NULL AND reading_author_source IS NOT NULL)),
 CHECK((reading_series_value IS NULL AND reading_series_key IS NULL AND reading_series_id IS NULL AND reading_series_source IS NULL) OR (reading_series_value IS NOT NULL AND reading_series_key IS NOT NULL AND reading_series_id IS NOT NULL AND reading_series_source IS NOT NULL)),
 CHECK(reading_numbered_order=0 OR reading_number_value=0),
 CHECK(has_reading_attribution_members=0 OR (reading_numbered_order IS NOT NULL AND reading_number_value IS NOT NULL))
) STRICT;

















CREATE TABLE catalog_assets(id INTEGER PRIMARY KEY,token TEXT NOT NULL UNIQUE,path TEXT NOT NULL UNIQUE,size INTEGER NOT NULL,modified_ns INTEGER NOT NULL,container TEXT NOT NULL,video_codec TEXT NOT NULL,audio_codec TEXT NOT NULL,width INTEGER NOT NULL,height INTEGER NOT NULL,duration REAL NOT NULL,available INTEGER NOT NULL) STRICT;
CREATE TABLE catalog_asset_links(entity_id INTEGER NOT NULL REFERENCES catalog_entities(id),asset_id INTEGER NOT NULL REFERENCES catalog_assets(id),part_index INTEGER NOT NULL,available INTEGER NOT NULL,PRIMARY KEY(entity_id,asset_id)) WITHOUT ROWID,STRICT;
CREATE INDEX catalog_asset_links_asset ON catalog_asset_links(asset_id,entity_id);
CREATE TABLE catalog_terms(id INTEGER PRIMARY KEY,key TEXT NOT NULL UNIQUE,label TEXT NOT NULL) STRICT;
WITH RECURSIVE n(id) AS(VALUES(1) UNION ALL SELECT id+1 FROM n WHERE id<30) INSERT INTO catalog_terms SELECT id,printf('genre-%d',id),printf('Genre %d',id) FROM n;
CREATE TABLE catalog_entity_terms(entity_id INTEGER NOT NULL REFERENCES catalog_entities(id),term_id INTEGER NOT NULL REFERENCES catalog_terms(id),PRIMARY KEY(entity_id,term_id)) WITHOUT ROWID,STRICT;
CREATE INDEX catalog_term_entities ON catalog_entity_terms(term_id,entity_id);
CREATE TABLE catalog_people(id INTEGER PRIMARY KEY,name TEXT NOT NULL) STRICT;
WITH RECURSIVE n(id) AS(VALUES(1) UNION ALL SELECT id+1 FROM n WHERE id<2000) INSERT INTO catalog_people SELECT id,printf('Person %d',id) FROM n;
CREATE TABLE catalog_credits(entity_id INTEGER NOT NULL REFERENCES catalog_entities(id),ord INTEGER NOT NULL,person_id INTEGER NOT NULL REFERENCES catalog_people(id),role_id INTEGER NOT NULL,PRIMARY KEY(entity_id,ord)) WITHOUT ROWID,STRICT;
CREATE INDEX catalog_credits_person ON catalog_credits(person_id,entity_id);
CREATE TABLE personal_items(profile_id TEXT NOT NULL,item_id INTEGER NOT NULL REFERENCES catalog_entities(id),rating INTEGER NOT NULL,last_played_at TEXT NOT NULL,plays INTEGER NOT NULL,revision INTEGER NOT NULL,PRIMARY KEY(profile_id,item_id)) WITHOUT ROWID,STRICT;
CREATE INDEX personal_rating ON personal_items(profile_id,rating,item_id);
CREATE INDEX personal_last ON personal_items(profile_id,last_played_at,item_id);
CREATE INDEX personal_plays ON personal_items(profile_id,plays,item_id);
CREATE TABLE recommendation_candidates(profile_id TEXT NOT NULL,entity_id INTEGER NOT NULL REFERENCES catalog_entities(id),score REAL NOT NULL,PRIMARY KEY(profile_id,entity_id)) WITHOUT ROWID,STRICT;
CREATE TABLE catalog_collection_members(collection_id INTEGER NOT NULL REFERENCES catalog_entities(id),item_id INTEGER NOT NULL REFERENCES catalog_entities(id),order_key TEXT NOT NULL,PRIMARY KEY(collection_id,item_id)) WITHOUT ROWID,STRICT;
CREATE INDEX catalog_collection_members_page ON catalog_collection_members(collection_id,order_key,item_id);
CREATE INDEX catalog_collection_members_item ON catalog_collection_members(item_id,collection_id);
CREATE TABLE library_revisions(library_id INTEGER PRIMARY KEY REFERENCES catalog_libraries(id),revision INTEGER NOT NULL) STRICT;
INSERT INTO library_revisions VALUES(1,1),(2,1);
-- Title lexical intersection control only; Q owns factored full-prefix A/B.
CREATE TABLE search_own(entity_id INTEGER PRIMARY KEY REFERENCES catalog_entities(id),library_id INTEGER NOT NULL,kind INTEGER NOT NULL,title_fold TEXT NOT NULL) STRICT;
CREATE INDEX search_own_exact ON search_own(library_id,kind,title_fold,entity_id);
CREATE VIRTUAL TABLE search_own_prefix USING fts5(title_fold,content='search_own',content_rowid='entity_id',tokenize='unicode61',prefix='2 3 4');

CREATE INDEX catalog_entities_title ON catalog_entities(library_id,kind,sort_key,id);
CREATE INDEX catalog_entities_title_keep_articles ON catalog_entities(library_id,kind,sort_key_keep_articles,id);
CREATE INDEX catalog_entities_parent ON catalog_entities(parent_id,id) WHERE parent_id IS NOT NULL;
CREATE INDEX catalog_entities_library ON catalog_entities(library_id,kind,id);
CREATE INDEX catalog_entities_library_id ON catalog_entities(library_id,id);
CREATE INDEX catalog_entities_year ON catalog_entities(library_id,kind,year,id);
CREATE INDEX catalog_entities_random ON catalog_entities(library_id,kind,random_key,id);
CREATE INDEX catalog_entities_added_v1 ON catalog_entities(library_id,kind,added_at,id);
CREATE INDEX catalog_entities_content_added ON catalog_entities(library_id,kind,last_content_added_at,id);
CREATE INDEX catalog_entities_duration ON catalog_entities(library_id,kind,duration_key,id);
CREATE INDEX catalog_entities_rating ON catalog_entities(library_id,kind,community_rating_key,id);
CREATE INDEX catalog_entities_critic ON catalog_entities(library_id,kind,critic_rating_key,id);
CREATE INDEX catalog_entities_file_size ON catalog_entities(library_id,kind,file_size_key,id);
CREATE INDEX catalog_entities_resolution ON catalog_entities(library_id,kind,resolution_key,id);
CREATE INDEX catalog_entities_release ON catalog_entities(library_id,kind,release_key,id);
CREATE INDEX catalog_entities_episode_count ON catalog_entities(library_id,kind,episode_count,id);
CREATE INDEX catalog_entities_latest_aired ON catalog_entities(library_id,kind,latest_aired_key,id);
CREATE INDEX catalog_entities_track ON catalog_entities(library_id,kind,track_key,id);
CREATE INDEX catalog_entities_show ON catalog_entities(show_id,season_id,episode_number,id) WHERE show_id IS NOT NULL;
CREATE INDEX catalog_entities_show_fk ON catalog_entities(show_id,id) WHERE show_id IS NOT NULL;
CREATE UNIQUE INDEX catalog_entities_season_number ON catalog_entities(show_id,season_number) WHERE kind=3;
CREATE UNIQUE INDEX catalog_entities_episode_position ON catalog_entities(show_id,COALESCE(season_id,0),numbering,episode_number) WHERE kind=4;
CREATE INDEX catalog_entities_season ON catalog_entities(season_id,episode_number,id) WHERE season_id IS NOT NULL;
CREATE INDEX catalog_entities_artist ON catalog_entities(artist_id,id) WHERE artist_id IS NOT NULL;
CREATE INDEX catalog_entities_album ON catalog_entities(album_id,COALESCE(disc_number,1),COALESCE(track_number,0),id) WHERE album_id IS NOT NULL;
CREATE INDEX catalog_entities_book ON catalog_entities(book_id,COALESCE(disc_number,1),COALESCE(part_number,0),id) WHERE book_id IS NOT NULL;
CREATE UNIQUE INDEX catalog_entities_collection_name ON catalog_entities(library_id,collection_name_key) WHERE kind=10;
CREATE INDEX catalog_entities_local ON catalog_entities(kind,local_key,id);
CREATE INDEX catalog_entities_book_author ON catalog_entities(library_id,kind,author COLLATE NOCASE,id) WHERE kind=8;
CREATE INDEX catalog_entities_missing ON catalog_entities(library_id,kind,missing_since_ms,id) WHERE missing_since_ms IS NOT NULL;
CREATE INDEX catalog_entities_artwork_due ON catalog_entities(artwork_due_ms,id) WHERE artwork_dirty=1 AND artwork_next_attempt_ms IS NOT NULL;
CREATE INDEX catalog_entities_parent_duration_key ON catalog_entities(parent_id,kind,duration_key,id) WHERE parent_id IS NOT NULL;
CREATE INDEX catalog_entities_parent_community_rating_key ON catalog_entities(parent_id,kind,community_rating_key,id) WHERE parent_id IS NOT NULL;
CREATE INDEX catalog_entities_parent_critic_rating_key ON catalog_entities(parent_id,kind,critic_rating_key,id) WHERE parent_id IS NOT NULL;
CREATE INDEX catalog_entities_parent_file_size_key ON catalog_entities(parent_id,kind,file_size_key,id) WHERE parent_id IS NOT NULL;
CREATE INDEX catalog_entities_parent_resolution_key ON catalog_entities(parent_id,kind,resolution_key,id) WHERE parent_id IS NOT NULL;
CREATE INDEX catalog_entities_parent_latest_aired_key ON catalog_entities(parent_id,kind,latest_aired_key,id) WHERE parent_id IS NOT NULL;