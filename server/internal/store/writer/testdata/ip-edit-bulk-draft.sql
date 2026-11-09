-- Exact E descriptive-bulk-v1 subset from PACKET-IP (8 November 2026).
-- Proposed fixture only; group E is not embedded and no Capture/EXPLAIN receipt
-- is claimed. metadata_edit_owners/metadata_edit_receipts come from
-- testdata/ip-text-edit-draft.sql, which this fixture must load first: batch
-- targets pin the exact atomic edit receipt through that pair.
--
-- Only the birth/capture columns and children used by IP-26 are declared. The
-- E fixed-slot reclaim block belongs to IP-28 and is deliberately absent.
ALTER TABLE metadata_repair_cascades ADD COLUMN descriptive_expected_count INTEGER CHECK(descriptive_expected_count IS NULL OR descriptive_expected_count>=0);

ALTER TABLE metadata_repair_cascades ADD COLUMN descriptive_captured_count INTEGER CHECK(descriptive_captured_count IS NULL OR descriptive_captured_count>=0);

ALTER TABLE metadata_repair_cascades ADD COLUMN descriptive_input_complete INTEGER CHECK(descriptive_input_complete IS NULL OR descriptive_input_complete IN(0,1));

ALTER TABLE metadata_repair_cascades ADD COLUMN descriptive_manifest_algorithm TEXT;

ALTER TABLE metadata_repair_cascades ADD COLUMN descriptive_manifest_digest BLOB CHECK(descriptive_manifest_digest IS NULL OR length(descriptive_manifest_digest)=32);

ALTER TABLE metadata_repair_cascades ADD COLUMN descriptive_expected_bytes INTEGER CHECK(descriptive_expected_bytes IS NULL OR (typeof(descriptive_expected_bytes)='integer' AND descriptive_expected_bytes>=0));

ALTER TABLE metadata_repair_cascades ADD COLUMN descriptive_captured_bytes INTEGER CHECK(descriptive_captured_bytes IS NULL OR (typeof(descriptive_captured_bytes)='integer' AND descriptive_captured_bytes>=0));

ALTER TABLE metadata_repair_cascades ADD COLUMN descriptive_captured_digest BLOB CHECK(descriptive_captured_digest IS NULL OR (typeof(descriptive_captured_digest)='blob' AND length(descriptive_captured_digest)=32));

ALTER TABLE metadata_repair_cascades ADD COLUMN descriptive_reclaim_after_ordinal INTEGER CHECK(descriptive_reclaim_after_ordinal IS NULL OR (typeof(descriptive_reclaim_after_ordinal)='integer' AND descriptive_reclaim_after_ordinal>=-1));

ALTER TABLE metadata_repair_cascades ADD COLUMN descriptive_receipt_reclaim_after_sequence INTEGER CHECK(descriptive_receipt_reclaim_after_sequence IS NULL OR (typeof(descriptive_receipt_reclaim_after_sequence)='integer' AND descriptive_receipt_reclaim_after_sequence>=0 AND descriptive_receipt_reclaim_after_sequence<=acknowledged_command_sequence)) CHECK((intent='descriptive-bulk-v1')=(descriptive_expected_count IS NOT NULL)) CHECK((descriptive_expected_count IS NULL)=(descriptive_captured_count IS NULL)) CHECK((descriptive_expected_count IS NULL)=(descriptive_input_complete IS NULL)) CHECK((descriptive_expected_count IS NULL)=(descriptive_manifest_algorithm IS NULL)) CHECK((descriptive_expected_count IS NULL)=(descriptive_manifest_digest IS NULL)) CHECK((descriptive_expected_count IS NULL)=(descriptive_expected_bytes IS NULL)) CHECK((descriptive_expected_count IS NULL)=(descriptive_captured_bytes IS NULL)) CHECK((descriptive_expected_count IS NULL)=(descriptive_captured_digest IS NULL)) CHECK((descriptive_expected_count IS NULL)=(descriptive_reclaim_after_ordinal IS NULL)) CHECK((descriptive_expected_count IS NULL)=(descriptive_receipt_reclaim_after_sequence IS NULL)) CHECK(intent<>'descriptive-bulk-v1' OR (length(descriptive_manifest_algorithm)>0 AND descriptive_captured_count<=descriptive_expected_count AND descriptive_captured_bytes<=descriptive_expected_bytes AND descriptive_reclaim_after_ordinal<descriptive_captured_count AND descriptive_receipt_reclaim_after_sequence<=acknowledged_command_sequence AND (descriptive_input_complete=0 OR (descriptive_captured_count=descriptive_expected_count AND descriptive_captured_bytes=descriptive_expected_bytes AND descriptive_captured_digest=descriptive_manifest_digest))));

