-- TEST-ONLY proposed bounded access for the SOL-INGEST skipped-directory veto.
-- INT must admit this exact index before any producer or read path may rely on it;
-- until then absence verification holds. Overlay only: no canonical/embed/Capture
-- claim and no adoption of the embedded inventory schema.
CREATE INDEX inventory_directories_skipped ON inventory_directories(job_id,relative_path) WHERE skipped>0;
