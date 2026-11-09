-- Exact D subset needed by IP-14's person operation claim/renew/finish and
-- person evidence BEGIN, 8 November 2026. Proposed fixture only; group D is
-- not embedded and this makes no Capture claim. The embedded schema owns
-- catalog_people, catalog_libraries, metadata_providers, provider subjects, the
-- provider policy and the generic metadata_evidence body. This adds the exact
-- person custody/receipt/build subset plus the typed exact-date evidence
-- carriers the final group-D proposal puts on metadata_evidence.
ALTER TABLE metadata_evidence ADD COLUMN birth_date TEXT NOT NULL DEFAULT '' CHECK(typeof(birth_date)='text');
ALTER TABLE metadata_evidence ADD COLUMN death_date TEXT NOT NULL DEFAULT '' CHECK(typeof(death_date)='text');
ALTER TABLE metadata_evidence ADD COLUMN birth_place TEXT NOT NULL DEFAULT '' CHECK(typeof(birth_place)='text');

CREATE TABLE metadata_person_operations(
 id TEXT PRIMARY KEY NOT NULL,
 person_id INTEGER NOT NULL REFERENCES catalog_people(id) ON DELETE NO ACTION ON UPDATE NO ACTION,
 provider TEXT NOT NULL REFERENCES metadata_providers(code),
 library_id INTEGER NOT NULL REFERENCES catalog_libraries(id),
 subject_id INTEGER NOT NULL,
 subject_kind TEXT NOT NULL CHECK(subject_kind='person'),
 provider_id TEXT NOT NULL,
 language TEXT NOT NULL,
 region TEXT NOT NULL,
 request_shape TEXT NOT NULL,
 captured_person_token TEXT NOT NULL,
 captured_person_revision INTEGER NOT NULL CHECK(captured_person_revision>0),
 subject_incarnation BLOB NOT NULL CHECK(length(subject_incarnation)=16),
 subject_revision INTEGER NOT NULL CHECK(subject_revision>0),
 policy_incarnation BLOB NOT NULL CHECK(length(policy_incarnation)=16),
 policy_revision INTEGER NOT NULL CHECK(policy_revision>0),
 consent_revision INTEGER NOT NULL CHECK(consent_revision>0),
 generation INTEGER NOT NULL CHECK(generation>0),
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 phase TEXT NOT NULL CHECK(phase IN('claimed','building','sealed','accepted','stale','retiring','done')),
 lease_owner TEXT NOT NULL,
 lease_generation INTEGER NOT NULL CHECK(lease_generation>0),
 lease_until_ms INTEGER NOT NULL CHECK(lease_until_ms>=0),
 next_attempt_ms INTEGER CHECK(next_attempt_ms IS NULL OR next_attempt_ms>=0),
 job_due_ms INTEGER GENERATED ALWAYS AS(CASE WHEN next_attempt_ms IS NULL THEN NULL ELSE max(next_attempt_ms,lease_until_ms) END) VIRTUAL,
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
 UNIQUE(id,command_incarnation),
 UNIQUE(id,person_id,provider,provider_id),
 CHECK(receipt_reclaim_after_sequence<=acknowledged_command_sequence AND acknowledged_command_sequence<=last_command_sequence),
 FOREIGN KEY(subject_id,provider,subject_kind,provider_id,language,region,request_shape) REFERENCES metadata_provider_subjects(id,provider,subject_kind,provider_id,language,region,request_shape),
 CHECK(((reclaim_command_sequence IS NULL)=(reclaim_operation_id IS NULL)) AND ((reclaim_command_sequence IS NULL)=(reclaim_request_digest IS NULL)) AND ((reclaim_command_sequence IS NULL)=(reclaim_applied_ms IS NULL)) AND ((reclaim_command_sequence IS NULL)=(reclaim_result_digest IS NULL)) AND ((reclaim_command_sequence IS NULL)=(reclaim_captured_ack IS NULL)) AND ((reclaim_command_sequence IS NULL)=(reclaim_old_cursor IS NULL)) AND ((reclaim_command_sequence IS NULL)=(reclaim_removed_count IS NULL)) AND ((reclaim_command_sequence IS NULL)=(reclaim_removed_bytes IS NULL)) AND ((reclaim_command_sequence IS NULL)=(reclaim_result_cursor IS NULL)) AND ((reclaim_command_sequence IS NULL)=(reclaim_no_next IS NULL))),
 CHECK(reclaim_command_sequence IS NULL OR (reclaim_command_sequence<=last_command_sequence AND reclaim_command_sequence-reclaim_captured_ack=1 AND reclaim_captured_ack<=acknowledged_command_sequence AND reclaim_old_cursor<=reclaim_result_cursor AND reclaim_result_cursor<=reclaim_captured_ack AND receipt_reclaim_after_sequence=reclaim_result_cursor AND (reclaim_no_next=0 OR reclaim_result_cursor=reclaim_captured_ack) AND (reclaim_removed_count<>0 OR reclaim_removed_bytes=0))),
 CHECK(typeof(id) IN('null','text')),
 CHECK(typeof(person_id) IN('null','integer')),
 CHECK(typeof(provider) IN('null','text')),
 CHECK(typeof(library_id) IN('null','integer')),
 CHECK(typeof(subject_id) IN('null','integer')),
 CHECK(typeof(subject_kind) IN('null','text')),
 CHECK(typeof(provider_id) IN('null','text')),
 CHECK(typeof(language) IN('null','text')),
 CHECK(typeof(region) IN('null','text')),
 CHECK(typeof(request_shape) IN('null','text')),
 CHECK(typeof(captured_person_token) IN('null','text')),
 CHECK(typeof(captured_person_revision) IN('null','integer')),
 CHECK(typeof(subject_incarnation) IN('null','blob')),
 CHECK(typeof(subject_revision) IN('null','integer')),
 CHECK(typeof(policy_incarnation) IN('null','blob')),
 CHECK(typeof(policy_revision) IN('null','integer')),
 CHECK(typeof(consent_revision) IN('null','integer')),
 CHECK(typeof(generation) IN('null','integer')),
 CHECK(typeof(revision) IN('null','integer')),
 CHECK(typeof(phase) IN('null','text')),
 CHECK(typeof(lease_owner) IN('null','text')),
 CHECK(typeof(lease_generation) IN('null','integer')),
 CHECK(typeof(lease_until_ms) IN('null','integer')),
 CHECK(typeof(next_attempt_ms) IN('null','integer')),
 CHECK(typeof(job_due_ms) IN('null','integer')),
 CHECK(typeof(command_incarnation) IN('null','blob')),
 CHECK(typeof(last_command_sequence) IN('null','integer')),
 CHECK(typeof(acknowledged_command_sequence) IN('null','integer')),
 CHECK(typeof(receipt_reclaim_after_sequence) IN('null','integer')),
 CHECK(typeof(receipt_reclaim_due_ms) IN('null','integer')),
 CHECK(typeof(reclaim_command_sequence) IN('null','integer')),
 CHECK(typeof(reclaim_operation_id) IN('null','blob')),
 CHECK(typeof(reclaim_request_digest) IN('null','blob')),
 CHECK(typeof(reclaim_applied_ms) IN('null','integer')),
 CHECK(typeof(reclaim_result_digest) IN('null','blob')),
 CHECK(typeof(reclaim_captured_ack) IN('null','integer')),
 CHECK(typeof(reclaim_old_cursor) IN('null','integer')),
 CHECK(typeof(reclaim_removed_count) IN('null','integer')),
 CHECK(typeof(reclaim_removed_bytes) IN('null','integer')),
 CHECK(typeof(reclaim_result_cursor) IN('null','integer')),
 CHECK(typeof(reclaim_no_next) IN('null','integer'))
);