-- E: generated constant child intent keys bind fields, targets and receipts to
-- the exact descriptive parent; credit-owner/other roots cannot own them.
CREATE UNIQUE INDEX metadata_repair_descriptive_owner ON metadata_repair_cascades(id,intent);

CREATE TABLE metadata_edit_batch_fields(
 operation_id TEXT NOT NULL,
 field TEXT NOT NULL,
 action TEXT NOT NULL CHECK(action IN('set','lock','unlock','restore')),
 value TEXT,
 lock_override INTEGER NOT NULL CHECK(lock_override BETWEEN 0 AND 2),
 descriptive_intent TEXT GENERATED ALWAYS AS('descriptive-bulk-v1') VIRTUAL,
 PRIMARY KEY(operation_id,field),
 CHECK((action='set')=(value IS NOT NULL)),
 FOREIGN KEY(operation_id,descriptive_intent) REFERENCES metadata_repair_cascades(id,intent),
 CHECK(typeof(operation_id) IN('null','text')),
 CHECK(typeof(field) IN('null','text')),
 CHECK(typeof(action) IN('null','text')),
 CHECK(typeof(value) IN('null','text')),
 CHECK(typeof(lock_override) IN('null','integer')),
 CHECK(typeof(descriptive_intent) IN('null','text'))
) WITHOUT ROWID;

CREATE TABLE metadata_edit_batch_targets(
 operation_id TEXT NOT NULL,
 ordinal INTEGER NOT NULL CHECK(ordinal>=0),
 entity_id INTEGER NOT NULL REFERENCES catalog_subjects(id),
 expected_entity_incarnation BLOB NOT NULL CHECK(length(expected_entity_incarnation)=16),
 expected_revision INTEGER NOT NULL CHECK(expected_revision>0),
 state TEXT NOT NULL CHECK(state IN('captured','applied','stale','denied')),
 result_revision INTEGER CHECK(result_revision IS NULL OR result_revision>0),
 edit_owner_id INTEGER,
 edit_command_incarnation BLOB CHECK(edit_command_incarnation IS NULL OR length(edit_command_incarnation)=16),
 edit_command_sequence INTEGER CHECK(edit_command_sequence IS NULL OR edit_command_sequence>0),
 descriptive_intent TEXT GENERATED ALWAYS AS('descriptive-bulk-v1') VIRTUAL,
 PRIMARY KEY(operation_id,ordinal),
 UNIQUE(operation_id,entity_id),
 FOREIGN KEY(edit_owner_id,edit_command_incarnation,edit_command_sequence) REFERENCES metadata_edit_receipts(owner_id,command_incarnation,command_sequence),
 CHECK((edit_owner_id IS NULL)=(edit_command_incarnation IS NULL) AND (edit_owner_id IS NULL)=(edit_command_sequence IS NULL)),
 CHECK((state='applied')=(edit_owner_id IS NOT NULL) AND (state='applied')=(result_revision IS NOT NULL)),
 FOREIGN KEY(operation_id,descriptive_intent) REFERENCES metadata_repair_cascades(id,intent),
 CHECK(typeof(operation_id) IN('null','text')),
 CHECK(typeof(ordinal) IN('null','integer')),
 CHECK(typeof(entity_id) IN('null','integer')),
 CHECK(typeof(expected_entity_incarnation) IN('null','blob')),
 CHECK(typeof(expected_revision) IN('null','integer')),
 CHECK(typeof(state) IN('null','text')),
 CHECK(typeof(result_revision) IN('null','integer')),
 CHECK(typeof(edit_owner_id) IN('null','integer')),
 CHECK(typeof(edit_command_incarnation) IN('null','blob')),
 CHECK(typeof(edit_command_sequence) IN('null','integer')),
 CHECK(typeof(descriptive_intent) IN('null','text'))
) WITHOUT ROWID;

