package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP person provider accept 234. The person operation owner, the generic
// evidence body/build/child rows and the catalogue person are embedded. The
// accepted person publication/source/origin owners are group D and
// fixture-qualified until that group is embedded, so these definitions make no
// Capture claim. Every read is an exact primary key, the exact sealed build or
// the exact person receipt tuple; no latest, MAX or scan selection exists.
const (
	ipPersonAcceptBuildStmt         stmt.ID = "ip.person.accept.build"
	ipPersonAcceptEvidenceStmt      stmt.ID = "ip.person.accept.evidence"
	ipPersonAcceptSealStmt          stmt.ID = "ip.person.accept.seal"
	ipPersonAcceptAliasesStmt       stmt.ID = "ip.person.accept.aliases"
	ipPersonAcceptExternalStmt      stmt.ID = "ip.person.accept.external"
	ipPersonAcceptSourceStmt        stmt.ID = "ip.person.accept.source"
	ipPersonAcceptPersonStmt        stmt.ID = "ip.person.accept.person"
	ipPersonAcceptSearchStmt        stmt.ID = "ip.person.accept.search_intent"
	ipPersonAcceptSourceInsertStmt  stmt.ID = "ip.person.accept.source_insert"
	ipPersonAcceptSourceCASStmt     stmt.ID = "ip.person.accept.source_cas"
	ipPersonAcceptPublicationStmt   stmt.ID = "ip.person.accept.publication"
	ipPersonAcceptPersonWriteStmt   stmt.ID = "ip.person.accept.person_write"
	ipPersonAcceptAliasStmt         stmt.ID = "ip.person.accept.alias"
	ipPersonAcceptExternalWriteStmt stmt.ID = "ip.person.accept.external_write"
	ipPersonAcceptOriginStmt        stmt.ID = "ip.person.accept.origin"
	ipPersonAcceptRetainProofStmt   stmt.ID = "ip.person.accept.retain_proof"
	ipPersonAcceptOwnerStmt         stmt.ID = "ip.person.accept.owner_advance"
	ipPersonAcceptRecordStmt        stmt.ID = "ip.person.accept.record"
)

