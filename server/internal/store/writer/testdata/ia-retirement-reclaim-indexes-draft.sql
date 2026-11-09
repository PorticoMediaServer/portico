-- IA-10 retirement reclaim index supplement, exact PACKET-IA C2 text.
-- Proposed fixture only, no canonical/embed/Capture claim.
-- The IA-08 removal draft deliberately excludes these IA-10 indexes; a reclaim
-- test loads that exact draft first, then this exact supplement.
CREATE UNIQUE INDEX catalog_removal_reclaim_operation ON catalog_removal_progress(reclaim_operation_id);
CREATE INDEX catalog_removal_receipt_due ON catalog_removal_progress(receipt_reclaim_due_ms,kind,target) WHERE receipt_reclaim_due_ms IS NOT NULL;
