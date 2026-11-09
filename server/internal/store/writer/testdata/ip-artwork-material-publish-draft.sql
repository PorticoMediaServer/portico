-- Exact E artwork publish subset from PACKET-IP, 8 November 2026, loaded AFTER
-- testdata/ip-artwork-material-draft.sql. Proposed fixture only; group E is not
-- embedded and this makes no Capture, adoption or schema-SHA claim.
--
-- The material fixture already recreates the subject-job owner, the draft
-- subject column on artwork_jobs, the material intent/slot custody and the
-- closed artwork receipt table. Publish additionally needs the draft source/blob
-- revision and retirement columns that IP-20 writes but IP-19 never touches.
-- artwork_sources` original_bytes is the retained uploaded original's custody;
-- retiring/retirement_token fence a source IP-23 unlinks.
ALTER TABLE artwork_sources ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK(typeof(revision)='integer' AND revision>0);
ALTER TABLE artwork_sources ADD COLUMN original_bytes INTEGER NOT NULL DEFAULT 0 CHECK(typeof(original_bytes)='integer' AND original_bytes>=0);
ALTER TABLE artwork_sources ADD COLUMN retiring INTEGER NOT NULL DEFAULT 0 CHECK(typeof(retiring)='integer' AND retiring IN(0,1));
ALTER TABLE artwork_sources ADD COLUMN retirement_token BLOB CHECK((retiring=0 AND retirement_token IS NULL) OR (retiring=1 AND typeof(retirement_token)='blob' AND length(retirement_token)=16));

ALTER TABLE artwork_blobs ADD COLUMN storage_revision INTEGER NOT NULL DEFAULT 1 CHECK(typeof(storage_revision)='integer' AND storage_revision>0);
ALTER TABLE artwork_blobs ADD COLUMN deletion_token BLOB CHECK((status='ready' AND deletion_token IS NULL) OR (status='deleting' AND typeof(deletion_token)='blob' AND length(deletion_token)=16));
