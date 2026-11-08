package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	enrichAcceptImagePoint    stmt.ID = "enrichment.accept.image"
	enrichAcceptCreditPerson  stmt.ID = "enrichment.accept.credit_person"
	enrichAcceptCreditScoped  stmt.ID = "enrichment.accept.credit_scoped"
	enrichAcceptNoExtra       stmt.ID = "enrichment.accept.no_extra_credit"
	enrichAcceptOwnerEntries  stmt.ID = "enrichment.accept.owner_entries"
	enrichAcceptOtherProvider stmt.ID = "enrichment.accept.other_provider"
	enrichAcceptSealImage     stmt.ID = "enrichment.accept.seal_image"
	enrichAcceptHeadEmpty     stmt.ID = "enrichment.accept.head_empty"
	enrichAcceptHeadReplace   stmt.ID = "enrichment.accept.head_replace"
	enrichAcceptSourceFirst   stmt.ID = "enrichment.accept.source_first"
	enrichAcceptSourceReplace stmt.ID = "enrichment.accept.source_replace"
	enrichAcceptSelection     stmt.ID = "enrichment.accept.selection"
	enrichAcceptOrigin        stmt.ID = "enrichment.accept.origin"
	enrichAcceptOriginSource  stmt.ID = "enrichment.accept.origin_source"
	enrichAcceptOwnerFinish   stmt.ID = "enrichment.accept.owner_finish"
	enrichAcceptRetain        stmt.ID = "enrichment.accept.retain_result"
	enrichAcceptReceipt       stmt.ID = "enrichment.accept.receipt"
	enrichAcceptRetireImage   stmt.ID = "enrichment.accept.retire_image"
	enrichAcceptStageCredit   stmt.ID = "enrichment.effective_stage.materialize_credit"
	enrichAcceptStagePrefix   stmt.ID = "enrichment.effective_stage.materialize_prefix"
)

