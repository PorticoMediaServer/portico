-- Selected narrow scalar-contribution primitive; measurement/semantics gates open.
-- Distinct from generic browse cards, facets or post-applied settlement.
CREATE TABLE catalog_scalar_contributions(
 library_id INTEGER NOT NULL,
 parent_id INTEGER NOT NULL,
 contributor_id INTEGER NOT NULL,
 source INTEGER NOT NULL CHECK(source BETWEEN 0 AND 8),
 contributor_revision INTEGER NOT NULL CHECK(contributor_revision>0),
 duration REAL,
 community_rating REAL,
 critic_rating REAL NOT NULL DEFAULT 0,
 file_size INTEGER NOT NULL DEFAULT 0,
 resolution INTEGER NOT NULL DEFAULT 0,
 latest_aired_key TEXT NOT NULL DEFAULT '',
 PRIMARY KEY(parent_id,contributor_id,source),
 FOREIGN KEY(library_id,parent_id) REFERENCES catalog_entities(library_id,id),
 FOREIGN KEY(library_id,contributor_id) REFERENCES catalog_entities(library_id,id)
) WITHOUT ROWID, STRICT;
CREATE INDEX catalog_scalar_contributor ON catalog_scalar_contributions(contributor_id,parent_id,source);
CREATE INDEX catalog_scalar_library_parent ON catalog_scalar_contributions(library_id,parent_id,contributor_id,source);
CREATE INDEX catalog_scalar_library_contributor ON catalog_scalar_contributions(library_id,contributor_id,parent_id,source);
CREATE INDEX catalog_scalar_duration ON catalog_scalar_contributions(parent_id,duration DESC,contributor_id,source) WHERE duration IS NOT NULL;
CREATE INDEX catalog_scalar_rating ON catalog_scalar_contributions(parent_id,community_rating DESC,contributor_id,source) WHERE community_rating IS NOT NULL;
CREATE INDEX catalog_scalar_critic ON catalog_scalar_contributions(parent_id,critic_rating DESC,contributor_id,source);
CREATE INDEX catalog_scalar_size ON catalog_scalar_contributions(parent_id,file_size DESC,contributor_id,source);
CREATE INDEX catalog_scalar_resolution ON catalog_scalar_contributions(parent_id,resolution DESC,contributor_id,source);
CREATE INDEX catalog_scalar_latest_aired ON catalog_scalar_contributions(parent_id,latest_aired_key DESC,contributor_id,source);