// The accept proves the ORIGINAL sealed person observation, the exact live
// person owner, the complete alias/external cohort and the PB people search
// intent, then writes one accepted person publication head, the person/provider
// source slot, the typed person fact values, aliases, external IDs, field
// origins, the source-proof retention, the owner terminal phase and the typed
// receipt in ONE transaction. It invents no catalogue person and copies no
// provider children per person root.
func ipPersonAcceptDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{
			// The one sealed person build. The seal tuple, owner incarnation and
			// complete manifest witnesses are all read from this exact row.
			ID: ipPersonAcceptBuildStmt, SQL: `SELECT evidence_id,owner_id,owner_incarnation,owner_generation,generation,state,cursor,expected_rows,expected_bytes,expected_algorithm,expected_commitment,staged_rows,staged_bytes,content_revision,provider,subject_kind,provider_id,seal_owner,seal_incarnation,seal_sequence,seal_kind FROM metadata_person_builds WHERE id=?`,
			Params: []stmt.Param{t("build")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_builds"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"build-1"}},
			},
		},
		{
			// The immutable person observation header. Only the person carriers
			// are read; a relabeled movie/entity observation never matches.
			ID: ipPersonAcceptEvidenceStmt, SQL: `SELECT subject_id,provider,subject_kind,provider_id,parser_version,language,region,request_shape,digest,observed_at,source_url,sealed,title,biography,birth_date,death_date,birth_place FROM metadata_evidence WHERE id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"evidence-1"}},
			},
		},
		{
			// Exactly one person evidence_seal receipt owns this evidence. The
			// reverse evidence index is the exact group-D receipt path.
			ID: ipPersonAcceptSealStmt, SQL: `SELECT command_incarnation,command_sequence,result_digest,accepted_revision,applied_ms,evidence_id,build_id,seal_algorithm,seal_version,seal_commitment,seal_rows,seal_bytes,proof_pin_count FROM metadata_person_receipts WHERE owner_id=? AND evidence_id=? AND command_kind='evidence_seal'`,
			Params: []stmt.Param{t("owner"), t("evidence")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_person_receipt_evidence"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"owner-1", "evidence-1"}},
			},
		},
		{
			// Every persisted alias ordinal and value is the exact sealed cohort.
			ID: ipPersonAcceptAliasesStmt, SQL: `SELECT ordinal,requested_id,canonical_id,alias,observed_at FROM metadata_evidence_aliases WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_aliases"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"evidence-1"}},
			},
		},
		{
			ID: ipPersonAcceptExternalStmt, SQL: `SELECT provider,subject_kind,provider_id FROM metadata_evidence_external_ids WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_external_ids"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{"evidence-1"}},
			},
		},
		{
			// The current person/provider/role source slot. The accept may birth
			// the slot; a delta must match its exact incarnation/revision/head.
			ID: ipPersonAcceptSourceStmt, SQL: `SELECT incarnation,revision,provider_id,accepted_publication_id,accepted_evidence_id FROM metadata_person_sources WHERE person_id=? AND provider=? AND role='primary'`,
			Params: []stmt.Param{i("person"), t("provider")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_sources"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(42), "tmdb"}},
			},
		},
		{
			// The exact captured catalogue person and its current automatic values.
			// The token is the owner's captured token, never a name search.
			ID: ipPersonAcceptPersonStmt, SQL: `SELECT revision,name,sort_name,biography,birth_place,biography_source,birth_date,death_date FROM catalog_people WHERE id=? AND token=?`,
			Params: []stmt.Param{i("person"), t("token")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_people"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(42), "person-token-42"}},
			},
		},
		{
			// The PB people search intent is a precondition, not a fabricated
			// publication. IP never bumps the search owner's own sequence.
			ID: ipPersonAcceptSearchStmt, SQL: `SELECT id,person_id,search_generation_id,phase FROM search_publish_jobs WHERE id=? AND person_id=?`,
			Params: []stmt.Param{i("job"), i("person")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"search_publish_jobs"}},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(1), int64(42)}},
				{Name: "missing", Bindings: []any{int64(2), int64(42)}},
			},
		},
		{
			// Birth the ONE person/provider/role source slot from the accept's own
			// incarnation. A slot is never re-birthed once accepted.
			ID: ipPersonAcceptSourceInsertStmt, SQL: `INSERT INTO metadata_person_sources(person_id,provider,role,provider_id,incarnation,revision,accepted_publication_id,accepted_evidence_id) VALUES(?,?,'primary',?,?,1,?,?)`,
			Params:  []stmt.Param{i("person"), t("provider"), t("provider_id"), b("incarnation"), t("publication"), t("evidence")},
			Cohorts: []stmt.Cohort{{Name: "inserted", Bindings: []any{int64(42), "tmdb", "42", make([]byte, 16), "publication-1", "evidence-1"}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{
			// The slot head moves only from the exact current incarnation, revision
			// and accepted pair; the incarnation itself is stable.
			ID: ipPersonAcceptSourceCASStmt, SQL: `UPDATE metadata_person_sources SET revision=revision+1,accepted_publication_id=?,accepted_evidence_id=? WHERE person_id=? AND provider=? AND role='primary' AND provider_id=? AND incarnation=? AND revision=? AND accepted_publication_id IS ? AND accepted_evidence_id IS ? AND revision<9223372036854775807 RETURNING revision`,
			Params: []stmt.Param{t("publication"), t("evidence"), i("person"), t("provider"),
				t("provider_id"), b("incarnation"), i("revision"), nt("old_publication"),
				nt("old_evidence")},
			Cohorts: []stmt.Cohort{{Name: "advanced", Bindings: []any{"publication-2", "evidence-2",
				int64(42), "tmdb", "42", make([]byte, 16), int64(1), "publication-1", "evidence-1"}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_sources"}},
		},
		{
			// One accepted person publication head. It pins the exact evidence and
			// its ORIGINAL person seal proof; the observation time is provider time,
			// never acceptance time.
			ID: ipPersonAcceptPublicationStmt, SQL: `INSERT INTO metadata_person_publications(id,person_id,provider,subject_kind,provider_id,evidence_id,operation_id,proof_owner,proof_incarnation,proof_sequence,proof_kind,accepted_revision,observed_at) VALUES(?,?,?,'person',?,?,?,?,?,?,'evidence_seal',?,?)`,
			Params: []stmt.Param{t("publication"), i("person"), t("provider"), t("provider_id"),
				t("evidence"), t("operation"), t("proof_owner"), b("proof_incarnation"),
				i("proof_sequence"), i("accepted_revision"), t("observed_at")},
			Cohorts: []stmt.Cohort{{Name: "inserted", Bindings: []any{"publication-1", int64(42),
				"tmdb", "42", "evidence-1", "owner-1", "owner-1", make([]byte, 16), int64(4),
				int64(2), "2026-01-01T00:00:00Z"}}},
			Plan: stmt.Plan{Hot: true},
		},
		{
			// The accepted typed values replace the current catalogue person under
			// exact old-value CAS. Unknown dates stay the exact accepted empty.
			ID: ipPersonAcceptPersonWriteStmt, SQL: `UPDATE catalog_people SET name=?,sort_name=?,biography=?,birth_date=?,death_date=?,birth_place=?,biography_source=?,revision=revision+1 WHERE id=? AND token=? AND revision=? AND name=? AND sort_name=? AND biography=? AND birth_date=? AND death_date=? AND birth_place=? AND biography_source=? AND revision<9223372036854775807 RETURNING revision`,
			Params: []stmt.Param{t("name"), t("sort"), t("biography"), t("birth_date"),
				t("death_date"), t("birth_place"), t("biography_source"), i("person"), t("token"),
				i("revision"), t("old_name"), t("old_sort"), t("old_biography"),
				t("old_birth_date"), t("old_death_date"), t("old_birth_place"),
				t("old_biography_source")},
			Cohorts: []stmt.Cohort{{Name: "advanced", Bindings: []any{"Ada Lovelace", "Lovelace, Ada",
				"Mathematician.", "1815-12-10", "1852-11-27", "London", "tmdb", int64(42),
				"person-token-42", int64(1), "Ada Lovelace", "Lovelace, Ada", "", "", "", "", ""}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_people"}},
		},
		{
			// Aliases are additive person facts. An alias already owned by another
			// person is never stolen; the name key matches the merged people merge.
			ID: ipPersonAcceptAliasStmt, SQL: `INSERT INTO catalog_people_aliases(alias,person_id) VALUES(?,?) ON CONFLICT(alias) DO NOTHING`,
			Params:  []stmt.Param{t("alias"), i("person")},
			Cohorts: []stmt.Cohort{{Name: "inserted", Bindings: []any{"name:ada lovelace", int64(42)}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{
			ID: ipPersonAcceptExternalWriteStmt, SQL: `INSERT INTO metadata_person_external_ids(person_id,provider,provider_id) VALUES(?,?,?) ON CONFLICT(person_id,provider,provider_id) DO NOTHING`,
			Params:  []stmt.Param{i("person"), t("provider"), t("provider_id")},
			Cohorts: []stmt.Cohort{{Name: "inserted", Bindings: []any{int64(42), "imdb", "nm0000001"}}},
			Plan:    stmt.Plan{Hot: true},
		},
		{
			// Exact field provenance with the ORIGINAL provider observation time.
			ID: ipPersonAcceptOriginStmt, SQL: `INSERT INTO metadata_person_field_origins(person_id,field,provider,provider_id,publication_id,evidence_id,source_url,observed_at,language,region,selection_revision) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(person_id,field) DO UPDATE SET provider=excluded.provider,provider_id=excluded.provider_id,publication_id=excluded.publication_id,evidence_id=excluded.evidence_id,source_url=excluded.source_url,observed_at=excluded.observed_at,language=excluded.language,region=excluded.region,selection_revision=excluded.selection_revision`,
			Params: []stmt.Param{i("person"), t("field"), t("provider"), t("provider_id"),
				t("publication"), t("evidence"), t("source_url"), t("observed_at"),
				t("language"), t("region"), i("selection_revision")},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{int64(42), "name", "tmdb", "42",
				"publication-1", "evidence-1", "https://api.themoviedb.org/3/person/42",
				"2026-01-01T00:00:00Z", "en-US", "US", int64(1)}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_field_origins"}},
		},
		{
			// The accepted source pins the ORIGINAL person seal receipt past ACK.
			ID: ipPersonAcceptRetainProofStmt, SQL: `UPDATE metadata_person_receipts SET proof_pin_count=proof_pin_count+1,proof_revision=proof_revision+1 WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='evidence_seal' AND evidence_id=? AND proof_pin_count>0 AND proof_pin_count<9223372036854775807 AND proof_revision<9223372036854775807`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), t("evidence")},
			Cohorts: []stmt.Cohort{{Name: "pinned", Bindings: []any{"owner-1", make([]byte, 16),
				int64(4), "evidence-1"}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_receipts"}},
		},
		{
			// Acceptance is the ONLY terminal person phase after seal. The exact
			// live owner CAS makes a second acceptance impossible.
			ID: ipPersonAcceptOwnerStmt, SQL: `UPDATE metadata_person_operations SET phase='accepted',revision=revision+1,last_command_sequence=? WHERE id=? AND command_incarnation=? AND generation=? AND revision=? AND phase='sealed' AND lease_owner=? AND lease_generation=? AND lease_until_ms=? AND last_command_sequence=? AND acknowledged_command_sequence=? RETURNING revision`,
			Params: []stmt.Param{i("sequence"), t("owner"), b("incarnation"), i("generation"),
				i("revision"), t("lease"), i("lease_generation"), i("lease_until"), i("old_last"),
				i("old_ack")},
			Cohorts: []stmt.Cohort{{Name: "accepted", Bindings: []any{int64(8), "owner-1",
				make([]byte, 16), int64(1), int64(7), "lease-1", int64(1), int64(300_000),
				int64(7), int64(7)}}},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_person_operations"}},
		},
		{
			// The one terminal accept result. It names the accepted publication,
			// evidence and build, and keeps its own proof pin.
			ID: ipPersonAcceptRecordStmt, SQL: `INSERT INTO metadata_person_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,after_ordinal,result_count,evidence_id,build_id,publication_id,proof_pin_count) VALUES(?,?,?,?,?,?,?,'accept',?,0,?,?,?,?,1)`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), b("operation"),
				b("request"), i("applied"), b("result_digest"), i("accepted_revision"),
				i("result_count"), t("evidence"), t("build"), t("publication")},
			Cohorts: []stmt.Cohort{{Name: "saved", Bindings: []any{"owner-1", make([]byte, 16),
				int64(8), make([]byte, 16), make([]byte, 32), int64(1), make([]byte, 32),
				int64(2), int64(5), "evidence-1", "build-1", "publication-1"}}},
			Plan: stmt.Plan{Hot: true},
		},
	}
	for n := range defs {
		defs[n].Owner = "store/writer IP person provider accept"
		defs[n].Kind = stmt.Read
		switch defs[n].ID {
		case ipPersonAcceptSourceInsertStmt, ipPersonAcceptSourceCASStmt,
			ipPersonAcceptPublicationStmt, ipPersonAcceptPersonWriteStmt,
			ipPersonAcceptAliasStmt, ipPersonAcceptExternalWriteStmt,
			ipPersonAcceptOriginStmt, ipPersonAcceptRetainProofStmt,
			ipPersonAcceptOwnerStmt, ipPersonAcceptRecordStmt:
			defs[n].Kind = stmt.Write
		}
		defs[n].CallSites = []string{"store/writer/ip_person_provider_accept.go",
			"store/writer/ip_person_provider_delta.go", "store/writer/ip_person_provider_accept_statements.go"}
		if len(defs[n].Cohorts) == 0 {
			defs[n].Cohorts = []stmt.Cohort{ipPersonAcceptCohort(defs[n])}
		}
	}
	return defs
}

// ipPersonAcceptCohort is one named missing-key fixture per statement. Values
// follow the declared parameter types; no statement-specific authority is
// implied by a representative binding.
func ipPersonAcceptCohort(def stmt.Definition) stmt.Cohort {
	args := make([]any, len(def.Params))
	for i, p := range def.Params {
		switch p.Type {
		case stmt.Text:
			if p.Nullable {
				args[i] = nil
			} else {
				args[i] = ""
			}
		case stmt.Blob:
			if p.Nullable {
				args[i] = nil
			} else {
				args[i] = make([]byte, 16)
			}
		case stmt.Real:
			args[i] = 1.0
		default:
			if p.Nullable {
				args[i] = nil
			} else {
				args[i] = int64(1)
			}
		}
	}
	return stmt.Cohort{Name: "missing_native_keys", Bindings: args}
}
