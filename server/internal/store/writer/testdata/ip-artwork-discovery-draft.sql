-- Exact E artwork discovery subset from PACKET-IP, 8 November 2026.
-- Proposed fixture only; group E is not embedded and this makes no Capture,
-- adoption or schema-SHA claim. The subject-job table is recreated with its
-- embedded columns plus the draft command/receipt counters so the disposable
-- probe matches the amended CREATE. Discovery stages/candidates and the closed
-- artwork receipt table are the frozen E proposal. The material-intent stub is
-- only the receipt foreign-key target; IP-18 never writes it.
DROP TABLE artwork_subject_jobs;

CREATE TABLE artwork_subject_jobs(
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 entity_id INTEGER REFERENCES catalog_subjects(id),
 person_id INTEGER REFERENCES catalog_people(id),
 source_incarnation BLOB NOT NULL CHECK(length(source_incarnation)=16),
 source_revision INTEGER NOT NULL CHECK(source_revision>0),
 phase TEXT NOT NULL CHECK(phase IN('ready','providers','publish','retire')),
 after_provider TEXT NOT NULL DEFAULT '',
 error_code TEXT NOT NULL DEFAULT '',
 job_revision INTEGER NOT NULL DEFAULT 1 CHECK(job_revision>0),
 job_lease_owner INTEGER NOT NULL DEFAULT 0 CHECK(job_lease_owner>=0),
 job_lease_generation INTEGER NOT NULL DEFAULT 0 CHECK(job_lease_generation>=0),
 job_lease_expires_ms INTEGER NOT NULL DEFAULT 0 CHECK(job_lease_expires_ms>=0),
 job_attempts INTEGER NOT NULL DEFAULT 0 CHECK(job_attempts>=0),
 job_next_attempt_ms INTEGER,
 job_due_ms INTEGER GENERATED ALWAYS AS(CASE WHEN job_next_attempt_ms IS NULL THEN NULL ELSE max(job_next_attempt_ms,job_lease_expires_ms) END) VIRTUAL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 last_command_sequence INTEGER NOT NULL DEFAULT 0 CHECK(last_command_sequence>=0),
 acknowledged_command_sequence INTEGER NOT NULL DEFAULT 0 CHECK(acknowledged_command_sequence>=0),
 receipt_reclaim_after_sequence INTEGER NOT NULL DEFAULT 0 CHECK(receipt_reclaim_after_sequence>=0),
 receipt_reclaim_due_ms INTEGER CHECK(receipt_reclaim_due_ms IS NULL OR receipt_reclaim_due_ms>=0),
 UNIQUE(id,command_incarnation),
 UNIQUE(entity_id),
 UNIQUE(person_id),
 CHECK(receipt_reclaim_after_sequence<=acknowledged_command_sequence AND acknowledged_command_sequence<=last_command_sequence),
 CHECK((entity_id IS NULL)<>(person_id IS NULL))
) STRICT;

CREATE INDEX artwork_subject_due ON artwork_subject_jobs(job_due_ms,id) WHERE job_due_ms IS NOT NULL;
CREATE INDEX artwork_subject_receipt_due ON artwork_subject_jobs(receipt_reclaim_due_ms,id) WHERE receipt_reclaim_due_ms IS NOT NULL;

CREATE TABLE artwork_material_intents(
 id TEXT PRIMARY KEY NOT NULL,
 owner_id INTEGER NOT NULL,
 owner_incarnation BLOB NOT NULL CHECK(length(owner_incarnation)=16),
 UNIQUE(id,owner_id,owner_incarnation)
);

