-- Exact D subset needed by IP-04's refresh page, 8 November 2026.
-- Proposed fixture only; group D is not embedded and this makes no Capture
-- claim. The acquisition owner table is already in the embedded schema; this
-- adds its raw provider/purpose/entity key. Person phase adds the accepted
-- person source owner and its provider/person keyset. The publication table is
-- a minimal FK target carrying only the exact composite key the source needs.
CREATE INDEX IF NOT EXISTS metadata_acquisition_sweep ON metadata_acquisition_jobs(provider,purpose,entity_id);

CREATE TABLE IF NOT EXISTS metadata_person_publications(
 id TEXT PRIMARY KEY NOT NULL,
 person_id INTEGER NOT NULL,
 provider TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 evidence_id TEXT NOT NULL,
 UNIQUE(person_id,provider,provider_id,id,evidence_id)
);

CREATE TABLE IF NOT EXISTS metadata_person_sources(
 person_id INTEGER NOT NULL REFERENCES catalog_people(id),
 provider TEXT NOT NULL REFERENCES metadata_providers(code),
 role TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 incarnation BLOB NOT NULL CHECK(length(incarnation)=16),
 revision INTEGER NOT NULL CHECK(revision>0),
 accepted_publication_id TEXT,
 accepted_evidence_id TEXT,
 PRIMARY KEY(person_id,provider,role),
 CHECK((accepted_publication_id IS NULL)=(accepted_evidence_id IS NULL)),
 FOREIGN KEY(person_id,provider,provider_id,accepted_publication_id,accepted_evidence_id) REFERENCES metadata_person_publications(person_id,provider,provider_id,id,evidence_id)
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS metadata_person_source_provider ON metadata_person_sources(provider,person_id);
CREATE INDEX IF NOT EXISTS metadata_person_source_publication ON metadata_person_sources(person_id,provider,provider_id,accepted_publication_id,accepted_evidence_id);
