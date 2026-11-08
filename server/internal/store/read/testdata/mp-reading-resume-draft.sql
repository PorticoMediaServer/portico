-- Exact group F reading child subset for MP-02 native tests only, copied from
-- internal planning/v3/schema-v1/phase2/changes/F-media-restart/F-READING.sql
-- at 97d28edb. Group F is NOT embedded in server/internal/store/schema/schema.sql,
-- so this is a test fixture, not canonical DDL, an embed claim or a migration.
CREATE TABLE personal_reading_resumes(
 personal_item_id INTEGER NOT NULL REFERENCES personal_items(id) ON DELETE NO ACTION,
 asset_id INTEGER NOT NULL CHECK(asset_id>0),
 source_token TEXT NOT NULL CHECK(length(CAST(source_token AS BLOB)) BETWEEN 1 AND 512 AND instr(source_token,char(0))=0),
 source_revision TEXT NOT NULL CHECK(length(CAST(source_revision AS BLOB))=64 AND source_revision NOT GLOB '*[^0-9a-f]*'),
 format TEXT NOT NULL CHECK(format IN('epub','pdf','cbz','cb7','cbr')),
 cfi TEXT,
 section TEXT,
 page_index INTEGER CHECK(page_index BETWEEN 0 AND 1000000),
 progression REAL NOT NULL CHECK(progression BETWEEN 0 AND 1),
 revision INTEGER NOT NULL CHECK(revision BETWEEN 1 AND 9007199254740991),
 observed_ms INTEGER NOT NULL CHECK(observed_ms BETWEEN 0 AND 9999999999999),
 variable_bytes INTEGER GENERATED ALWAYS AS (length(CAST(source_token AS BLOB))+length(CAST(source_revision AS BLOB))+length(CAST(format AS BLOB))+coalesce(length(CAST(cfi AS BLOB)),0)+coalesce(length(CAST(section AS BLOB)),0)) VIRTUAL,
 PRIMARY KEY(personal_item_id,asset_id),
 CHECK((format='epub' AND cfi IS NOT NULL AND length(CAST(cfi AS BLOB)) BETWEEN 1 AND 4096 AND page_index IS NULL AND (section IS NULL OR length(CAST(section AS BLOB))<=1024)) OR (format IN('pdf','cbz','cb7','cbr') AND cfi IS NULL AND section IS NULL AND page_index IS NOT NULL))
) STRICT;
-- Within one title, latest is chosen BEFORE progression/current-source filters;
-- an older unfinished version must not replace a newer completed one.
CREATE INDEX personal_reading_resumes_latest ON personal_reading_resumes(personal_item_id,observed_ms DESC,asset_id DESC);
