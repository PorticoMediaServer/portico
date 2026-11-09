-- Exact D/E subset needed by IP-08's complete TV publication set, 8 November
-- 2026. Proposed fixture only; groups D and E are not embedded and this makes
-- no Capture claim. The publication set/page/member body, its member namespace
-- indexes and the evidence/build owners are already embedded. This adds only
-- the IP set proof tuple on the set header and the exact receipt set reverse
-- index named by the packet. The draft's composite receipt FK cannot be added
-- by ALTER TABLE on an embedded table, so the typed writer enforces the exact
-- proof tuple explicitly; metadata_ip_receipts itself comes from the IP-06
-- fixture (testdata/ip-entity-evidence-draft.sql).
ALTER TABLE metadata_publication_sets ADD COLUMN ip_proof_owner TEXT;
ALTER TABLE metadata_publication_sets ADD COLUMN ip_proof_incarnation BLOB CHECK(ip_proof_incarnation IS NULL OR length(ip_proof_incarnation)=16);
ALTER TABLE metadata_publication_sets ADD COLUMN ip_proof_sequence INTEGER CHECK(ip_proof_sequence IS NULL OR ip_proof_sequence>0);
ALTER TABLE metadata_publication_sets ADD COLUMN ip_proof_kind TEXT GENERATED ALWAYS AS(CASE WHEN ip_proof_owner IS NOT NULL THEN 'set_seal' END) VIRTUAL;
CREATE INDEX metadata_ip_receipt_set ON metadata_ip_receipts(set_id,owner_id) WHERE set_id IS NOT NULL;
CREATE INDEX metadata_publication_sets_ip_proof ON metadata_publication_sets(ip_proof_owner,ip_proof_incarnation,ip_proof_sequence,ip_proof_kind,id);