CREATE TABLE artwork_discovery_stages(
 id INTEGER PRIMARY KEY AUTOINCREMENT,
 owner_id INTEGER NOT NULL,
 owner_incarnation BLOB NOT NULL CHECK(length(owner_incarnation)=16),
 provider TEXT NOT NULL REFERENCES metadata_providers(code),
 source_incarnation BLOB NOT NULL CHECK(length(source_incarnation)=16),
 source_revision INTEGER NOT NULL CHECK(source_revision>0),
 policy_revision INTEGER NOT NULL CHECK(policy_revision>0),
 effective_language TEXT NOT NULL,
 generation INTEGER NOT NULL CHECK(generation>0),
 revision INTEGER NOT NULL CHECK(revision>0),
 state TEXT NOT NULL CHECK(state IN('building','ready','retiring','done')),
 expected_count INTEGER NOT NULL CHECK(expected_count>=0),
 expected_bytes INTEGER NOT NULL CHECK(expected_bytes>=0),
 manifest_algorithm TEXT NOT NULL,
 manifest_digest BLOB NOT NULL CHECK(length(manifest_digest)=32),
 staged_count INTEGER NOT NULL DEFAULT 0 CHECK(staged_count>=0),
 staged_bytes INTEGER NOT NULL DEFAULT 0 CHECK(staged_bytes>=0),
 reclaim_after_ordinal INTEGER NOT NULL DEFAULT -1 CHECK(reclaim_after_ordinal>=-1),
 reclaim_due_ms INTEGER CHECK(reclaim_due_ms IS NULL OR reclaim_due_ms>=0),
 FOREIGN KEY(owner_id,owner_incarnation) REFERENCES artwork_subject_jobs(id,command_incarnation),
 UNIQUE(id,owner_id,owner_incarnation),
 CHECK(staged_count<=expected_count AND staged_bytes<=expected_bytes),
 CHECK(state<>'ready' OR (staged_count=expected_count AND staged_bytes=expected_bytes)),
 CHECK(typeof(id) IN('null','integer')),
 CHECK(typeof(owner_id) IN('null','integer')),
 CHECK(typeof(owner_incarnation) IN('null','blob')),
 CHECK(typeof(provider) IN('null','text')),
 CHECK(typeof(source_incarnation) IN('null','blob')),
 CHECK(typeof(source_revision) IN('null','integer')),
 CHECK(typeof(policy_revision) IN('null','integer')),
 CHECK(typeof(effective_language) IN('null','text')),
 CHECK(typeof(generation) IN('null','integer')),
 CHECK(typeof(revision) IN('null','integer')),
 CHECK(typeof(state) IN('null','text')),
 CHECK(typeof(expected_count) IN('null','integer')),
 CHECK(typeof(expected_bytes) IN('null','integer')),
 CHECK(typeof(manifest_algorithm) IN('null','text')),
 CHECK(typeof(manifest_digest) IN('null','blob')),
 CHECK(typeof(staged_count) IN('null','integer')),
 CHECK(typeof(staged_bytes) IN('null','integer')),
 CHECK(typeof(reclaim_after_ordinal) IN('null','integer')),
 CHECK(typeof(reclaim_due_ms) IN('null','integer'))
);

CREATE INDEX artwork_discovery_stage_owner ON artwork_discovery_stages(owner_id,owner_incarnation,id);
CREATE INDEX artwork_discovery_stage_provider ON artwork_discovery_stages(provider,id);
CREATE INDEX artwork_discovery_stage_reclaim ON artwork_discovery_stages(reclaim_due_ms,id) WHERE reclaim_due_ms IS NOT NULL;

CREATE TABLE artwork_discovery_candidates(
 stage_id INTEGER NOT NULL REFERENCES artwork_discovery_stages(id),
 ordinal INTEGER NOT NULL CHECK(ordinal>=0),
 role TEXT NOT NULL CHECK(length(role)>0 AND role=lower(trim(role)) AND role NOT IN('thumb','thumbnail')),
 source_ordinal INTEGER NOT NULL CHECK(source_ordinal>=0),
 image_id TEXT NOT NULL,
 origin TEXT NOT NULL,
 locale TEXT NOT NULL,
 attribution TEXT NOT NULL,
 score REAL NOT NULL CHECK(score BETWEEN -1.7976931348623157e308 AND 1.7976931348623157e308),
 PRIMARY KEY(stage_id,ordinal),
 UNIQUE(stage_id,role,image_id),
 UNIQUE(stage_id,role,source_ordinal),
 CHECK(typeof(stage_id) IN('null','integer')),
 CHECK(typeof(ordinal) IN('null','integer')),
 CHECK(typeof(role) IN('null','text')),
 CHECK(typeof(source_ordinal) IN('null','integer')),
 CHECK(typeof(image_id) IN('null','text')),
 CHECK(typeof(origin) IN('null','text')),
 CHECK(typeof(locale) IN('null','text')),
 CHECK(typeof(attribution) IN('null','text')),
 CHECK(typeof(score) IN('null','integer','real'))
) WITHOUT ROWID;