CREATE INDEX metadata_edit_batch_entity ON metadata_edit_batch_targets(entity_id,operation_id) WHERE entity_id IS NOT NULL;

CREATE TABLE metadata_edit_batch_receipts(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 operation_id BLOB NOT NULL UNIQUE CHECK(length(operation_id)=16),
 request_digest BLOB NOT NULL CHECK(length(request_digest)=32),
 applied_ms INTEGER NOT NULL CHECK(applied_ms>=0),
 result_digest BLOB NOT NULL CHECK(length(result_digest)=32),
 command_kind TEXT NOT NULL CHECK(command_kind IN('begin','capture','seal','page','finish','cancel','reclaim')),
 accepted_revision INTEGER NOT NULL CHECK(accepted_revision>0),
 after_ordinal INTEGER NOT NULL CHECK(after_ordinal>=0),
 captured_count INTEGER NOT NULL CHECK(captured_count>=0),
 processed_count INTEGER NOT NULL CHECK(processed_count>=0),
 failed_count INTEGER NOT NULL CHECK(failed_count>=0),
 input_complete INTEGER NOT NULL CHECK(input_complete IN(0,1)),
 manifest_algorithm TEXT NOT NULL,
 manifest_digest BLOB NOT NULL CHECK(length(manifest_digest)=32),
 descriptive_intent TEXT GENERATED ALWAYS AS('descriptive-bulk-v1') VIRTUAL,
 PRIMARY KEY(owner_id,command_incarnation,command_sequence),
 FOREIGN KEY(owner_id,command_incarnation) REFERENCES metadata_repair_cascades(id,command_incarnation),
 CHECK(processed_count+failed_count<=captured_count),
 FOREIGN KEY(owner_id,descriptive_intent) REFERENCES metadata_repair_cascades(id,intent),
 CHECK(typeof(owner_id) IN('null','text')),
 CHECK(typeof(command_incarnation) IN('null','blob')),
 CHECK(typeof(command_sequence) IN('null','integer')),
 CHECK(typeof(operation_id) IN('null','blob')),
 CHECK(typeof(request_digest) IN('null','blob')),
 CHECK(typeof(applied_ms) IN('null','integer')),
 CHECK(typeof(result_digest) IN('null','blob')),
 CHECK(typeof(command_kind) IN('null','text')),
 CHECK(typeof(accepted_revision) IN('null','integer')),
 CHECK(typeof(after_ordinal) IN('null','integer')),
 CHECK(typeof(captured_count) IN('null','integer')),
 CHECK(typeof(processed_count) IN('null','integer')),
 CHECK(typeof(failed_count) IN('null','integer')),
 CHECK(typeof(input_complete) IN('null','integer')),
 CHECK(typeof(manifest_algorithm) IN('null','text')),
 CHECK(typeof(manifest_digest) IN('null','blob')),
 CHECK(typeof(descriptive_intent) IN('null','text'))
) WITHOUT ROWID;

-- E: a live bulk target pins its exact atomic edit result until bounded target
-- retirement; the FK lookup never scans every bulk operation (IP-27). The
-- reviewed edit owner/receipt pair comes from testdata/ip-text-edit-draft.sql.
CREATE INDEX metadata_edit_batch_edit_receipt ON metadata_edit_batch_targets(edit_owner_id,edit_command_incarnation,edit_command_sequence,operation_id) WHERE edit_owner_id IS NOT NULL;
