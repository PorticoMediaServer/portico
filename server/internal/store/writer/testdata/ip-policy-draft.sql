-- Exact D provider-policy / durable-sweep subset from PACKET-IP, 8 November 2026.
-- Proposed fixture only; no native schema adoption and no Capture claim. The
-- embedded schema still owns metadata_provider_policy without command columns,
-- so this disposable clone replaces that one table for the native probe.
DROP TABLE metadata_provider_policy;

CREATE TABLE metadata_provider_policy(
 library_id INTEGER NOT NULL REFERENCES catalog_libraries(id),
 provider TEXT NOT NULL REFERENCES metadata_providers(code),
 incarnation BLOB NOT NULL CHECK(length(incarnation)=16),
 revision INTEGER NOT NULL CHECK(revision>0),
 enabled INTEGER NOT NULL CHECK(enabled IN(0,1)),
 priority INTEGER NOT NULL,
 region TEXT NOT NULL,
 algorithm TEXT NOT NULL CHECK(length(algorithm)>0),
 refresh_mode TEXT NOT NULL CHECK(refresh_mode IN('fill_missing','replace_unlocked')),
 credit_enabled INTEGER NOT NULL DEFAULT 1 CHECK(credit_enabled IN(0,1)),
 credit_after_item INTEGER,
 credit_after_provider TEXT,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 last_command_sequence INTEGER NOT NULL DEFAULT 0 CHECK(last_command_sequence>=0),
 acknowledged_command_sequence INTEGER NOT NULL DEFAULT 0 CHECK(acknowledged_command_sequence>=0),
 receipt_reclaim_after_sequence INTEGER NOT NULL DEFAULT 0 CHECK(receipt_reclaim_after_sequence>=0),
 receipt_reclaim_due_ms INTEGER CHECK(receipt_reclaim_due_ms IS NULL OR receipt_reclaim_due_ms>=0),
 PRIMARY KEY(library_id,provider)
) WITHOUT ROWID;

CREATE UNIQUE INDEX metadata_policy_command_owner ON metadata_provider_policy(library_id,provider,command_incarnation);
CREATE INDEX metadata_policy_reclaim ON metadata_provider_policy(receipt_reclaim_due_ms,library_id,provider) WHERE receipt_reclaim_due_ms IS NOT NULL;

CREATE TABLE metadata_policy_receipts(
 library_id INTEGER NOT NULL,
 provider TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 operation_id BLOB NOT NULL UNIQUE CHECK(length(operation_id)=16),
 request_digest BLOB NOT NULL CHECK(length(request_digest)=32),
 applied_ms INTEGER NOT NULL CHECK(applied_ms>=0),
 result_digest BLOB NOT NULL CHECK(length(result_digest)=32),
 policy_incarnation BLOB NOT NULL CHECK(length(policy_incarnation)=16),
 accepted_revision INTEGER NOT NULL CHECK(accepted_revision>0),
 enabled INTEGER NOT NULL CHECK(enabled IN(0,1)),
 priority INTEGER NOT NULL,
 region TEXT NOT NULL,
 algorithm TEXT NOT NULL,
 refresh_mode TEXT NOT NULL CHECK(refresh_mode IN('fill_missing','replace_unlocked')),
 credit_enabled INTEGER NOT NULL CHECK(credit_enabled IN(0,1)),
 credit_after_item INTEGER,
 credit_after_provider TEXT,
 PRIMARY KEY(library_id,provider,command_incarnation,command_sequence),
 FOREIGN KEY(library_id,provider,command_incarnation) REFERENCES metadata_provider_policy(library_id,provider,command_incarnation),
 CHECK((credit_after_item IS NULL)=(credit_after_provider IS NULL))
) WITHOUT ROWID;