-- D: person disclosure/retirement and provider operation lookup.
CREATE INDEX metadata_person_operation_subject ON metadata_person_operations(person_id,provider,id);

-- D: indexed provider FK and bounded provider retirement.
CREATE INDEX metadata_person_operation_provider ON metadata_person_operations(provider,id);

-- D: retained policy-library FK, current scope and library retirement.
CREATE INDEX metadata_person_operation_library ON metadata_person_operations(library_id,id);

-- D: exact namespace FK lookup; immutable provider subject is not a person ID.
CREATE INDEX metadata_person_operation_namespace ON metadata_person_operations(subject_id,provider,subject_kind,provider_id,language,region,request_shape);

-- D: governor-ready person lease/retry keyset.
CREATE INDEX metadata_person_operation_due ON metadata_person_operations(job_due_ms,id) WHERE job_due_ms IS NOT NULL;

-- D: discoverable ACKed receipt debt.
CREATE INDEX metadata_person_operation_reclaim ON metadata_person_operations(receipt_reclaim_due_ms,id) WHERE receipt_reclaim_due_ms IS NOT NULL;

-- D: Exact person-provider command results and seal proof, retained across job/lease replacement.
CREATE TABLE metadata_person_receipts(
 owner_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 operation_id BLOB NOT NULL UNIQUE CHECK(length(operation_id)=16),
 request_digest BLOB NOT NULL CHECK(length(request_digest)=32),
 applied_ms INTEGER NOT NULL CHECK(applied_ms>=0),
 result_digest BLOB NOT NULL CHECK(length(result_digest)=32),
 command_kind TEXT NOT NULL CHECK(command_kind IN('begin','append','evidence_seal','accept','delta','renew','retry','reclaim')),
 accepted_revision INTEGER NOT NULL CHECK(accepted_revision>0),
 after_ordinal INTEGER NOT NULL CHECK(after_ordinal>=0),
 result_count INTEGER NOT NULL CHECK(result_count>=0),
 evidence_id TEXT,
 build_id TEXT,
 publication_id TEXT,
 seal_algorithm TEXT,
 seal_version INTEGER,
 seal_commitment BLOB,
 seal_rows INTEGER,
 seal_bytes INTEGER,
 reclaimed_rows INTEGER CHECK(reclaimed_rows IS NULL OR reclaimed_rows>=0),
 reclaimed_bytes INTEGER CHECK(reclaimed_bytes IS NULL OR reclaimed_bytes>=0),
 after_family INTEGER CHECK(after_family IS NULL OR after_family BETWEEN 0 AND 2),
 after_alias_ordinal INTEGER CHECK(after_alias_ordinal IS NULL OR after_alias_ordinal>=-1),
 after_external_provider TEXT,
 after_external_kind TEXT CHECK(after_external_kind IS NULL OR after_external_kind='person'),
 after_external_id TEXT,
 terminal INTEGER CHECK(terminal IS NULL OR terminal IN(0,1)),
 proof_pin_count INTEGER NOT NULL DEFAULT 0 CHECK(proof_pin_count>=0),
 proof_revision INTEGER NOT NULL DEFAULT 1 CHECK(proof_revision>0),
 CHECK((command_kind='reclaim')=(reclaimed_rows IS NOT NULL)),
 CHECK((command_kind='reclaim')=(reclaimed_bytes IS NOT NULL)),
 CHECK((command_kind='reclaim')=(after_family IS NOT NULL)),
 CHECK((command_kind='reclaim')=(after_alias_ordinal IS NOT NULL)),
 CHECK((command_kind='reclaim')=(terminal IS NOT NULL)),
 CHECK((after_external_provider IS NULL)=(after_external_kind IS NULL)),
 CHECK((after_external_provider IS NULL)=(after_external_id IS NULL)),
 CHECK(after_external_provider IS NULL OR (command_kind='reclaim' AND after_family>=1 AND length(after_external_provider)>0 AND length(after_external_id)>0)),
 CHECK(typeof(reclaimed_rows) IN('null','integer') AND typeof(reclaimed_bytes) IN('null','integer') AND typeof(after_family) IN('null','integer') AND typeof(after_alias_ordinal) IN('null','integer') AND typeof(terminal) IN('null','integer')),
 CHECK(typeof(after_external_provider) IN('null','text') AND typeof(after_external_kind) IN('null','text') AND typeof(after_external_id) IN('null','text')),
 PRIMARY KEY(owner_id,command_incarnation,command_sequence),
 UNIQUE(owner_id,command_incarnation,command_sequence,command_kind,evidence_id),
 FOREIGN KEY(owner_id,command_incarnation) REFERENCES metadata_person_operations(id,command_incarnation),
 FOREIGN KEY(evidence_id) REFERENCES metadata_evidence(id),
 CHECK((command_kind='evidence_seal')=(seal_algorithm IS NOT NULL)),
 CHECK((seal_algorithm IS NULL)=(seal_version IS NULL) AND (seal_algorithm IS NULL)=(seal_commitment IS NULL) AND (seal_algorithm IS NULL)=(seal_rows IS NULL) AND (seal_algorithm IS NULL)=(seal_bytes IS NULL)),
 CHECK(command_kind<>'evidence_seal' OR (evidence_id IS NOT NULL AND build_id IS NOT NULL AND seal_version>0 AND length(seal_algorithm)>0 AND length(seal_commitment)=32 AND seal_rows>0 AND seal_bytes>0)),
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
 CHECK(typeof(result_count) IN('null','integer')),
 CHECK(typeof(evidence_id) IN('null','text')),
 CHECK(typeof(build_id) IN('null','text')),
 CHECK(typeof(publication_id) IN('null','text')),
 CHECK(typeof(seal_algorithm) IN('null','text')),
 CHECK(typeof(seal_version) IN('null','integer')),
 CHECK(typeof(seal_commitment) IN('null','blob')),
 CHECK(typeof(seal_rows) IN('null','integer')),
 CHECK(typeof(seal_bytes) IN('null','integer')),
 CHECK(typeof(proof_pin_count) IN('null','integer')),
 CHECK(typeof(proof_revision) IN('null','integer'))
) WITHOUT ROWID;

