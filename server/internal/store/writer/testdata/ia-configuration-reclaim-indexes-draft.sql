-- IA-10 configuration reclaim index supplement, exact PACKET-IA C2 text.
-- Proposed fixture only, no canonical/embed/Capture claim.
-- The IA-06 configuration drafts deliberately exclude these IA-10 indexes; a
-- reclaim test loads one exact draft first, then this exact supplement.
CREATE UNIQUE INDEX catalog_configuration_reclaim_operation ON catalog_configuration_operations(reclaim_operation_id);
CREATE INDEX catalog_configuration_receipt_due ON catalog_configuration_operations(receipt_reclaim_due_ms,original_operation_id) WHERE receipt_reclaim_due_ms IS NOT NULL;
