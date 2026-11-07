package writer

// Exact selected declarations from MG-001 candidate 4e0751989357d1aed85467ba81f5c24ce79aa3a26f947e0fd222070891bb9262.
// Empty disposable canonical fixture only; no adoption or production migration.
const mgLCDraftSQL = `CREATE TABLE lc_channels(
 id TEXT PRIMARY KEY,
 revision INTEGER NOT NULL,
 config_json TEXT NOT NULL,
 enabled INTEGER NOT NULL,
 position INTEGER NOT NULL,
 name TEXT NOT NULL,
 active_generation TEXT NOT NULL DEFAULT '',
 state TEXT NOT NULL,
 health_code TEXT NOT NULL DEFAULT '',
 generated_through_ms INTEGER NOT NULL DEFAULT 0,
 removed INTEGER NOT NULL DEFAULT 0,
 active_generation_ref TEXT GENERATED ALWAYS AS (NULLIF(active_generation,'')) STORED,
 FOREIGN KEY(active_generation_ref,id) REFERENCES lc_generations(id,channel_id)
) STRICT;

CREATE TABLE lc_generations(
 id TEXT PRIMARY KEY,
 channel_id TEXT NOT NULL REFERENCES lc_channels(id),
 config_revision INTEGER NOT NULL,
 config_json TEXT NOT NULL,
 catalog_fence TEXT NOT NULL,
 seed TEXT NOT NULL,
 status TEXT NOT NULL,
 phase TEXT NOT NULL,
 base_generation TEXT NOT NULL,
 start_ms INTEGER NOT NULL,
 end_ms INTEGER NOT NULL,
 boundary_ms INTEGER NOT NULL,
 copy_after_ms INTEGER NOT NULL DEFAULT -1,
 scan_rule INTEGER NOT NULL DEFAULT 0,
 scan_after TEXT NOT NULL DEFAULT '',
 cursor_ms INTEGER NOT NULL,
 candidate_count INTEGER NOT NULL DEFAULT 0,
 unresolved_count INTEGER NOT NULL DEFAULT 0,
 lease_token TEXT NOT NULL DEFAULT '',
 lease_until_ms INTEGER NOT NULL DEFAULT 0,
 created_ms INTEGER NOT NULL,
 published_ms INTEGER NOT NULL DEFAULT 0,
 error_code TEXT NOT NULL DEFAULT '',
 play_history_fence INTEGER NOT NULL DEFAULT 0 CHECK(play_history_fence>=0),
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 create_operation_id BLOB NOT NULL CHECK(length(create_operation_id)=16),
 birth_request_digest BLOB NOT NULL CHECK(length(birth_request_digest)=32),
 last_command_sequence INTEGER NOT NULL DEFAULT 1 CHECK(last_command_sequence>=1),
 acknowledged_command_sequence INTEGER NOT NULL DEFAULT 0 CHECK(acknowledged_command_sequence>=0 AND acknowledged_command_sequence<=last_command_sequence),
 stage_revision INTEGER NOT NULL DEFAULT 1 CHECK(stage_revision>0),
 staged_bytes INTEGER NOT NULL DEFAULT 0 CHECK(staged_bytes>=0),
 entry_count INTEGER NOT NULL DEFAULT 0 CHECK(entry_count>=0),
 entry_digest BLOB NOT NULL CHECK(length(entry_digest)=32),
 verified_entry_count INTEGER NOT NULL DEFAULT 0 CHECK(verified_entry_count>=0 AND verified_entry_count<=entry_count),
 verify_after_ms INTEGER NOT NULL DEFAULT -1 CHECK(verify_after_ms>=-1),
 verify_end_ms INTEGER NOT NULL CHECK(verify_end_ms>=start_ms),
 verified_entry_digest BLOB NOT NULL CHECK(length(verified_entry_digest)=32),
 schedule_sealed INTEGER NOT NULL DEFAULT 0 CHECK(schedule_sealed IN(0,1)),
 reclaim_family TEXT NOT NULL DEFAULT 'none' CHECK(reclaim_family IN('none','entries','candidates','rule_state','used','generation','complete')),
 reclaim_key TEXT NOT NULL DEFAULT '',
 reclaim_subkey INTEGER NOT NULL DEFAULT 0 CHECK(reclaim_subkey>=0),
 pending_base_generation TEXT GENERATED ALWAYS AS (CASE WHEN status='pending' THEN NULLIF(base_generation,'') ELSE NULL END) STORED,
 FOREIGN KEY(pending_base_generation,channel_id) REFERENCES lc_generations(id,channel_id),
 CONSTRAINT lc_schedule_seal CHECK(schedule_sealed=0 OR (entry_count>0 AND verified_entry_count=entry_count AND verify_end_ms=cursor_ms AND cursor_ms>=end_ms AND verified_entry_digest=entry_digest))
) STRICT;

CREATE TABLE lc_playback_refs(
 playback_id TEXT PRIMARY KEY,
 channel_id TEXT NOT NULL,
 generation_id TEXT NOT NULL,
 occurrence_id TEXT NOT NULL,
 end_ms INTEGER NOT NULL,
 lease_until_ms INTEGER NOT NULL,
 FOREIGN KEY(generation_id,channel_id) REFERENCES lc_generations(id,channel_id),
 FOREIGN KEY(generation_id,occurrence_id) REFERENCES lc_entries(generation_id,occurrence_id)
) STRICT;

CREATE TABLE lc_generation_command_receipts(
 generation_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 command_sequence INTEGER NOT NULL CHECK(command_sequence>0),
 operation_id BLOB NOT NULL CHECK(length(operation_id)=16),
 request_digest BLOB NOT NULL CHECK(length(request_digest)=32),
 result_digest BLOB NOT NULL CHECK(length(result_digest)=32),
 applied_ms INTEGER NOT NULL CHECK(applied_ms>=0),
 command_kind TEXT NOT NULL CHECK(command_kind IN('begin','claim','candidates','entries','verify','seal','publish','cancel','reclaim')),
 channel_id TEXT NOT NULL REFERENCES lc_channels(id),
 config_revision INTEGER NOT NULL CHECK(config_revision>0),
 catalog_fence TEXT NOT NULL,
 play_history_fence INTEGER NOT NULL CHECK(play_history_fence>=0),
 accepted_revision INTEGER NOT NULL CHECK(accepted_revision>0),
 accepted_acknowledged_sequence INTEGER NOT NULL CHECK(accepted_acknowledged_sequence>=0 AND accepted_acknowledged_sequence<command_sequence),
 result_candidate_count INTEGER NOT NULL CHECK(result_candidate_count>=0),
 result_entry_count INTEGER NOT NULL CHECK(result_entry_count>=0),
 result_cursor_ms INTEGER NOT NULL,
 result_staged_bytes INTEGER NOT NULL CHECK(result_staged_bytes>=0),
 result_entry_digest BLOB NOT NULL CHECK(length(result_entry_digest)=32),
 result_verified_entry_count INTEGER NOT NULL CHECK(result_verified_entry_count>=0 AND result_verified_entry_count<=result_entry_count),
 result_verify_after_ms INTEGER NOT NULL CHECK(result_verify_after_ms>=-1),
 result_verify_end_ms INTEGER NOT NULL,
 result_verified_entry_digest BLOB NOT NULL CHECK(length(result_verified_entry_digest)=32),
 result_sealed INTEGER NOT NULL CHECK(result_sealed IN(0,1)),
 result_phase TEXT NOT NULL,
 result_status TEXT NOT NULL CHECK(result_status IN('pending','published','superseded','failed','cancelled','retired')),
 result_lease_token TEXT NOT NULL,
 result_lease_until_ms INTEGER NOT NULL CHECK(result_lease_until_ms>=0),
 result_published_ms INTEGER NOT NULL CHECK(result_published_ms>=0),
 result_reclaim_family TEXT NOT NULL CHECK(result_reclaim_family IN('none','entries','candidates','rule_state','used','generation','complete')),
 result_reclaim_key TEXT NOT NULL DEFAULT '',
 result_reclaim_subkey INTEGER NOT NULL DEFAULT 0 CHECK(result_reclaim_subkey>=0),
 PRIMARY KEY(generation_id,command_incarnation,command_sequence),
 UNIQUE(generation_id,command_incarnation,operation_id),
 CONSTRAINT lc_receipt_publish CHECK(command_kind<>'publish' OR (result_sealed=1 AND result_status='published' AND result_entry_count>0 AND result_verified_entry_count=result_entry_count AND result_verified_entry_digest=result_entry_digest AND result_published_ms>0)),
 CHECK(typeof("generation_id") IN('null','text')),
 CHECK(typeof("command_incarnation") IN('null','blob')),
 CHECK(typeof("command_sequence") IN('null','integer')),
 CHECK(typeof("operation_id") IN('null','blob')),
 CHECK(typeof("request_digest") IN('null','blob')),
 CHECK(typeof("result_digest") IN('null','blob')),
 CHECK(typeof("applied_ms") IN('null','integer')),
 CHECK(typeof("command_kind") IN('null','text')),
 CHECK(typeof("channel_id") IN('null','text')),
 CHECK(typeof("config_revision") IN('null','integer')),
 CHECK(typeof("catalog_fence") IN('null','text')),
 CHECK(typeof("play_history_fence") IN('null','integer')),
 CHECK(typeof("accepted_revision") IN('null','integer')),
 CHECK(typeof("accepted_acknowledged_sequence") IN('null','integer')),
 CHECK(typeof("result_candidate_count") IN('null','integer')),
 CHECK(typeof("result_entry_count") IN('null','integer')),
 CHECK(typeof("result_cursor_ms") IN('null','integer')),
 CHECK(typeof("result_staged_bytes") IN('null','integer')),
 CHECK(typeof("result_entry_digest") IN('null','blob')),
 CHECK(typeof("result_verified_entry_count") IN('null','integer')),
 CHECK(typeof("result_verify_after_ms") IN('null','integer')),
 CHECK(typeof("result_verify_end_ms") IN('null','integer')),
 CHECK(typeof("result_verified_entry_digest") IN('null','blob')),
 CHECK(typeof("result_sealed") IN('null','integer')),
 CHECK(typeof("result_phase") IN('null','text')),
 CHECK(typeof("result_status") IN('null','text')),
 CHECK(typeof("result_lease_token") IN('null','text')),
 CHECK(typeof("result_lease_until_ms") IN('null','integer')),
 CHECK(typeof("result_published_ms") IN('null','integer')),
 CHECK(typeof("result_reclaim_family") IN('null','text')),
 CHECK(typeof("result_reclaim_key") IN('null','text')),
 CHECK(typeof("result_reclaim_subkey") IN('null','integer'))
) WITHOUT ROWID;

CREATE TABLE lc_generation_retirements(
 generation_id TEXT NOT NULL,
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 create_operation_id BLOB NOT NULL CHECK(length(create_operation_id)=16),
 birth_request_digest BLOB NOT NULL CHECK(length(birth_request_digest)=32),
 channel_id TEXT NOT NULL REFERENCES lc_channels(id),
 final_sequence INTEGER NOT NULL CHECK(final_sequence>0),
 final_result_digest BLOB NOT NULL CHECK(length(final_result_digest)=32),
 retired_ms INTEGER NOT NULL CHECK(retired_ms>=0),
 PRIMARY KEY(generation_id),
 CHECK(typeof("generation_id") IN('null','text')),
 CHECK(typeof("command_incarnation") IN('null','blob')),
 CHECK(typeof("create_operation_id") IN('null','blob')),
 CHECK(typeof("birth_request_digest") IN('null','blob')),
 CHECK(typeof("channel_id") IN('null','text')),
 CHECK(typeof("final_sequence") IN('null','integer')),
 CHECK(typeof("final_result_digest") IN('null','blob')),
 CHECK(typeof("retired_ms") IN('null','integer'))
) WITHOUT ROWID;

CREATE INDEX lc_directory ON lc_channels(enabled,removed,position,id);

CREATE INDEX lc_generations_channel_id_fk ON lc_generations(channel_id);

CREATE INDEX lc_jobs ON lc_generations(status,lease_until_ms,created_ms,id);

CREATE UNIQUE INDEX lc_one_job ON lc_generations(channel_id) WHERE status='pending';

CREATE INDEX lc_ref_channel ON lc_playback_refs(channel_id,lease_until_ms);

CREATE UNIQUE INDEX lc_generations_channel_scope ON lc_generations(id,channel_id);

CREATE INDEX lc_channels_generation_head ON lc_channels(active_generation_ref,id);

CREATE INDEX lc_generations_pending_base ON lc_generations(pending_base_generation,channel_id);

CREATE INDEX lc_playback_refs_generation ON lc_playback_refs(generation_id,occurrence_id,playback_id);

CREATE INDEX lc_generation_command_receipts_scope ON lc_generation_command_receipts(channel_id,generation_id);

CREATE INDEX lc_generation_retirements_scope ON lc_generation_retirements(channel_id,generation_id);`