// Every SQL string and parameter list is compiled here. No caller SQL, table,
// callback or registry extension is accepted by the command.
func enrichmentAcceptDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	num := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	blob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	point := `SELECT 1 FROM catalog_credits AS catalog_credits
 INNER JOIN credit_source_publication_members AS m ON m.publication_id=? AND m.position=catalog_credits.ord AND m.entity_id=catalog_credits.entity_id AND m.provider=catalog_credits.provider AND m.source_row_id=catalog_credits.source_row_id
 INNER JOIN credit_source_rows AS s ON s.source_row_id=catalog_credits.source_row_id AND s.entity_id=catalog_credits.entity_id AND s.provider=catalog_credits.provider
 INNER JOIN catalog_credit_labels AS rl ON rl.id=catalog_credits.role_id
 INNER JOIN catalog_credit_labels AS dl ON dl.id=catalog_credits.department_id`
	predicates := ` WHERE catalog_credits.image_id=? AND catalog_credits.ord=? AND catalog_credits.entity_id=? AND catalog_credits.role_id=? AND catalog_credits.department_id=?
 AND catalog_credits.credited_name=? AND catalog_credits.provider=? AND catalog_credits.credit_id=? AND catalog_credits.provider_person_id=? AND catalog_credits.source_ordinal=? AND catalog_credits.source_row_id=?
 AND catalog_credits.owner_entry_id IS NULL AND catalog_credits.owner_incarnation IS NULL AND catalog_credits.owner_revision IS NULL AND catalog_credits.source_status='current'
 AND catalog_credits.credited_name=s.original_credited_name AND catalog_credits.credit_id=s.original_credit_id AND catalog_credits.provider_person_id=s.original_provider_person_id
 AND catalog_credits.source_ordinal=s.original_source_ordinal AND rl.label=s.original_role AND dl.label=s.original_department`
	creditParams := []stmt.Param{text("publication"), num("image"), num("ordinal"), num("entity"), num("role"), num("department"), text("name"), text("provider"), text("credit"), text("provider_person"), num("source_ordinal"), text("source_row")}
	defs := []stmt.Definition{
		{ID: enrichAcceptImagePoint, Kind: stmt.Read, SQL: `SELECT 1 FROM catalog_credit_images WHERE id=? AND revision=? AND entity_id=? AND entity_incarnation=? AND source_revision=? AND identity_revision=? AND metadata_revision=? AND owner_revision=? AND policy_incarnation=? AND policy_revision=? AND consent_revision=? AND metadata_language=? AND source_manifest_digest=? AND build_request_digest=? AND manifest_algorithm=? AND build_expected_count=? AND build_expected_bytes=? AND build_expected_digest=? AND build_staged_count=? AND build_staged_bytes=? AND build_seed=? AND build_chain=? AND build_after_ord=? AND sealed=0 AND source_proof_owner=? AND source_proof_incarnation=? AND source_proof_sequence=? AND source_proof_publication_id=? AND source_proof_provider=?`, Params: []stmt.Param{num("image"), num("image_revision"), num("entity"), blob("entity_incarnation"), num("source_revision"), num("identity_revision"), num("metadata_revision"), num("owner_revision"), blob("policy_incarnation"), num("policy_revision"), num("consent_revision"), text("language"), blob("source_manifest"), blob("begin_request"), text("algorithm"), num("expected_count"), num("expected_bytes"), blob("expected_digest"), num("staged_count"), num("staged_bytes"), blob("seed"), blob("chain"), num("after_ord"), text("source_owner"), blob("source_incarnation"), num("source_sequence"), text("publication"), text("provider")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_credit_images"}}},
		{ID: enrichAcceptCreditPerson, Kind: stmt.Read, SQL: point + ` INNER JOIN catalog_people AS p ON p.id=catalog_credits.person_id` + predicates + ` AND catalog_credits.person_id=? AND s.original_target_kind='person' AND s.original_target_id=p.token AND s.original_person_token=p.token AND s.original_person_identity_key=p.identity_key`, Params: append(append([]stmt.Param{}, creditParams...), num("person")), Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_credits"}, Indexes: []string{"sqlite_autoindex_credit_source_rows_2", "sqlite_autoindex_catalog_credit_labels_1"}}},
		{ID: enrichAcceptCreditScoped, Kind: stmt.Read, SQL: point + predicates + ` AND catalog_credits.person_id IS NULL AND s.original_target_kind='scoped_credit' AND s.original_target_id=catalog_credits.credit_id`, Params: creditParams, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_credits"}, Indexes: []string{"sqlite_autoindex_credit_source_rows_2", "sqlite_autoindex_catalog_credit_labels_1"}}},
		{ID: enrichAcceptNoExtra, Kind: stmt.Read, SQL: `SELECT ord FROM catalog_credits WHERE image_id=? AND ord>=? ORDER BY ord LIMIT 1`, Params: []stmt.Param{num("image"), num("next_ordinal")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_credits"}}},
		{ID: enrichAcceptOwnerEntries, Kind: stmt.Read, SQL: `SELECT position FROM credit_owner_entries WHERE entity_id=? AND owner_incarnation=? AND owner_revision=? ORDER BY position LIMIT 1`, Params: []stmt.Param{num("entity"), blob("owner_incarnation"), num("owner_revision")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_credit_owner_entries_2"}}},
		{ID: enrichAcceptOtherProvider, Kind: stmt.Read, SQL: `SELECT entity_id FROM credit_source_heads WHERE entity_id=? AND provider<>? LIMIT 1`, Params: []stmt.Param{num("entity"), text("provider")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"credit_source_heads"}}},
		{ID: enrichAcceptSealImage, Kind: stmt.Write, SQL: `UPDATE catalog_credit_images SET revision=revision+1,build_verified_count=build_staged_count,build_verified_bytes=build_staged_bytes,member_count=build_staged_count,prepared_bytes=build_staged_bytes,manifest_digest=build_chain,sealed=1,remaining_rows=build_staged_count,remaining_bytes=build_staged_bytes WHERE id=? AND entity_id=? AND revision=? AND sealed=0 AND build_staged_count=build_expected_count AND build_staged_bytes=build_expected_bytes AND build_chain=build_expected_digest AND revision<9223372036854775807 RETURNING revision`, Params: []stmt.Param{num("image"), num("entity"), num("image_revision")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_credit_images"}}},
		{ID: enrichAcceptHeadEmpty, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET credit_image_id=?,revision=revision+1 WHERE id=? AND revision=? AND credit_image_id IS NULL AND metadata_incarnation=? AND source_revision=? AND identity_revision=? AND metadata_revision=? AND credit_owner_revision=? AND credit_owner_locked=0 AND retired=0 AND revision<9223372036854775807 RETURNING revision`, Params: []stmt.Param{num("image"), num("entity"), num("revision"), blob("incarnation"), num("source_revision"), num("identity_revision"), num("metadata_revision"), num("owner_revision")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}}},
		{ID: enrichAcceptHeadReplace, Kind: stmt.Write, SQL: `UPDATE catalog_entities SET credit_image_id=?,revision=revision+1 WHERE id=? AND revision=? AND credit_image_id=? AND metadata_incarnation=? AND source_revision=? AND identity_revision=? AND metadata_revision=? AND credit_owner_revision=? AND credit_owner_locked=0 AND retired=0 AND revision<9223372036854775807 RETURNING revision`, Params: []stmt.Param{num("image"), num("entity"), num("revision"), num("old_image"), blob("incarnation"), num("source_revision"), num("identity_revision"), num("metadata_revision"), num("owner_revision")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}}},
		{ID: enrichAcceptSourceFirst, Kind: stmt.Write, SQL: `INSERT INTO credit_source_heads(entity_id,provider,publication_id,revision) VALUES(?,?,?,?)`, Params: []stmt.Param{num("entity"), text("provider"), text("publication"), num("revision")}},
		{ID: enrichAcceptSourceReplace, Kind: stmt.Write, SQL: `UPDATE credit_source_heads SET publication_id=?,revision=revision+1 WHERE entity_id=? AND provider=? AND publication_id=? AND revision=? AND revision<9223372036854775807`, Params: []stmt.Param{text("publication"), num("entity"), text("provider"), text("old_publication"), num("source_head_revision")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"credit_source_heads"}}},
		{ID: enrichAcceptRetain, Kind: stmt.Write, SQL: `UPDATE enrichment_command_receipts SET proof_pin_count=proof_pin_count+1,proof_revision=proof_revision+1 WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='credit_source_seal' AND publication_id=? AND entity_id=? AND seal_provider=? AND proof_pin_count>0 AND proof_pin_count<9223372036854775807 AND proof_revision<9223372036854775807 AND proof_cleanup_after_sequence=0`, Params: []stmt.Param{text("source_owner"), blob("source_incarnation"), num("source_sequence"), text("publication"), num("entity"), text("provider")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"enrichment_command_receipts"}}},
		{ID: enrichAcceptRetireImage, Kind: stmt.Write, SQL: `UPDATE catalog_credit_images SET reclaim_state='ready',job_next_attempt_ms=? WHERE id=? AND entity_id=? AND sealed=1 AND reclaim_state='held'`, Params: []stmt.Param{num("applied_ms"), num("old_image"), num("entity")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_credit_images"}}},
	}
	for i := range defs {
		defs[i].Owner = "writer.enrichment"
		defs[i].CallSites = []string{"writer/enrichment_accept_body.go:applyEnrichAccept"}
		defs[i].Cohorts = []stmt.Cohort{{Name: "missing_captured_cohort", Bindings: missingSealBindings(defs[i].Params)}}
	}
	return defs
}

const (
	enrichAcceptEvidenceBytes  stmt.ID = "enrichment.accept.evidence_retained_bytes"
	enrichAcceptSourceBytes    stmt.ID = "enrichment.accept.source_retained_bytes"
	enrichAcceptNoCurrent      stmt.ID = "enrichment.accept.no_current_context"
	enrichAcceptOldContext     stmt.ID = "enrichment.accept.current_context"
	enrichAcceptReleaseCurrent stmt.ID = "enrichment.accept.release_current"
	enrichAcceptOldStage       stmt.ID = "enrichment.accept.retired_context"
	enrichAcceptDropBegin      stmt.ID = "enrichment.accept.drop_begin"
	enrichAcceptDropPin        stmt.ID = "enrichment.accept.drop_begin_pin"
)

func enrichmentAcceptWriteDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	num := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	blob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: enrichAcceptEvidenceBytes, Kind: stmt.Read, SQL: `SELECT seal_retained_bytes FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='evidence_seal'`, Params: []stmt.Param{text("owner"), blob("incarnation"), num("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"enrichment_command_receipts"}}},
		{ID: enrichAcceptSourceBytes, Kind: stmt.Read, SQL: `SELECT seal_retained_bytes FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='credit_source_seal'`, Params: []stmt.Param{text("owner"), blob("incarnation"), num("sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"enrichment_command_receipts"}}},

		{ID: enrichAcceptOldContext, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_provider_operations WHERE entity_id=? AND provider=? AND proof_current_slot=1 AND id=? AND command_incarnation=? AND generation=? AND credit_image_id=? AND proof_pin_units=? AND proof_reserved_bytes=? AND proof_stage_slot IS NULL AND proof_cleanup_phase='idle'`, Params: []stmt.Param{num("entity"), text("provider"), text("old_owner"), blob("old_incarnation"), num("generation"), num("old_image"), num("pin_units"), num("reserved_bytes")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_provider_operations_6"}}},
		{ID: enrichAcceptNoCurrent, Kind: stmt.Read, SQL: `SELECT 1 FROM metadata_provider_operations WHERE entity_id=? AND provider=? AND proof_current_slot=1`, Params: []stmt.Param{num("entity"), text("provider")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_provider_operations_6"}}},
		{ID: enrichAcceptReleaseCurrent, Kind: stmt.Write, SQL: `UPDATE metadata_provider_operations SET proof_current_slot=? WHERE id=? AND command_incarnation=? AND generation=? AND credit_image_id=? AND proof_pin_units=? AND proof_reserved_bytes=? AND proof_current_slot=1 AND proof_stage_slot IS NULL AND proof_cleanup_phase='idle'`, Params: []stmt.Param{stmt.Param{Name: "cleared_slot", Type: stmt.Integer, Nullable: true}, text("old_owner"), blob("old_incarnation"), num("old_generation"), num("old_image"), num("old_pin_units"), num("old_reserved_bytes")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_provider_operations_3"}}},
		{ID: enrichAcceptOldStage, Kind: stmt.Write, SQL: `UPDATE metadata_provider_operations SET proof_stage_slot=? WHERE id=? AND command_incarnation=? AND generation=? AND proof_current_slot IS NULL AND proof_stage_slot IS NULL`, Params: []stmt.Param{num("retired_stage_slot"), text("old_owner"), blob("old_incarnation"), num("old_generation")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_provider_operations_3"}}},
		{ID: enrichAcceptOwnerFinish, Kind: stmt.Write, SQL: `UPDATE metadata_provider_operations SET last_command_sequence=?,status='applied',proof_stage_slot=?,proof_current_slot=1 WHERE id=? AND command_incarnation=? AND generation=? AND entity_id=? AND provider=? AND credit_image_id=? AND last_command_sequence=? AND acknowledged_command_sequence=? AND phase='enrichment-v1' AND status='staged' AND lease=? AND lease_until=? AND source_publication_id=? AND proof_cleanup_phase='idle' AND proof_stage_slot=? AND proof_current_slot IS NULL AND proof_pin_units=2 AND proof_reserved_bytes>=?`, Params: []stmt.Param{num("sequence"), stmt.Param{Name: "cleared_stage", Type: stmt.Integer, Nullable: true}, text("owner"), blob("incarnation"), num("generation"), num("entity"), text("provider"), num("image"), num("last_sequence"), num("ack_sequence"), text("lease"), text("lease_until"), text("publication"), num("stage_slot"), num("full_reserved_bytes")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_provider_operations_5"}}},
		{ID: enrichAcceptSelection, Kind: stmt.Write, SQL: `UPDATE metadata_subject_providers SET accepted_evidence_id=?,revision=revision+1 WHERE entity_id=? AND provider=? AND role='primary' AND incarnation=? AND revision=? AND subject_kind='movie' AND provider_id=? AND status='selected' AND accepted_set_id IS NULL AND accepted_publication_id IS NULL AND accepted_track_id IS NULL AND revision<9223372036854775807`, Params: []stmt.Param{text("evidence"), num("entity"), text("provider"), blob("selection_incarnation"), num("selection_revision"), text("provider_id")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_subject_providers"}}},
		{ID: enrichAcceptOriginSource, Kind: stmt.Read, SQL: `SELECT subject_kind,provider_id,provider,source_url,observed_at,language,region FROM metadata_evidence WHERE id=? AND sealed=1`, Params: []stmt.Param{text("evidence")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_evidence_1"}}},
		{ID: enrichAcceptOrigin, Kind: stmt.Write, SQL: `INSERT INTO metadata_field_origins(subject_kind,provider_id,entity_id,field,provider,source_kind,source_url,confidence,observed_at,language,region,selection_revision,publication_id,evidence_id) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(entity_id,field) DO UPDATE SET subject_kind=excluded.subject_kind,provider_id=excluded.provider_id,provider=excluded.provider,source_kind=excluded.source_kind,source_url=excluded.source_url,confidence=excluded.confidence,observed_at=excluded.observed_at,language=excluded.language,region=excluded.region,selection_revision=excluded.selection_revision,publication_id=excluded.publication_id,evidence_id=excluded.evidence_id`, Params: []stmt.Param{text("subject_kind"), text("provider_id"), num("entity"), text("field"), text("provider"), text("source_kind"), text("source_url"), stmt.Param{Name: "confidence", Type: stmt.Real, Nullable: false}, text("observed_at"), text("language"), text("region"), num("selection_revision"), stmt.Param{Name: "publication", Type: stmt.Text, Nullable: true}, text("evidence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"metadata_field_origins"}}},
		{ID: enrichAcceptReceipt, Kind: stmt.Write, SQL: `INSERT INTO enrichment_command_receipts(provider_operation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,accepted_revision,applied_ms,entity_id,after_item,after_ordinal,effective_image_id,effective_image_revision,result_count,result_digest,source_dependency_owner,source_dependency_incarnation,source_dependency_sequence,source_dependency_publication_id,source_dependency_provider) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{text("owner"), blob("incarnation"), num("sequence"), blob("operation"), blob("request"), text("kind"), num("accepted_revision"), num("applied_ms"), num("entity"), text("after_item"), num("next_ordinal"), num("image"), num("image_revision"), num("count"), blob("result"), text("source_owner"), blob("source_incarnation"), num("source_sequence"), text("source_publication"), text("source_provider")}},
		{ID: enrichAcceptDropBegin, Kind: stmt.Write, SQL: `DELETE FROM enrichment_command_receipts WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='effective_stage' AND request_digest=? AND effective_image_id=? AND result_digest=? AND source_dependency_owner=? AND source_dependency_incarnation=? AND source_dependency_sequence=?`, Params: []stmt.Param{text("owner"), blob("incarnation"), num("sequence"), blob("begin_request"), num("image"), blob("begin_result"), text("source_owner"), blob("source_incarnation"), num("source_sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"enrichment_command_receipts"}}},
		{ID: enrichAcceptDropPin, Kind: stmt.Write, SQL: `UPDATE enrichment_command_receipts SET proof_pin_count=proof_pin_count-1,proof_revision=proof_revision+1 WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='credit_source_seal' AND proof_pin_count>1 AND proof_revision<9223372036854775807`, Params: []stmt.Param{text("source_owner"), blob("source_incarnation"), num("source_sequence")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"enrichment_command_receipts"}}},
	}
	for i := range defs {
		defs[i].Owner = "writer.enrichment"
		defs[i].CallSites = []string{"writer/enrichment_accept_body.go:applyEnrichAccept"}
		defs[i].Cohorts = []stmt.Cohort{{Name: "missing_captured_owner", Bindings: missingSealBindings(defs[i].Params)}}
	}
	return defs
}

// Exact maximum uses of every compiled statement inside ONE EnrichAccept
// command. A missing or understated entry fails closed at freeze/turn time; it
// never silently reuses another command's reservation.
func enrichmentAcceptMaxUses(id stmt.ID) int {
	switch id {
	case enrichAcceptCreditPerson, enrichAcceptCreditScoped:
		return MaxCastCredits + MaxCrewCredits
	case effectiveSourcePinReceiptLengths, effectiveSourcePinReceiptCopy:
		return 2 // the ORIGINAL evidence and source seal receipts are distinct
	case sealHistoryOwner, sealHistoryHeader, sealHistoryPayload, sealReceiptLengths, enrichmentDisclosureOwner,
		effectiveDisclosureLibrary, effectiveDisclosureSubject, effectiveReceiptAck,
		effectiveSourcePinEvidenceLengths, effectiveSourcePinPublicationLengths,
		effectiveSourcePinEvidenceCopy, effectiveSourcePinPublicationCopy,
		effectiveSourceBuildControl, effectiveSourceCustodyControl,
		enrichmentEffectiveOwner, enrichmentEffectiveReceipt, enrichmentEffectiveEntity,
		enrichmentEffectiveInsert, enrichmentEffectiveAttach, enrichmentEffectiveRecord,
		enrichmentEffectiveLibrary, enrichmentEffectiveLanguage, enrichmentEffectivePolicy,
		enrichmentEffectiveProvider, enrichmentEffectiveConsent, enrichmentEffectiveSelection,
		enrichmentEffectiveEvidence, enrichmentEffectiveSubject, enrichmentEffectivePublication,
		enrichmentEffectiveCreditHead, enrichmentEffectiveCapture, enrichmentEffectiveAssociation,
		enrichmentEffectiveAsset, enrichmentEffectiveObject, enrichmentEffectiveSource,
		enrichAcceptOriginSource, enrichAcceptImagePoint, enrichAcceptNoExtra, enrichAcceptOwnerEntries, enrichAcceptOtherProvider,
		enrichAcceptNoCurrent, enrichAcceptOldContext, enrichAcceptEvidenceBytes, enrichAcceptSourceBytes,
		enrichAcceptSealImage, enrichAcceptHeadEmpty, enrichAcceptHeadReplace, enrichAcceptSourceFirst,
		enrichAcceptSourceReplace, enrichAcceptSelection, enrichAcceptOrigin, enrichAcceptOwnerFinish,
		enrichAcceptRetain, enrichAcceptReceipt, enrichAcceptRetireImage, enrichAcceptReleaseCurrent,
		enrichAcceptOldStage, enrichAcceptDropBegin, enrichAcceptDropPin:
		return 1
	default:
		return 0
	}
}

// The accept profile reuses the merged history/disclosure points and adds only
// the materialized-image, head-flip and retained-source-proof points.
func enrichmentAcceptCommandDefinitions() []stmt.Definition {
	defs := enrichmentSealHistoryCommandDefinitions()
	defs = append(defs, enrichmentEffectiveBeginDefinitions()...)
	defs = append(defs, enrichmentEffectiveProducerDefinitions()...)
	defs = append(defs, enrichmentAcceptDefinitions()...)
	defs = append(defs, enrichmentAcceptWriteDefinitions()...)
	defs = append(defs, enrichmentEffectiveSourcePinDefinitions()...)
	defs = append(defs, enrichmentEffectiveSourceControlDefinitions()...)
	return defs
}
