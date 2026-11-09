-- Exact E artwork GC subset from PACKET-IP, 8 November 2026, loaded AFTER
-- testdata/ip-artwork-material-draft.sql, testdata/ip-artwork-material-publish-draft.sql
-- and testdata/ip-artwork-selection-draft.sql. Proposed fixture only; group E is
-- not embedded and this makes no Capture, adoption or schema-SHA claim.
--
-- IP-23 fences a source retirement against every retained reference. Selections,
-- normalized history-artwork pins and material-slot digests already have their
-- reverse indexes in the earlier fixtures/embedded schema, and the encoded blob
-- reverse indexes (artwork_sources_small/large/ambient) are embedded. The GC
-- source fence additionally needs the E receipt-digest reverse indexes that the
-- closed artwork receipt table proposes but no earlier fixture creates:
--   * artwork_receipt_selected_digest: a retained publish/upload/reencode
--     receipt still pins the accepted source it selected;
--   * artwork_receipt_previous_digest: a retained upload receipt still pins the
--     source it replaced.
-- The existing embedded backup_blob_holds singleton is the backup hold proof;
-- no new backup table is assumed.
CREATE INDEX artwork_receipt_selected_digest ON artwork_command_receipts(selected_digest,owner_id) WHERE selected_digest IS NOT NULL;
CREATE INDEX artwork_receipt_previous_digest ON artwork_command_receipts(previous_digest,owner_id) WHERE previous_digest IS NOT NULL;
