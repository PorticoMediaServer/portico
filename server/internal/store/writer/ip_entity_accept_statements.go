package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IP nonmovie entity provider accept 216. The exact provider-operation owner
// (metadata_provider_operations), the immutable evidence body/build and the
// publication/subject-provider/field-origin bodies are embedded. The IP accept
// receipt header, its evidence reverse index and the accepted-source proof
// retention are group D and fixture-qualified until that group is embedded, so
// these definitions make no Capture claim.
const (
	ipEntityAcceptEvidenceStmt      stmt.ID = "ip.entity.accept.evidence"
	ipEntityAcceptEvidenceSealStmt  stmt.ID = "ip.entity.accept.evidence_seal"
	ipEntityAcceptSetSealStmt       stmt.ID = "ip.entity.accept.set_seal"
	ipEntityAcceptSetStmt           stmt.ID = "ip.entity.accept.set"
	ipEntityAcceptBuildStmt         stmt.ID = "ip.entity.accept.build"
	ipEntityAcceptSelectionStmt     stmt.ID = "ip.entity.accept.selection"
	ipEntityAcceptPriorStmt         stmt.ID = "ip.entity.accept.prior_publication"
	ipEntityAcceptPublicationStmt   stmt.ID = "ip.entity.accept.publication"
	ipEntityAcceptSelectionCASStmt  stmt.ID = "ip.entity.accept.selection_cas"
	ipEntityAcceptExternalCountStmt stmt.ID = "ip.entity.accept.external_count"
	ipEntityAcceptExternalPointStmt stmt.ID = "ip.entity.accept.external_point"
	ipEntityAcceptExternalStmt      stmt.ID = "ip.entity.accept.external"
	ipEntityAcceptTermCountStmt     stmt.ID = "ip.entity.accept.term_count"
	ipEntityAcceptTermPointStmt     stmt.ID = "ip.entity.accept.term_point"
	ipEntityAcceptTermStmt          stmt.ID = "ip.entity.accept.term"
	ipEntityAcceptEntityTermStmt    stmt.ID = "ip.entity.accept.entity_term"
	ipEntityAcceptTermSourceStmt    stmt.ID = "ip.entity.accept.term_source"
	ipEntityAcceptCreditCountStmt   stmt.ID = "ip.entity.accept.credit_count"
	ipEntityAcceptCreditPointStmt   stmt.ID = "ip.entity.accept.credit_point"
	ipEntityAcceptEntityStmt        stmt.ID = "ip.entity.accept.entity"
	ipEntityAcceptEntityWriteStmt   stmt.ID = "ip.entity.accept.entity_write"
	ipEntityAcceptOwnerFieldStmt    stmt.ID = "ip.entity.accept.owner_field"
	ipEntityAcceptOriginStmt        stmt.ID = "ip.entity.accept.origin"
	ipEntityAcceptSearchStmt        stmt.ID = "ip.entity.accept.search_intent"
	ipEntityAcceptRetainProofStmt   stmt.ID = "ip.entity.accept.retain_proof"
	ipEntityAcceptRecordStmt        stmt.ID = "ip.entity.accept.record"
	ipEntityAcceptSavedStmt         stmt.ID = "ip.entity.accept.saved"
)

