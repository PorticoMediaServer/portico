-- Exact E artwork selection subset from PACKET-IP, 8 November 2026, loaded
-- AFTER testdata/ip-artwork-material-draft.sql and
-- testdata/ip-artwork-material-publish-draft.sql. Proposed fixture only; group E
-- is not embedded and this makes no Capture, adoption or schema-SHA claim.
--
-- The material fixture already recreates the subject-job owner and the closed
-- artwork receipt table with its select/clear labels; the publish fixture adds
-- the source/blob revision, retirement and deletion columns. Selection
-- additionally needs the E replacement of artwork_selections (nullable digest
-- for the explicit locked-empty choice) and the normalized history-artwork pin
-- table with its digest reverse index. artwork_material_slots and
-- artwork_material_intents.source_digest stay outside this closure.
DROP TABLE artwork_selections;

CREATE TABLE artwork_selections(
 kind TEXT NOT NULL CHECK(kind IN('entity','person')),
 entity_id INTEGER NOT NULL,
 role TEXT NOT NULL CHECK(length(role)>0 AND role=lower(trim(role)) AND role NOT IN('thumb','thumbnail')),
 choice TEXT NOT NULL CHECK(choice IN('matched','locked')),
 digest TEXT REFERENCES artwork_sources(digest),
 provider TEXT NOT NULL,
 image_id TEXT NOT NULL,
 origin TEXT NOT NULL,
 attribution TEXT NOT NULL,
 locale TEXT NOT NULL DEFAULT '',
 effective_language TEXT NOT NULL DEFAULT '',
 source_fence TEXT NOT NULL,
 source_object_id TEXT,
 source_object_revision TEXT,
 revision INTEGER NOT NULL DEFAULT 1 CHECK(revision>0),
 actor TEXT NOT NULL,
 observed_at TEXT NOT NULL,
 PRIMARY KEY(kind,entity_id,role,choice),
 CHECK((source_object_id IS NULL)=(source_object_revision IS NULL)),
 CHECK(digest IS NOT NULL OR choice='locked'),
 CHECK(typeof("kind")IN('null','text')),
 CHECK(typeof("entity_id")IN('null','integer')),
 CHECK(typeof("role")IN('null','text')),
 CHECK(typeof("choice")IN('null','text')),
 CHECK(typeof("digest")IN('null','text')),
 CHECK(typeof("provider")IN('null','text')),
 CHECK(typeof("image_id")IN('null','text')),
 CHECK(typeof("origin")IN('null','text')),
 CHECK(typeof("attribution")IN('null','text')),
 CHECK(typeof("locale")IN('null','text')),
 CHECK(typeof("effective_language")IN('null','text')),
 CHECK(typeof("source_fence")IN('null','text')),
 CHECK(typeof("source_object_id")IN('null','text')),
 CHECK(typeof("source_object_revision")IN('null','text')),
 CHECK(typeof("revision")IN('null','integer')),
 CHECK(typeof("actor")IN('null','text')),
 CHECK(typeof("observed_at")IN('null','text'))
) WITHOUT ROWID;

CREATE INDEX artwork_selections_digest ON artwork_selections(digest);
CREATE INDEX artwork_selections_source ON artwork_selections(provider,origin,digest);

-- E: Exact artwork history/undo pins; no instr(JSON,digest) GC heuristic.
CREATE TABLE metadata_owner_history_artwork(
 history_id INTEGER NOT NULL REFERENCES metadata_owner_history(id),
 role TEXT NOT NULL,
 choice TEXT NOT NULL CHECK(choice IN('matched','locked')),
 digest TEXT NOT NULL REFERENCES artwork_sources(digest),
 PRIMARY KEY(history_id,role,choice,digest),
 CHECK(typeof(history_id) IN('null','integer')),
 CHECK(typeof(role) IN('null','text')),
 CHECK(typeof(choice) IN('null','text')),
 CHECK(typeof(digest) IN('null','text'))
) WITHOUT ROWID;

-- E: source FK, exact undo hold and GC reverse probe.
CREATE INDEX metadata_owner_history_artwork_digest ON metadata_owner_history_artwork(digest,history_id);
