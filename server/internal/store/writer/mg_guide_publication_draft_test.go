package writer

// Only an empty disposable canonical clone is overlaid for private effect
// controls. Exact owner draft 38b4c8d9 / full candidate SHA4e0751989357d1aed85467ba81f5c24ce79aa3a26f947e0fd222070891bb9262.
// Not product schema, command admission, authority or qualification.
const mgGuidePublicationDraftSQL = `CREATE TABLE live_sources(
 id TEXT PRIMARY KEY REFERENCES live_source_identities(id),
 name TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),
 state TEXT NOT NULL CHECK(state IN ('active','disabled')),
 tuner_count INTEGER NOT NULL CHECK(tuner_count>=0),
 active_generation TEXT NOT NULL,
 published_at TEXT NOT NULL,
 FOREIGN KEY(active_generation,id) REFERENCES live_generations(id,source_id)
) STRICT;

CREATE TABLE live_operations(
 request_id TEXT PRIMARY KEY,
 input_hash TEXT NOT NULL,
 source_id TEXT NOT NULL REFERENCES live_source_identities(id),
 generation_id TEXT NOT NULL,
 expected_revision INTEGER NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('staging','published','superseded')),
 result_revision INTEGER NOT NULL DEFAULT 0,
 result_state TEXT NOT NULL DEFAULT '',
 published_at TEXT NOT NULL DEFAULT '',
 command_incarnation BLOB NOT NULL CHECK(length(command_incarnation)=16),
 create_operation_id BLOB NOT NULL CHECK(length(create_operation_id)=16),
 birth_request_digest BLOB NOT NULL CHECK(length(birth_request_digest)=32),
 last_command_sequence INTEGER NOT NULL DEFAULT 1 CHECK(last_command_sequence>=1),
 acknowledged_command_sequence INTEGER NOT NULL DEFAULT 0 CHECK(acknowledged_command_sequence>=0 AND acknowledged_command_sequence<=last_command_sequence),
 expected_generation TEXT NOT NULL DEFAULT '',
 expected_choice_count INTEGER NOT NULL CHECK(expected_choice_count>=0),
 stage_revision INTEGER NOT NULL DEFAULT 1 CHECK(stage_revision>0),
 staged_channel_count INTEGER NOT NULL DEFAULT 0 CHECK(staged_channel_count>=0),
 staged_programme_count INTEGER NOT NULL DEFAULT 0 CHECK(staged_programme_count>=0),
 staged_choice_count INTEGER NOT NULL DEFAULT 0 CHECK(staged_choice_count>=0 AND staged_choice_count<=expected_choice_count),
 staged_bytes INTEGER NOT NULL DEFAULT 0 CHECK(staged_bytes>=0),
 manifest_digest BLOB NOT NULL CHECK(length(manifest_digest)=32),
 stage_digest BLOB NOT NULL CHECK(length(stage_digest)=32),
 sealed INTEGER NOT NULL DEFAULT 0 CHECK(sealed IN(0,1)),
 reclaim_family TEXT NOT NULL DEFAULT 'none' CHECK(reclaim_family IN('none','programmes','channels','choices','generation','complete')),
 reclaim_cursor TEXT NOT NULL DEFAULT '',
 FOREIGN KEY(generation_id,source_id) REFERENCES live_generations(id,source_id),
 CHECK((expected_revision=0)=(expected_generation='')),
 CONSTRAINT live_operation_seal CHECK(sealed=0 OR (staged_channel_count>0 AND staged_choice_count=expected_choice_count AND stage_digest=manifest_digest))
) STRICT;

CREATE UNIQUE INDEX live_generations_source_scope ON live_generations(id,source_id);`