CREATE TABLE metadata_refresh_sweeps(
 provider TEXT NOT NULL REFERENCES metadata_providers(code),
 purpose TEXT NOT NULL CHECK(purpose IN('metadata','artwork')),
 generation INTEGER NOT NULL CHECK(generation>0),
 revision INTEGER NOT NULL CHECK(revision>0),
 epoch_ms INTEGER NOT NULL CHECK(epoch_ms>=0),
 phase TEXT NOT NULL CHECK(phase IN('entities','people','complete')),
 after_id INTEGER NOT NULL DEFAULT 0 CHECK(after_id>=0),
 page_from_id INTEGER NOT NULL DEFAULT 0 CHECK(page_from_id>=0),
 page_until_id INTEGER NOT NULL DEFAULT 0 CHECK(page_until_id>=page_from_id),
 page_waiting INTEGER NOT NULL DEFAULT 0 CHECK(page_waiting IN(0,1)),
 job_lease_owner INTEGER NOT NULL DEFAULT 0 CHECK(job_lease_owner>=0),
 job_lease_generation INTEGER NOT NULL DEFAULT 0 CHECK(job_lease_generation>=0),
 job_lease_expires_ms INTEGER NOT NULL DEFAULT 0 CHECK(job_lease_expires_ms>=0),
 job_next_attempt_ms INTEGER CHECK(job_next_attempt_ms IS NULL OR job_next_attempt_ms>=0),
 job_due_ms INTEGER GENERATED ALWAYS AS(CASE WHEN job_next_attempt_ms IS NULL THEN NULL ELSE max(job_next_attempt_ms,job_lease_expires_ms) END) VIRTUAL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 last_command_sequence INTEGER NOT NULL DEFAULT 0 CHECK(last_command_sequence>=0),
 acknowledged_command_sequence INTEGER NOT NULL DEFAULT 0 CHECK(acknowledged_command_sequence>=0),
 receipt_reclaim_after_sequence INTEGER NOT NULL DEFAULT 0 CHECK(receipt_reclaim_after_sequence>=0),
 receipt_reclaim_due_ms INTEGER CHECK(receipt_reclaim_due_ms IS NULL OR receipt_reclaim_due_ms>=0),
 reclaim_command_sequence INTEGER CHECK(reclaim_command_sequence IS NULL OR reclaim_command_sequence>0),
 reclaim_operation_id BLOB CHECK(reclaim_operation_id IS NULL OR length(reclaim_operation_id)=16),
 reclaim_request_digest BLOB CHECK(reclaim_request_digest IS NULL OR length(reclaim_request_digest)=32),
 reclaim_applied_ms INTEGER CHECK(reclaim_applied_ms IS NULL OR reclaim_applied_ms>=0),
 reclaim_result_digest BLOB CHECK(reclaim_result_digest IS NULL OR length(reclaim_result_digest)=32),
 reclaim_captured_ack INTEGER CHECK(reclaim_captured_ack IS NULL OR reclaim_captured_ack>=0),
 reclaim_old_cursor INTEGER CHECK(reclaim_old_cursor IS NULL OR reclaim_old_cursor>=0),
 reclaim_removed_count INTEGER CHECK(reclaim_removed_count IS NULL OR reclaim_removed_count>=0),
 reclaim_removed_bytes INTEGER CHECK(reclaim_removed_bytes IS NULL OR reclaim_removed_bytes>=0),
 reclaim_result_cursor INTEGER CHECK(reclaim_result_cursor IS NULL OR reclaim_result_cursor>=0),
 reclaim_no_next INTEGER CHECK(reclaim_no_next IS NULL OR reclaim_no_next IN(0,1)),
 PRIMARY KEY(provider,purpose),
 UNIQUE(provider,purpose,command_incarnation),
 CHECK(receipt_reclaim_after_sequence<=acknowledged_command_sequence AND acknowledged_command_sequence<=last_command_sequence)
) WITHOUT ROWID;

CREATE INDEX metadata_refresh_sweep_due ON metadata_refresh_sweeps(job_due_ms,provider,purpose) WHERE job_due_ms IS NOT NULL;
CREATE INDEX metadata_refresh_sweep_reclaim ON metadata_refresh_sweeps(receipt_reclaim_due_ms,provider,purpose) WHERE receipt_reclaim_due_ms IS NOT NULL;

CREATE TABLE metadata_refresh_receipts(
 provider TEXT NOT NULL,
 purpose TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 operation_id BLOB NOT NULL UNIQUE CHECK(length(operation_id)=16),
 request_digest BLOB NOT NULL CHECK(length(request_digest)=32),
 applied_ms INTEGER NOT NULL CHECK(applied_ms>=0),
 result_digest BLOB NOT NULL CHECK(length(result_digest)=32),
 command_kind TEXT NOT NULL CHECK(command_kind IN('request','page','finish')),
 accepted_revision INTEGER NOT NULL CHECK(accepted_revision>0),
 generation INTEGER NOT NULL CHECK(generation>0),
 epoch_ms INTEGER NOT NULL CHECK(epoch_ms>=0),
 phase TEXT NOT NULL CHECK(phase IN('entities','people','complete')),
 after_id INTEGER NOT NULL CHECK(after_id>=0),
 scheduled_count INTEGER NOT NULL CHECK(scheduled_count>=0),
 page_waiting INTEGER NOT NULL CHECK(page_waiting IN(0,1)),
 PRIMARY KEY(provider,purpose,command_incarnation,command_sequence),
 FOREIGN KEY(provider,purpose,command_incarnation) REFERENCES metadata_refresh_sweeps(provider,purpose,command_incarnation)
) WITHOUT ROWID;
