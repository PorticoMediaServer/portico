package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	enrichmentEffectiveOwner   stmt.ID = "enrichment.effective_stage.owner"
	enrichmentEffectiveReceipt stmt.ID = "enrichment.effective_stage.receipt"
	enrichmentEffectiveEntity  stmt.ID = "enrichment.effective_stage.entity"
	enrichmentEffectiveInsert  stmt.ID = "enrichment.effective_stage.insert_header"
	enrichmentEffectiveAttach  stmt.ID = "enrichment.effective_stage.attach"
	enrichmentEffectiveRecord  stmt.ID = "enrichment.effective_stage.insert_receipt"
)

// W composes these finite statements into the sole registry and recipe. Nothing
// here constructs a registry/turn, opens a connection or grants execution.
func enrichmentEffectiveBeginDefinitions() []stmt.Definition {
	return []stmt.Definition{
		{
			ID: enrichmentEffectiveOwner, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_facts.go:effectiveBeginOwnerFacts"},
			SQL: `SELECT command_incarnation,last_command_sequence,acknowledged_command_sequence,
 entity_id,provider,phase,status,generation,lease,lease_until,entity_incarnation,
 source_revision,identity_revision,metadata_revision,hierarchy_revision,selection_revision,
 descriptive_revision,policy_incarnation,policy_revision,consent_revision,algorithm,
 refresh_mode,input_digest,selected_id,page,after_item,credit_image_id
 FROM metadata_provider_operations WHERE id=?`,
			Params: []stmt.Param{{Name: "provider_operation_id", Type: stmt.Text}},
			Cohorts: []stmt.Cohort{
				{Name: "owner_present", Bindings: []any{"operation-one"}},
				{Name: "owner_missing", Bindings: []any{"operation-missing"}},
			},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_provider_operations_1"}, Seeks: []string{"id=?"}},
		},
		{
			ID: enrichmentEffectiveReceipt, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_facts.go:effectiveBeginReceiptFacts"},
			SQL: `SELECT command_incarnation,command_sequence,operation_id,request_digest,command_kind,
 accepted_revision,applied_ms,entity_id,after_item,after_ordinal,publication_id,
 effective_image_id,effective_image_revision,result_count,result_digest
 FROM enrichment_command_receipts
 WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=?`,
			Params: []stmt.Param{
				{Name: "provider_operation_id", Type: stmt.Text},
				{Name: "command_incarnation", Type: stmt.Blob},
				{Name: "command_sequence", Type: stmt.Integer},
			},
			Cohorts: []stmt.Cohort{
				{Name: "receipt_present", Bindings: []any{"operation-one", []byte{16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31}, int64(11)}},
				{Name: "receipt_missing", Bindings: []any{"operation-one", []byte{16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31}, int64(12)}},
			},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"enrichment_command_receipts"}, Seeks: []string{"provider_operation_id=? AND command_incarnation=? AND command_sequence=?"}},
		},
		{
			ID: enrichmentEffectiveEntity, Owner: "writer.enrichment", Kind: stmt.Read,
			CallSites: []string{"writer/enrichment_effective_begin_facts.go:effectiveBeginEntityFacts"},
			SQL: `SELECT id,library_id,kind,retired,revision,metadata_incarnation,
 source_revision,identity_revision,metadata_revision,metadata_language,
 credit_owner_revision,credit_owner_locked,credit_image_id
 FROM catalog_entities WHERE id=?`,
			Params: []stmt.Param{{Name: "entity_id", Type: stmt.Integer}},
			Cohorts: []stmt.Cohort{
				{Name: "entity_present", Bindings: []any{int64(101)}},
				{Name: "entity_missing", Bindings: []any{int64(102)}},
			},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}},
		},
		{
			ID: enrichmentEffectiveInsert, Owner: "writer.enrichment", Kind: stmt.Write,
			CallSites: []string{"writer/enrichment_effective_begin_values.go:effectiveBeginHeaderValues"},
			SQL: `INSERT INTO catalog_credit_images(
 entity_id,entity_incarnation,source_revision,identity_revision,metadata_revision,owner_revision,
 policy_incarnation,policy_revision,consent_revision,metadata_language,source_manifest_digest,
 build_request_digest,manifest_algorithm,build_expected_count,build_expected_bytes,build_expected_digest,
 build_staged_count,build_staged_bytes,build_verified_count,build_verified_bytes,build_seed,build_chain,
 build_after_ord,member_count,prepared_bytes,manifest_digest,current_input_digest,sealed,
 remaining_rows,remaining_bytes,reclaim_state,reclaim_after_ord,command_incarnation,
 last_command_sequence,acknowledged_command_sequence,job_revision,job_lease_owner,
 job_lease_generation,job_lease_expires_ms,job_attempts,job_next_attempt_ms
 ) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
 RETURNING id,revision`,
			Params: effectiveBeginHeaderParameters(),
			Cohorts: []stmt.Cohort{{Name: "hidden_header", Bindings: effectiveBeginHeaderValues(
				effectiveBeginHeader{entityID: 101, sourceRevision: 13, identityRevision: 17, metadataRevision: 19,
					ownerRevision: 23, policyRevision: 37, consentRevision: 41, language: "en-CA", expectedCount: 150, expectedBytes: 12345},
				RequestDigest{}, [32]byte{}, [16]byte{})}},
			Plan: stmt.Plan{Hot: true},
		},
		{
			ID: enrichmentEffectiveAttach, Owner: "writer.enrichment", Kind: stmt.Write,
			CallSites: []string{"writer/enrichment_effective_begin_statements.go:enrichmentEffectiveBeginDefinitions"},
			SQL: `UPDATE metadata_provider_operations SET credit_image_id=?,last_command_sequence=?
 WHERE id=? AND command_incarnation=? AND last_command_sequence=? AND acknowledged_command_sequence=?
 AND credit_image_id IS NULL AND entity_id=? AND generation=? AND phase=? AND status=?
 AND lease=? AND lease_until=? AND page=? AND after_item=?`,
			Params: []stmt.Param{
				{Name: "new_image_id", Type: stmt.Integer}, {Name: "new_sequence", Type: stmt.Integer},
				{Name: "provider_operation_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob},
				{Name: "last_sequence", Type: stmt.Integer}, {Name: "acknowledged_sequence", Type: stmt.Integer},
				{Name: "entity_id", Type: stmt.Integer}, {Name: "generation", Type: stmt.Integer},
				{Name: "phase", Type: stmt.Text}, {Name: "status", Type: stmt.Text},
				{Name: "lease", Type: stmt.Text}, {Name: "lease_until", Type: stmt.Text},
				{Name: "page", Type: stmt.Integer}, {Name: "after_item", Type: stmt.Text},
			},
			Cohorts: []stmt.Cohort{{Name: "exact_owner_cas", Bindings: []any{int64(307), int64(12), "operation-one", make([]byte, 16), int64(11), int64(11), int64(101), int64(31), "unadmitted-fixture-phase", "staged", "native-lease", "2026-11-05T12:00:00.000000000Z", int64(59), "opaque-page:61"}}},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_provider_operations_3"}, Seeks: []string{"id=? AND command_incarnation=?"}},
		},
		{
			ID: enrichmentEffectiveRecord, Owner: "writer.enrichment", Kind: stmt.Write,
			CallSites: []string{"writer/enrichment_effective_begin_statements.go:enrichmentEffectiveBeginDefinitions"},
			SQL: `INSERT INTO enrichment_command_receipts(
 provider_operation_id,command_incarnation,command_sequence,operation_id,request_digest,command_kind,
 accepted_revision,applied_ms,entity_id,after_item,after_ordinal,publication_id,
 effective_image_id,effective_image_revision,result_count,result_digest
 ) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{
				{Name: "provider_operation_id", Type: stmt.Text}, {Name: "command_incarnation", Type: stmt.Blob},
				{Name: "command_sequence", Type: stmt.Integer}, {Name: "operation_id", Type: stmt.Blob},
				{Name: "request_digest", Type: stmt.Blob}, {Name: "command_kind", Type: stmt.Text},
				{Name: "observed_catalog_revision", Type: stmt.Integer}, {Name: "applied_ms", Type: stmt.Integer},
				{Name: "entity_id", Type: stmt.Integer}, {Name: "after_item", Type: stmt.Text},
				{Name: "next_ordinal", Type: stmt.Integer}, {Name: "publication_id", Type: stmt.Text, Nullable: true},
				{Name: "image_id", Type: stmt.Integer}, {Name: "image_revision", Type: stmt.Integer},
				{Name: "result_count", Type: stmt.Integer}, {Name: "result_digest", Type: stmt.Blob},
			},
			Cohorts: []stmt.Cohort{{Name: "scalar_hidden_begin", Bindings: []any{"operation-one", make([]byte, 16), int64(12), make([]byte, 16), make([]byte, 32), "effective_stage", int64(203), int64(1793861096027), int64(101), "opaque-page:61", int64(0), nil, int64(307), int64(409), int64(0), make([]byte, 32)}}},
			Plan:    stmt.Plan{Hot: true},
		},
	}
}

func effectiveBeginHeaderParameters() []stmt.Param {
	return []stmt.Param{
		{Name: "entity_id", Type: stmt.Integer}, {Name: "entity_incarnation", Type: stmt.Blob},
		{Name: "source_revision", Type: stmt.Integer}, {Name: "identity_revision", Type: stmt.Integer},
		{Name: "metadata_revision", Type: stmt.Integer}, {Name: "owner_revision", Type: stmt.Integer},
		{Name: "policy_incarnation", Type: stmt.Blob}, {Name: "policy_revision", Type: stmt.Integer},
		{Name: "consent_revision", Type: stmt.Integer}, {Name: "metadata_language", Type: stmt.Text},
		{Name: "source_manifest_digest", Type: stmt.Blob}, {Name: "build_request_digest", Type: stmt.Blob},
		{Name: "manifest_algorithm", Type: stmt.Text}, {Name: "expected_count", Type: stmt.Integer},
		{Name: "expected_bytes", Type: stmt.Integer}, {Name: "expected_digest", Type: stmt.Blob},
		{Name: "staged_count", Type: stmt.Integer}, {Name: "staged_bytes", Type: stmt.Integer},
		{Name: "verified_count", Type: stmt.Integer}, {Name: "verified_bytes", Type: stmt.Integer},
		{Name: "seed", Type: stmt.Blob}, {Name: "chain", Type: stmt.Blob},
		{Name: "after_ord", Type: stmt.Integer}, {Name: "member_count", Type: stmt.Integer},
		{Name: "prepared_bytes", Type: stmt.Integer}, {Name: "manifest_digest", Type: stmt.Blob, Nullable: true},
		{Name: "current_input_digest", Type: stmt.Blob}, {Name: "sealed", Type: stmt.Integer},
		{Name: "remaining_rows", Type: stmt.Integer}, {Name: "remaining_bytes", Type: stmt.Integer},
		{Name: "reclaim_state", Type: stmt.Text}, {Name: "reclaim_after_ord", Type: stmt.Integer},
		{Name: "retirement_incarnation", Type: stmt.Blob}, {Name: "retirement_last_sequence", Type: stmt.Integer},
		{Name: "retirement_acknowledged_sequence", Type: stmt.Integer}, {Name: "retirement_revision", Type: stmt.Integer},
		{Name: "retirement_lease_owner", Type: stmt.Integer}, {Name: "retirement_lease_generation", Type: stmt.Integer},
		{Name: "retirement_lease_expires_ms", Type: stmt.Integer}, {Name: "retirement_attempts", Type: stmt.Integer},
		{Name: "retirement_next_attempt_ms", Type: stmt.Integer, Nullable: true},
	}
}