CREATE TABLE artwork_command_receipts(
 owner_id INTEGER NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 operation_id BLOB NOT NULL UNIQUE CHECK(length(operation_id)=16),
 request_digest BLOB NOT NULL CHECK(length(request_digest)=32),
 applied_ms INTEGER NOT NULL CHECK(applied_ms>=0),
 result_digest BLOB NOT NULL CHECK(length(result_digest)=32),
 command_kind TEXT NOT NULL CHECK(command_kind IN('discovery_claim','discovery_page','discovery_finish','job_claim','job_renew','material_publish','select','clear','upload_accept','gc_mark','gc_finish','reencode','abandoned_reclaim')),
 accepted_revision INTEGER NOT NULL CHECK(accepted_revision>0),
 result_count INTEGER NOT NULL DEFAULT 0 CHECK(result_count>=0),
 result_bytes INTEGER NOT NULL DEFAULT 0 CHECK(result_bytes>=0),
 result_complete INTEGER NOT NULL DEFAULT 0 CHECK(result_complete IN(0,1)),
 after_ordinal INTEGER NOT NULL DEFAULT 0 CHECK(after_ordinal>=0),
 lease_generation INTEGER NOT NULL DEFAULT 0 CHECK(lease_generation>=0),
 lease_until_ms INTEGER NOT NULL DEFAULT 0 CHECK(lease_until_ms>=0),
 stage_id INTEGER,
 material_intent_id TEXT,
 role TEXT,
 choice TEXT CHECK(choice IS NULL OR choice IN('matched','locked')),
 selection_revision INTEGER CHECK(selection_revision IS NULL OR selection_revision>0),
 selected_digest TEXT REFERENCES artwork_sources(digest),
 previous_digest TEXT REFERENCES artwork_sources(digest),
 blob_digest TEXT,
 retirement_token BLOB CHECK(retirement_token IS NULL OR length(retirement_token)=16),
 storage_revision INTEGER CHECK(storage_revision IS NULL OR storage_revision>0),
 history_id INTEGER REFERENCES metadata_owner_history(id),
 PRIMARY KEY(owner_id,command_incarnation,command_sequence),
 FOREIGN KEY(owner_id,command_incarnation) REFERENCES artwork_subject_jobs(id,command_incarnation),
 FOREIGN KEY(stage_id,owner_id,command_incarnation) REFERENCES artwork_discovery_stages(id,owner_id,owner_incarnation),
 FOREIGN KEY(material_intent_id,owner_id,command_incarnation) REFERENCES artwork_material_intents(id,owner_id,owner_incarnation),
 CHECK((role IS NULL)=(choice IS NULL) AND (role IS NULL)=(selection_revision IS NULL)),
 CHECK(command_kind NOT IN('select','clear','upload_accept') OR role IS NOT NULL),
 CHECK(command_kind<>'select' OR selected_digest IS NOT NULL OR choice='locked'),
 CHECK(command_kind<>'gc_mark' OR (retirement_token IS NOT NULL AND storage_revision IS NOT NULL)),
 CHECK(typeof(owner_id) IN('null','integer')),
 CHECK(typeof(command_incarnation) IN('null','blob')),
 CHECK(typeof(command_sequence) IN('null','integer')),
 CHECK(typeof(operation_id) IN('null','blob')),
 CHECK(typeof(request_digest) IN('null','blob')),
 CHECK(typeof(applied_ms) IN('null','integer')),
 CHECK(typeof(result_digest) IN('null','blob')),
 CHECK(typeof(command_kind) IN('null','text')),
 CHECK(typeof(accepted_revision) IN('null','integer')),
 CHECK(typeof(result_count) IN('null','integer')),
 CHECK(typeof(result_bytes) IN('null','integer')),
 CHECK(typeof(result_complete) IN('null','integer')),
 CHECK(typeof(after_ordinal) IN('null','integer')),
 CHECK(typeof(lease_generation) IN('null','integer')),
 CHECK(typeof(lease_until_ms) IN('null','integer')),
 CHECK(typeof(stage_id) IN('null','integer')),
 CHECK(typeof(material_intent_id) IN('null','text')),
 CHECK(typeof(role) IN('null','text')),
 CHECK(typeof(choice) IN('null','text')),
 CHECK(typeof(selection_revision) IN('null','integer')),
 CHECK(typeof(selected_digest) IN('null','text')),
 CHECK(typeof(previous_digest) IN('null','text')),
 CHECK(typeof(blob_digest) IN('null','text')),
 CHECK(typeof(retirement_token) IN('null','blob')),
 CHECK(typeof(storage_revision) IN('null','integer')),
 CHECK(typeof(history_id) IN('null','integer'))
) WITHOUT ROWID;

CREATE INDEX artwork_receipt_stage_id ON artwork_command_receipts(stage_id,owner_id) WHERE stage_id IS NOT NULL;