-- D: evidence FK and proof retention lookup.
CREATE INDEX metadata_person_receipt_evidence ON metadata_person_receipts(evidence_id,owner_id) WHERE evidence_id IS NOT NULL;

-- D: Person-owned construction and seal link over the SAME generic evidence body; different owner from EN builds.
CREATE TABLE metadata_person_builds(
 id TEXT PRIMARY KEY NOT NULL,
 evidence_id TEXT NOT NULL UNIQUE,
 provider TEXT NOT NULL,
 subject_kind TEXT NOT NULL CHECK(subject_kind='person'),
 provider_id TEXT NOT NULL,
 owner_id TEXT NOT NULL,
 owner_incarnation BLOB NOT NULL CHECK(length(owner_incarnation)=16),
 owner_generation INTEGER NOT NULL CHECK(owner_generation>0),
 generation INTEGER NOT NULL CHECK(generation>0),
 content_revision INTEGER NOT NULL CHECK(content_revision>0),
 state TEXT NOT NULL CHECK(state IN('building','ready','retiring','done')),
 cursor INTEGER NOT NULL DEFAULT 0 CHECK(cursor>=0),
 expected_rows INTEGER NOT NULL CHECK(expected_rows>0),
 expected_bytes INTEGER NOT NULL CHECK(expected_bytes>0),
 expected_algorithm TEXT NOT NULL,
 expected_commitment BLOB NOT NULL CHECK(length(expected_commitment)=32),
 staged_rows INTEGER NOT NULL DEFAULT 0 CHECK(staged_rows>=0),
 staged_bytes INTEGER NOT NULL DEFAULT 0 CHECK(staged_bytes>=0),
 materialized_charge INTEGER NOT NULL DEFAULT 0 CHECK(materialized_charge>=0),
 seal_owner TEXT,
 seal_incarnation BLOB CHECK(seal_incarnation IS NULL OR length(seal_incarnation)=16),
 seal_sequence INTEGER CHECK(seal_sequence IS NULL OR seal_sequence>0),
 seal_kind TEXT GENERATED ALWAYS AS(CASE WHEN seal_owner IS NOT NULL THEN 'evidence_seal' END) VIRTUAL,
 reclaim_after_family INTEGER NOT NULL DEFAULT 0 CHECK(reclaim_after_family>=0),
 reclaim_after_ordinal INTEGER NOT NULL DEFAULT 0 CHECK(reclaim_after_ordinal>=0),
 reclaim_due_ms INTEGER CHECK(reclaim_due_ms IS NULL OR reclaim_due_ms>=0),
 FOREIGN KEY(evidence_id,provider,subject_kind,provider_id) REFERENCES metadata_evidence(id,provider,subject_kind,provider_id),
 FOREIGN KEY(owner_id,owner_incarnation) REFERENCES metadata_person_operations(id,command_incarnation),
 FOREIGN KEY(seal_owner,seal_incarnation,seal_sequence,seal_kind,evidence_id) REFERENCES metadata_person_receipts(owner_id,command_incarnation,command_sequence,command_kind,evidence_id),
 CHECK(staged_rows<=expected_rows AND staged_bytes<=expected_bytes AND cursor=staged_rows),
 CHECK((seal_owner IS NULL)=(seal_incarnation IS NULL) AND (seal_owner IS NULL)=(seal_sequence IS NULL)),
 CHECK(state<>'ready' OR (seal_owner IS NOT NULL AND staged_rows=expected_rows AND staged_bytes=expected_bytes)),
 CHECK(typeof(id) IN('null','text')),
 CHECK(typeof(evidence_id) IN('null','text')),
 CHECK(typeof(provider) IN('null','text')),
 CHECK(typeof(subject_kind) IN('null','text')),
 CHECK(typeof(provider_id) IN('null','text')),
 CHECK(typeof(owner_id) IN('null','text')),
 CHECK(typeof(owner_incarnation) IN('null','blob')),
 CHECK(typeof(owner_generation) IN('null','integer')),
 CHECK(typeof(generation) IN('null','integer')),
 CHECK(typeof(content_revision) IN('null','integer')),
 CHECK(typeof(state) IN('null','text')),
 CHECK(typeof(cursor) IN('null','integer')),
 CHECK(typeof(expected_rows) IN('null','integer')),
 CHECK(typeof(expected_bytes) IN('null','integer')),
 CHECK(typeof(expected_algorithm) IN('null','text')),
 CHECK(typeof(expected_commitment) IN('null','blob')),
 CHECK(typeof(staged_rows) IN('null','integer')),
 CHECK(typeof(staged_bytes) IN('null','integer')),
 CHECK(typeof(materialized_charge) IN('null','integer')),
 CHECK(typeof(seal_owner) IN('null','text')),
 CHECK(typeof(seal_incarnation) IN('null','blob')),
 CHECK(typeof(seal_sequence) IN('null','integer')),
 CHECK(typeof(seal_kind) IN('null','text')),
 CHECK(typeof(reclaim_after_family) IN('null','integer')),
 CHECK(typeof(reclaim_after_ordinal) IN('null','integer')),
 CHECK(typeof(reclaim_due_ms) IN('null','integer'))
);

-- D: exactly one immutable build per operation/incarnation; unique-key recovery survives BEGIN receipt retirement.
CREATE UNIQUE INDEX metadata_person_build_owner ON metadata_person_builds(owner_id,owner_incarnation);

-- D: pinned seal FK and downstream-first proof reclaim.
CREATE INDEX metadata_person_build_proof ON metadata_person_builds(seal_owner,seal_incarnation,seal_sequence,seal_kind,evidence_id);

-- D: staged/abandoned evidence remains charged until actual deletion.
CREATE INDEX metadata_person_build_reclaim ON metadata_person_builds(reclaim_due_ms,id) WHERE reclaim_due_ms IS NOT NULL;