// The accept proves the ORIGINAL sealed observation, the current selected
// provider head and the exact complete child cohort, then writes one accepted
// publication head, the accepted subject slot, the entity IDs/terms, the owner
// field automatic observations and their field origins, the search intent pin,
// the typed receipt and the source-proof retention in ONE transaction. It never
// writes an EN FIRST seal tuple, a credit image or a metadata copy per child.
func ipEntityAcceptDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{
			// The immutable sealed observation header and its exact IP proof tuple.
			ID: ipEntityAcceptEvidenceStmt, SQL: `SELECT subject_id,provider,subject_kind,provider_id,parser_version,language,region,request_shape,observed_at,source_url,digest,sealed,ip_seal_owner,ip_seal_incarnation,ip_seal_sequence FROM metadata_evidence WHERE id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence"}},
		},
		{
			// Exactly one evidence_seal receipt owns this evidence. The reverse
			// evidence index is the exact group-D receipt path.
			ID: ipEntityAcceptEvidenceSealStmt, SQL: `SELECT command_incarnation,command_sequence,result_digest,accepted_revision,applied_ms,terminal,evidence_subject_id,parser_version,build_id,seal_commitment,seal_rows,seal_bytes,proof_pin_count FROM metadata_ip_receipts WHERE owner_id=? AND evidence_id=? AND command_kind='evidence_seal'`,
			Params: []stmt.Param{t("owner"), t("evidence")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_ip_receipt_evidence"}},
		},
		{
			// Exactly one set_seal receipt owns a complete TV set. The reverse set
			// index is the exact group-D receipt path.
			ID: ipEntityAcceptSetSealStmt, SQL: `SELECT command_incarnation,command_sequence,result_digest,accepted_revision,applied_ms,terminal,evidence_id,evidence_subject_id,parser_version,build_id,seal_commitment,seal_rows,seal_bytes,proof_pin_count FROM metadata_ip_receipts WHERE owner_id=? AND set_id=? AND command_kind='set_seal'`,
			Params: []stmt.Param{t("owner"), t("set")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"metadata_ip_receipt_set"}},
		},
		{
			ID: ipEntityAcceptSetStmt, SQL: `SELECT entity_id,provider,subject_kind,provider_id,complete,coverage_complete,incarnation,generation,revision,ip_proof_owner,ip_proof_incarnation,ip_proof_sequence FROM metadata_publication_sets WHERE id=?`,
			Params: []stmt.Param{t("set")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_publication_sets"}},
		},
		{
			// The complete sealed build the acceptance may publish. It is read by
			// its PK only; no child scan chooses the build.
			ID: ipEntityAcceptBuildStmt, SQL: `SELECT b.evidence_id,b.owner_operation,b.owner_incarnation,b.owner_generation,b.state,b.phase,b.cursor,b.expected_rows,b.materialized_rows,b.content_revision,b.generation,e.subject_id,e.provider,e.subject_kind,e.provider_id,e.parser_version,e.language,e.region,e.request_shape,e.observed_at,e.digest FROM metadata_evidence_builds AS b JOIN metadata_evidence AS e ON e.id=b.evidence_id WHERE b.id=?`,
			Params: []stmt.Param{t("build")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_builds", "metadata_evidence"}},
		},
		{
			// The current selected provider head. This is the IA accepted-subject
			// seam IP-09 assumes; it is not created or reinterpreted here.
			ID: ipEntityAcceptSelectionStmt, SQL: `SELECT incarnation,revision,subject_kind,provider_id,status,accepted_evidence_id,accepted_set_id,accepted_publication_id,accepted_track_id,observed_at FROM metadata_subject_providers WHERE entity_id=? AND provider=? AND role='primary'`,
			Params: []stmt.Param{i("entity"), t("provider")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_subject_providers"}},
		},
		{
			// The prior accepted publication's provider namespace, used for the
			// exact previous_* linkage. It is read only when the selected slot
			// already names a prior accepted publication.
			ID: ipEntityAcceptPriorStmt, SQL: `SELECT provider_id FROM metadata_publications WHERE id=?`,
			Params: []stmt.Param{t("publication")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_publications"}},
		},
		{
			// One accepted publication head. previous_* are closed NULL groups and
			// the evidence/set tuple is the exact sealed source identity.
			ID: ipEntityAcceptPublicationStmt, SQL: `INSERT INTO metadata_publications(previous_provider,previous_subject_kind,previous_provider_id,id,operation_id,entity_id,provider,subject_kind,provider_id,evidence_id,set_id,previous_id,selection_revision,generation,decision,observed_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,'accepted',?)`,
			Params: []stmt.Param{
				nt("previous_provider"), nt("previous_subject_kind"), nt("previous_provider_id"),
				t("publication"), t("operation"), i("entity"), t("provider"), t("subject_kind"),
				t("provider_id"), t("evidence"), nt("set"), nt("previous_id"),
				i("selection_revision"), i("generation"), t("observed_at"),
			},
		},
		{
			// The accepted slot moves only from the exact current selected head and
			// keeps the ORIGINAL provider observation time, never acceptance time.
			ID: ipEntityAcceptSelectionCASStmt, SQL: `UPDATE metadata_subject_providers SET accepted_evidence_id=?,accepted_set_id=?,accepted_publication_id=?,revision=revision+1,observed_at=? WHERE entity_id=? AND provider=? AND role='primary' AND incarnation=? AND revision=? AND subject_kind=? AND provider_id=? AND status='selected' AND accepted_track_id IS NULL AND accepted_evidence_id IS ? AND accepted_set_id IS ? AND accepted_publication_id IS ? AND revision<9223372036854775807 RETURNING revision`,
			Params: []stmt.Param{
				t("evidence"), nt("set"), t("publication"), t("observed_at"),
				i("entity"), t("provider"), b("incarnation"), i("revision"),
				t("subject_kind"), t("provider_id"), nt("old_evidence"), nt("old_set"),
				nt("old_publication"),
			},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_subject_providers"}},
		},
		{
			ID: ipEntityAcceptExternalCountStmt, SQL: `SELECT count(*) FROM metadata_evidence_external_ids WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_external_ids"}},
		},
		{
			ID: ipEntityAcceptExternalPointStmt, SQL: `SELECT 1 FROM metadata_evidence_external_ids WHERE evidence_id=? AND provider=? AND subject_kind=? AND provider_id=?`,
			Params: []stmt.Param{t("evidence"), t("provider"), t("subject_kind"), t("provider_id")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_external_ids"}},
		},
		{
			ID: ipEntityAcceptExternalStmt, SQL: `INSERT INTO metadata_entity_external_ids(entity_id,provider,subject_kind,provider_id) VALUES(?,?,?,?) ON CONFLICT(entity_id,provider,subject_kind,provider_id) DO NOTHING`,
			Params: []stmt.Param{i("entity"), t("provider"), t("subject_kind"), t("provider_id")},
		},
		{
			ID: ipEntityAcceptTermCountStmt, SQL: `SELECT count(*) FROM metadata_evidence_terms WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_terms"}},
		},
		{
			ID: ipEntityAcceptTermPointStmt, SQL: `SELECT 1 FROM metadata_evidence_terms WHERE evidence_id=? AND vocabulary=? AND ordinal=? AND value=?`,
			Params: []stmt.Param{t("evidence"), t("vocabulary"), i("ordinal"), t("value")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_terms"}},
		},
		{
			ID: ipEntityAcceptTermStmt, SQL: `INSERT INTO catalog_terms(vocab,key,label) VALUES(?,?,?) ON CONFLICT(vocab,key) DO UPDATE SET label=excluded.label RETURNING id`,
			Params: []stmt.Param{i("vocab"), t("key"), t("label")},
		},
		{
			ID: ipEntityAcceptEntityTermStmt, SQL: `INSERT INTO catalog_entity_terms(entity_id,term_id) VALUES(?,?) ON CONFLICT(entity_id,term_id) DO NOTHING`,
			Params: []stmt.Param{i("entity"), i("term")},
		},
		{
			ID: ipEntityAcceptTermSourceStmt, SQL: `INSERT INTO catalog_term_sources(entity_id,term_id,provider,source_id,label_override,source_name,ordinal) VALUES(?,?,?,?,NULL,'',?) ON CONFLICT(entity_id,provider,source_id) DO UPDATE SET term_id=excluded.term_id,label_override=NULL,source_name=excluded.source_name,ordinal=excluded.ordinal`,
			Params: []stmt.Param{i("entity"), i("term"), t("provider"), t("source_id"), i("ordinal")},
		},
		{
			ID: ipEntityAcceptCreditCountStmt, SQL: `SELECT count(*) FROM metadata_evidence_credits WHERE evidence_id=?`,
			Params: []stmt.Param{t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_credits"}},
		},
		{
			// Exact evidence credit row. billing/source_ordinal are nullable, so
			// they are compared with IS ? rather than an OR sentinel.
			ID: ipEntityAcceptCreditPointStmt, SQL: `SELECT 1 FROM metadata_evidence_credits WHERE evidence_id=? AND scope=? AND ordinal=? AND credit_class=? AND provider_person_id=? AND credit_id=? AND name=? AND role=? AND department=? AND billing IS ? AND source_ordinal IS ?`,
			Params: []stmt.Param{
				t("evidence"), t("scope"), i("ordinal"), t("credit_class"),
				t("provider_person_id"), t("credit_id"), t("name"), t("role"),
				t("department"), ni("billing"), ni("source_ordinal"),
			},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_evidence_credits"}},
		},
		{
			// The current automatic projection and its exact CAS witnesses.
			ID: ipEntityAcceptEntityStmt, SQL: `SELECT title,sort_title,metadata_language,overview,original_title,tagline,release_date,content_rating,studio,network,country,year,revision,metadata_incarnation FROM catalog_entities WHERE id=? AND metadata_incarnation=? AND retired=0`,
			Params: []stmt.Param{i("entity"), b("incarnation")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}},
		},
		{
			// The accepted automatic values replace the current projection under
			// exact old-value CAS. Locked owner values stay in metadata_owner_fields
			// and keep overriding these automatic values at read time.
			ID: ipEntityAcceptEntityWriteStmt, SQL: `UPDATE catalog_entities SET title=?,sort_title=?,metadata_language=?,sort_key=?,sort_head=?,sort_key_keep_articles=?,sort_head_keep_articles=?,overview=?,original_title=?,tagline=?,release_date=?,content_rating=?,studio=?,network=?,country=?,year=?,revision=revision+1,metadata_revision=metadata_revision+1 WHERE id=? AND revision=? AND metadata_incarnation=? AND retired=0 AND title=? AND sort_title=? AND metadata_language=? AND overview=? AND original_title=? AND tagline=? AND release_date=? AND content_rating=? AND studio=? AND network=? AND country=? AND year=? AND revision<9223372036854775807 AND metadata_revision<9223372036854775807 RETURNING revision`,
			Params: []stmt.Param{
				t("title"), t("sort_title"), t("language"), t("sort_key"), t("sort_head"),
				t("sort_key_keep"), t("sort_head_keep"), t("overview"), t("original_title"),
				t("tagline"), t("release_date"), t("content_rating"), t("studio"),
				t("network"), t("country"), i("year"),
				i("entity"), i("revision"), b("incarnation"),
				t("old_title"), t("old_sort_title"), t("old_language"), t("old_overview"),
				t("old_original_title"), t("old_tagline"), t("old_release_date"),
				t("old_content_rating"), t("old_studio"), t("old_network"),
				t("old_country"), i("old_year"),
			},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}},
		},
		{
			// The automatic observation is retained under a lock; only the
			// automatic pair moves and the owner value/lock stays untouched.
			ID: ipEntityAcceptOwnerFieldStmt, SQL: `UPDATE metadata_owner_fields SET automatic_value=?,automatic_present=? WHERE kind=? AND entity_id=? AND field=?`,
			Params: []stmt.Param{t("automatic"), i("automatic_present"), t("kind"), i("entity"), t("field")},
		},
		{
			// Exact field provenance with the ORIGINAL provider observation time.
			ID: ipEntityAcceptOriginStmt, SQL: `INSERT INTO metadata_field_origins(subject_kind,provider_id,entity_id,field,provider,source_kind,source_url,confidence,observed_at,language,region,selection_revision,publication_id,evidence_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(entity_id,field) DO UPDATE SET subject_kind=excluded.subject_kind,provider_id=excluded.provider_id,provider=excluded.provider,source_kind=excluded.source_kind,source_url=excluded.source_url,confidence=excluded.confidence,observed_at=excluded.observed_at,language=excluded.language,region=excluded.region,selection_revision=excluded.selection_revision,publication_id=excluded.publication_id,evidence_id=excluded.evidence_id`,
			Params: []stmt.Param{
				t("subject_kind"), t("provider_id"), i("entity"), t("field"), t("provider"),
				t("source_kind"), t("source_url"), stmt.Param{Name: "confidence", Type: stmt.Real, Nullable: false},
				t("observed_at"), t("language"), t("region"), i("selection_revision"),
				t("publication"), t("evidence"),
			},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_field_origins"}},
		},
		{
			// The search intent is a precondition, not a fabricated publication.
			// IP never bumps the search owner's own sequence.
			ID: ipEntityAcceptSearchStmt, SQL: `SELECT id,entity_id,search_generation_id,phase FROM search_publish_jobs WHERE id=? AND entity_id=?`,
			Params: []stmt.Param{i("job"), i("entity")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"search_publish_jobs"}},
		},
		{
			// The accepted source pins the ORIGINAL seal receipt past public ACK.
			ID: ipEntityAcceptRetainProofStmt, SQL: `UPDATE metadata_ip_receipts SET proof_pin_count=proof_pin_count+1,proof_revision=proof_revision+1 WHERE owner_id=? AND command_incarnation=? AND command_sequence=? AND command_kind=? AND evidence_id=? AND proof_pin_count>0 AND proof_pin_count<9223372036854775807 AND proof_revision<9223372036854775807`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence"), t("kind"), t("evidence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_ip_receipts"}},
		},
		{
			// The one terminal accept result. It carries the publication and the
			// exact source proof witnesses, never a mutable current head.
			ID: ipEntityAcceptRecordStmt, SQL: `INSERT INTO metadata_ip_receipts(owner_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,result_digest,result_phase,after_family,after_ordinal,after_key,result_count,result_bytes,terminal,evidence_id,evidence_subject_id,parser_version,build_id,set_id,publication_id,proof_pin_count) VALUES(?,?,?,?,?,'accept',?,?,?,'accepted',0,0,'',?,?,1,?,?,?,?,?,?,1)`,
			Params: []stmt.Param{
				t("owner"), b("incarnation"), i("sequence"), b("operation"), b("request"),
				i("accepted_revision"), i("applied_ms"), b("result_digest"),
				i("result_count"), i("result_bytes"), t("evidence"), i("subject"),
				t("parser"), t("build"), nt("set"), t("publication"),
			},
		},
		{
			// The exact saved accept header, including every source witness used
			// by typed replay. A replay never reads mutable publication state.
			ID: ipEntityAcceptSavedStmt, SQL: `SELECT command_incarnation,command_sequence,operation_id,request_digest,applied_ms,result_digest,command_kind,accepted_revision,result_phase,terminal,evidence_id,evidence_subject_id,parser_version,build_id,set_id,publication_id,result_count,result_bytes FROM metadata_ip_receipts WHERE owner_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{t("owner"), b("incarnation"), i("sequence")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_ip_receipts"}},
		},
	}
	for x := range defs {
		defs[x].Owner = "store/writer IP entity accept"
		defs[x].Kind = stmt.Read
		switch defs[x].ID {
		case ipEntityAcceptPublicationStmt, ipEntityAcceptSelectionCASStmt, ipEntityAcceptExternalStmt,
			ipEntityAcceptTermStmt, ipEntityAcceptEntityTermStmt, ipEntityAcceptTermSourceStmt,
			ipEntityAcceptEntityWriteStmt, ipEntityAcceptOwnerFieldStmt, ipEntityAcceptOriginStmt,
			ipEntityAcceptRetainProofStmt, ipEntityAcceptRecordStmt:
			defs[x].Kind = stmt.Write
		}
		defs[x].CallSites = []string{"store/writer/ip_entity_accept.go", "store/writer/ip_entity_accept_statements.go"}
		defs[x].Cohorts = []stmt.Cohort{ipEntityAcceptCohort(defs[x])}
	}
	return defs
}

// ipEntityAcceptCohort is one named missing-key fixture per statement. Values
// follow the declared parameter types; no statement-specific authority is
// implied by a representative binding.
func ipEntityAcceptCohort(def stmt.Definition) stmt.Cohort {
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
