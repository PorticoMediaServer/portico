PRAGMA journal_mode=WAL;
PRAGMA synchronous=FULL;
PRAGMA wal_autocheckpoint=1000;
CREATE TABLE catalog_libraries(id INTEGER PRIMARY KEY,name TEXT NOT NULL) STRICT;
INSERT INTO catalog_libraries VALUES(1,'main mixed library'),(2,'small mixed library');
CREATE TABLE catalog_entities(
 id INTEGER PRIMARY KEY,public_id BLOB NOT NULL UNIQUE CHECK(length(public_id)=16),
 library_id INTEGER NOT NULL REFERENCES catalog_libraries(id),kind INTEGER NOT NULL,
 parent_id INTEGER REFERENCES catalog_entities(id),title TEXT NOT NULL,
 sort_key TEXT NOT NULL,sort_key_keep_articles TEXT NOT NULL,random_key INTEGER NOT NULL,
 year INTEGER NOT NULL,added_at TEXT,last_content_added_at TEXT,
 release_key TEXT NOT NULL,latest_aired_key TEXT NOT NULL,duration_key REAL NOT NULL,
 community_rating_key REAL NOT NULL,critic_rating_key REAL NOT NULL,file_size_key INTEGER NOT NULL,
 resolution_key INTEGER NOT NULL,episode_count INTEGER NOT NULL,track_key INTEGER NOT NULL,
 content_rating TEXT NOT NULL,retired INTEGER NOT NULL DEFAULT 0,revision INTEGER NOT NULL DEFAULT 1,
 overview TEXT NOT NULL DEFAULT ''
) STRICT;
CREATE INDEX catalog_entities_parent ON catalog_entities(parent_id,id);
CREATE INDEX catalog_entities_library ON catalog_entities(library_id,kind,id);
CREATE INDEX catalog_entities_title ON catalog_entities(library_id,kind,sort_key,id);
CREATE INDEX catalog_entities_title_keep ON catalog_entities(library_id,kind,sort_key_keep_articles,id);
CREATE INDEX catalog_entities_added ON catalog_entities(library_id,kind,added_at,id);
CREATE INDEX catalog_entities_year ON catalog_entities(library_id,kind,year,id);
CREATE INDEX catalog_entities_random ON catalog_entities(library_id,kind,random_key,id);
CREATE INDEX catalog_entities_duration ON catalog_entities(library_id,kind,duration_key,id);
CREATE INDEX catalog_entities_community ON catalog_entities(library_id,kind,community_rating_key,id);
CREATE INDEX catalog_entities_critic ON catalog_entities(library_id,kind,critic_rating_key,id);
CREATE INDEX catalog_entities_size ON catalog_entities(library_id,kind,file_size_key,id);
CREATE INDEX catalog_entities_resolution ON catalog_entities(library_id,kind,resolution_key,id);
CREATE INDEX catalog_entities_release ON catalog_entities(library_id,kind,release_key,id);
CREATE INDEX catalog_entities_latest ON catalog_entities(library_id,kind,latest_aired_key,id);
CREATE INDEX catalog_entities_episodes ON catalog_entities(library_id,kind,episode_count,id);
CREATE INDEX catalog_entities_track ON catalog_entities(library_id,kind,track_key,id);
CREATE INDEX catalog_entities_content_added ON catalog_entities(library_id,kind,last_content_added_at,id);
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
