-- Lead-approved shared lifetime-root native custody, 7 November 2026.
-- Physical rows/variable bytes remain debt until actual indexed deletion.
-- ACK advances a floor only; it never refunds receipt bytes or deletes rows.
CREATE TABLE user_mutation_command_owners(
 root_id TEXT PRIMARY KEY NOT NULL REFERENCES user_mutation_roots(id) ON DELETE NO ACTION ON UPDATE NO ACTION CHECK(length(CAST(root_id AS BLOB))=22),
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 last_command_sequence INTEGER NOT NULL DEFAULT 0 CHECK(last_command_sequence>=0),
 acknowledged_command_sequence INTEGER NOT NULL DEFAULT 0 CHECK(acknowledged_command_sequence>=0),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 receipt_rows INTEGER NOT NULL DEFAULT 0 CHECK(receipt_rows>=0),
 receipt_variable_bytes INTEGER NOT NULL DEFAULT 0 CHECK(receipt_variable_bytes>=0),
 receipt_reclaim_after_sequence INTEGER NOT NULL DEFAULT 0 CHECK(receipt_reclaim_after_sequence>=0),
 receipt_reclaim_due_ms INTEGER CHECK(receipt_reclaim_due_ms IS NULL OR receipt_reclaim_due_ms>=0),
 reclaim_command_sequence INTEGER,
 reclaim_operation_id BLOB CHECK(reclaim_operation_id IS NULL OR length(reclaim_operation_id)=16),
 reclaim_request_digest BLOB CHECK(reclaim_request_digest IS NULL OR length(reclaim_request_digest)=32),
 reclaim_command_kind INTEGER,
 reclaim_result_version INTEGER,
 reclaim_result_count INTEGER,
 reclaim_result_digest BLOB CHECK(reclaim_result_digest IS NULL OR length(reclaim_result_digest)=32),
 reclaim_result_payload BLOB CHECK(reclaim_result_payload IS NULL OR length(reclaim_result_payload) BETWEEN 1 AND 4096),
 reclaim_captured_acknowledged_sequence INTEGER,
 reclaim_old_after_sequence INTEGER,
 reclaim_removed_sequence INTEGER,
 reclaim_removed_variable_bytes INTEGER,
 reclaim_result_after_sequence INTEGER,
 reclaim_result_no_next INTEGER,
 reclaim_applied_ms INTEGER,
 control_variable_bytes INTEGER GENERATED ALWAYS AS(length(CAST(root_id AS BLOB))+length(command_incarnation)+coalesce(length(reclaim_operation_id),0)+coalesce(length(reclaim_request_digest),0)+coalesce(length(reclaim_result_digest),0)+coalesce(length(reclaim_result_payload),0)) VIRTUAL,
 UNIQUE(root_id,command_incarnation),
 CHECK(acknowledged_command_sequence<=last_command_sequence AND last_command_sequence-acknowledged_command_sequence<=1),
 CHECK(receipt_reclaim_after_sequence<=acknowledged_command_sequence),
 CHECK((receipt_rows=0)=(receipt_variable_bytes=0)),
 CHECK((reclaim_command_sequence IS NULL AND reclaim_operation_id IS NULL AND reclaim_request_digest IS NULL AND reclaim_command_kind IS NULL AND reclaim_result_version IS NULL AND reclaim_result_count IS NULL AND reclaim_result_digest IS NULL AND reclaim_result_payload IS NULL AND reclaim_captured_acknowledged_sequence IS NULL AND reclaim_old_after_sequence IS NULL AND reclaim_removed_sequence IS NULL AND reclaim_removed_variable_bytes IS NULL AND reclaim_result_after_sequence IS NULL AND reclaim_result_no_next IS NULL AND reclaim_applied_ms IS NULL)
 OR (reclaim_command_sequence IS NOT NULL AND reclaim_command_sequence>0 AND reclaim_command_sequence<=last_command_sequence AND reclaim_operation_id IS NOT NULL AND reclaim_request_digest IS NOT NULL AND reclaim_command_kind IS NOT NULL AND reclaim_command_kind BETWEEN 100 AND 899 AND reclaim_result_version IS NOT NULL AND reclaim_result_version=1 AND reclaim_result_count IS NOT NULL AND reclaim_result_count IN(0,1) AND reclaim_result_digest IS NOT NULL AND reclaim_result_payload IS NOT NULL AND reclaim_captured_acknowledged_sequence IS NOT NULL AND reclaim_captured_acknowledged_sequence>=0 AND reclaim_captured_acknowledged_sequence<reclaim_command_sequence AND reclaim_old_after_sequence IS NOT NULL AND reclaim_old_after_sequence>=0 AND reclaim_old_after_sequence<=reclaim_captured_acknowledged_sequence AND reclaim_removed_variable_bytes IS NOT NULL AND reclaim_removed_variable_bytes>=0 AND reclaim_result_after_sequence IS NOT NULL AND reclaim_result_after_sequence>=reclaim_old_after_sequence AND reclaim_result_after_sequence<=reclaim_captured_acknowledged_sequence AND reclaim_result_no_next IS NOT NULL AND reclaim_result_no_next IN(0,1) AND reclaim_applied_ms IS NOT NULL AND reclaim_applied_ms>=0 AND
 ((reclaim_result_count=0 AND reclaim_removed_sequence IS NULL AND reclaim_removed_variable_bytes=0 AND reclaim_result_after_sequence=reclaim_old_after_sequence AND reclaim_result_no_next=1) OR (reclaim_result_count=1 AND reclaim_removed_sequence IS NOT NULL AND reclaim_removed_sequence>reclaim_old_after_sequence AND reclaim_removed_sequence<=reclaim_captured_acknowledged_sequence AND reclaim_removed_variable_bytes>0 AND reclaim_result_after_sequence=reclaim_removed_sequence))))
) STRICT;

CREATE TABLE user_mutation_command_receipts(
 root_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 operation_id BLOB NOT NULL CHECK(length(operation_id)=16),
 command_kind INTEGER NOT NULL CHECK(command_kind BETWEEN 100 AND 899),
 request_digest BLOB NOT NULL CHECK(length(request_digest)=32),
 result_version INTEGER NOT NULL CHECK(result_version=1),
 result_count INTEGER NOT NULL CHECK(result_count BETWEEN 0 AND 10000),
 result_digest BLOB NOT NULL CHECK(length(result_digest)=32),
 result_payload BLOB NOT NULL CHECK(length(result_payload) BETWEEN 1 AND 65536),
 applied_ms INTEGER NOT NULL CHECK(applied_ms>=0),
 variable_bytes INTEGER GENERATED ALWAYS AS(length(CAST(root_id AS BLOB))+length(command_incarnation)+length(operation_id)+length(request_digest)+length(result_digest)+length(result_payload)) VIRTUAL,
 PRIMARY KEY(root_id,command_incarnation,command_sequence),
 UNIQUE(root_id,command_incarnation,operation_id),
 FOREIGN KEY(root_id,command_incarnation) REFERENCES user_mutation_command_owners(root_id,command_incarnation) ON DELETE NO ACTION ON UPDATE NO ACTION
) STRICT, WITHOUT ROWID;

CREATE INDEX user_mutation_command_reclaim_due ON user_mutation_command_owners(receipt_reclaim_due_ms,root_id) WHERE receipt_reclaim_due_ms IS NOT NULL;
