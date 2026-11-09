-- TEST-ONLY proposed bounded access for the SOL-INGEST skipped-directory veto in
-- the IA-18 read harness. The index is not embedded and INT has not admitted it;
-- this overlay defines the exact statement target for EXPLAIN-only checks. No
-- canonical/embed/Capture/golden claim.
CREATE INDEX inventory_directories_skipped ON inventory_directories(job_id,relative_path) WHERE skipped>0;
