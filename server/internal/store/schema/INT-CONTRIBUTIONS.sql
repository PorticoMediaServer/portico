-- Narrow typed scalar and physical-asset contributions.
-- File-size-only changes advance only the asset file_size_revision witness.
-- No broad contributor-revision rewrite; no member/gallery/personal image.
CREATE TABLE catalog_scalar_contributions(
 library_id INTEGER NOT NULL,
 parent_id INTEGER NOT NULL,
 contributor_id INTEGER NOT NULL,
 source INTEGER NOT NULL CHECK(source BETWEEN 0 AND 8),
 contributor_revision INTEGER NOT NULL CHECK(contributor_revision>0),
 duration REAL,
 community_rating REAL,
 critic_rating REAL NOT NULL DEFAULT 0,
 resolution INTEGER NOT NULL DEFAULT 0,
 latest_aired_key TEXT NOT NULL DEFAULT '',
 membership_sequence INTEGER NOT NULL CHECK(membership_sequence>0),
 admission_age INTEGER CHECK(admission_age>=0),
 PRIMARY KEY(parent_id,contributor_id,source),
 FOREIGN KEY(library_id,parent_id) REFERENCES catalog_entities(library_id,id),
 FOREIGN KEY(library_id,contributor_id) REFERENCES catalog_entities(library_id,id),
 CHECK("library_id" IS NULL OR typeof("library_id")='integer'),
 CHECK("parent_id" IS NULL OR typeof("parent_id")='integer'),
 CHECK("contributor_id" IS NULL OR typeof("contributor_id")='integer'),
 CHECK("source" IS NULL OR typeof("source")='integer'),
 CHECK("contributor_revision" IS NULL OR typeof("contributor_revision")='integer'),
 CHECK("duration" IS NULL OR typeof("duration")IN('integer','real')),
 CHECK("community_rating" IS NULL OR typeof("community_rating")IN('integer','real')),
 CHECK("critic_rating" IS NULL OR typeof("critic_rating")IN('integer','real')),
 CHECK("resolution" IS NULL OR typeof("resolution")='integer'),
 CHECK("latest_aired_key" IS NULL OR typeof("latest_aired_key")='text'),
 CHECK("membership_sequence" IS NULL OR typeof("membership_sequence")='integer'),
 CHECK("admission_age" IS NULL OR typeof("admission_age")='integer')
) WITHOUT ROWID;

CREATE TABLE catalog_asset_contributions(
 library_id INTEGER NOT NULL,
 parent_id INTEGER NOT NULL,
 asset_id INTEGER NOT NULL,
 membership_refs INTEGER NOT NULL CHECK(membership_refs>0),
 file_size INTEGER NOT NULL,
 file_size_revision INTEGER NOT NULL CHECK(file_size_revision>0),
 PRIMARY KEY(parent_id,asset_id),
 FOREIGN KEY(library_id,parent_id) REFERENCES catalog_entities(library_id,id),
 FOREIGN KEY(library_id,asset_id) REFERENCES catalog_assets(library_id,id),
 CHECK("library_id" IS NULL OR typeof("library_id")='integer'),
 CHECK("parent_id" IS NULL OR typeof("parent_id")='integer'),
 CHECK("asset_id" IS NULL OR typeof("asset_id")='integer'),
 CHECK("membership_refs" IS NULL OR typeof("membership_refs")='integer'),
 CHECK("file_size" IS NULL OR typeof("file_size")='integer'),
 CHECK("file_size_revision" IS NULL OR typeof("file_size_revision")='integer')
) WITHOUT ROWID;

CREATE INDEX catalog_scalar_contributor ON catalog_scalar_contributions(contributor_id,parent_id,source);
CREATE INDEX catalog_scalar_library_parent ON catalog_scalar_contributions(library_id,parent_id,contributor_id,source);
CREATE INDEX catalog_scalar_library_contributor ON catalog_scalar_contributions(library_id,contributor_id,parent_id,source);
CREATE INDEX catalog_scalar_duration ON catalog_scalar_contributions(parent_id,duration DESC,contributor_id,source) WHERE duration IS NOT NULL;
CREATE INDEX catalog_scalar_rating ON catalog_scalar_contributions(parent_id,community_rating DESC,contributor_id,source) WHERE community_rating IS NOT NULL;
CREATE INDEX catalog_scalar_critic ON catalog_scalar_contributions(parent_id,critic_rating DESC,contributor_id,source);
CREATE INDEX catalog_scalar_resolution ON catalog_scalar_contributions(parent_id,resolution DESC,contributor_id,source);
CREATE INDEX catalog_scalar_latest_aired ON catalog_scalar_contributions(parent_id,latest_aired_key DESC,contributor_id,source);
CREATE INDEX catalog_scalar_admission ON catalog_scalar_contributions(parent_id,admission_age,contributor_id,source) WHERE admission_age IS NOT NULL;
CREATE INDEX catalog_scalar_membership ON catalog_scalar_contributions(parent_id,membership_sequence,contributor_id,source);
CREATE INDEX catalog_asset_contribution_source ON catalog_asset_contributions(asset_id,parent_id);
CREATE INDEX catalog_asset_contribution_size ON catalog_asset_contributions(parent_id,file_size DESC,asset_id);
CREATE INDEX catalog_asset_contribution_library ON catalog_asset_contributions(library_id,parent_id,asset_id);
