-- Exact D subset needed by IP-07's nonmovie entity evidence seal, 8 November
-- 2026. Proposed fixture only; group D is not embedded and this makes no
-- Capture claim. The evidence body/build, provider-operation owner and child
-- families are already embedded. This adds only the IP proof tuple on the
-- generic evidence body plus the exact evidence proof index named by the
-- packet. The draft's cross-column CHECKs and the composite receipt FK cannot
-- be added by ALTER TABLE on an embedded table, so the typed writer enforces
-- those tuples explicitly; metadata_ip_receipts itself comes from the IP-06
-- fixture (testdata/ip-entity-evidence-draft.sql).
ALTER TABLE metadata_evidence ADD COLUMN ip_seal_owner TEXT;
ALTER TABLE metadata_evidence ADD COLUMN ip_seal_incarnation BLOB CHECK(ip_seal_incarnation IS NULL OR length(ip_seal_incarnation)=16);
ALTER TABLE metadata_evidence ADD COLUMN ip_seal_sequence INTEGER CHECK(ip_seal_sequence IS NULL OR ip_seal_sequence>0);
ALTER TABLE metadata_evidence ADD COLUMN ip_seal_kind TEXT GENERATED ALWAYS AS(CASE WHEN ip_seal_owner IS NOT NULL THEN 'evidence_seal' END) VIRTUAL;
CREATE INDEX metadata_evidence_ip_proof ON metadata_evidence(ip_seal_owner,ip_seal_incarnation,ip_seal_sequence,ip_seal_kind,id,subject_id,parser_version);
