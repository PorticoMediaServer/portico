package writer

import (
	"context"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	effectiveBeginReservation   stmt.ID = "enrichment.effective_stage.proof_reservation"
	effectiveBeginSourceRetain  stmt.ID = "enrichment.effective_stage.retain_source"
	effectiveBeginImageSource   stmt.ID = "enrichment.effective_stage.image_source"
	effectiveBeginReceiptSource stmt.ID = "enrichment.effective_stage.receipt_source"
)

// The image and the unresolved historical result each retain the EXACT source
// proof. These are two incoming physical references to the same charged cohort,
// not two extra distinct proof units. The existing producer reservation must
// already cover both full E/S closures; BEGIN neither invents nor refunds it.
func enrichmentEffectiveVerifiedDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	blob := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	defs := []stmt.Definition{
		{ID: effectiveBeginReservation, Kind: stmt.Read, SQL: `SELECT proof_reserved_bytes FROM metadata_provider_operations INDEXED BY sqlite_autoindex_metadata_provider_operations_1
 WHERE id=? AND command_incarnation=? AND entity_id=? AND provider=? AND generation=?
 AND source_publication_id=? AND source_content_revision=? AND proof_cleanup_phase='idle'
 AND proof_stage_slot IS NOT NULL AND proof_current_slot IS NULL AND proof_pin_units>=2 AND proof_reserved_bytes>=?`,
			Params: []stmt.Param{text("owner"), blob("owner_incarnation"), integer("entity"), text("provider"), integer("owner_generation"), text("publication"), integer("source_revision"), integer("full_reserved_bytes")},
			Plan:   stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_provider_operations_1"}, Seeks: []string{"id=?"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "metadata_provider_operations", Name: "sqlite_autoindex_metadata_provider_operations_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}},

		{ID: effectiveBeginSourceRetain, SQL: `UPDATE enrichment_command_receipts SET proof_pin_count=proof_pin_count+2,proof_revision=proof_revision+1
 WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='credit_source_seal'
 AND publication_id=? AND entity_id=? AND seal_provider=? AND proof_revision=? AND proof_pin_count=?
 AND proof_pin_count>0 AND proof_pin_count<=9223372036854775805 AND proof_revision<9223372036854775807 AND proof_cleanup_after_sequence=0`,
			Params: []stmt.Param{text("source_owner"), blob("source_incarnation"), integer("source_sequence"), text("publication"), integer("entity"), text("provider"), integer("proof_revision"), integer("proof_pins")},
			Plan:   stmt.Plan{Hot: true, PrimaryKeys: []string{"enrichment_command_receipts"}}},
		{ID: effectiveBeginImageSource, SQL: `UPDATE catalog_credit_images SET source_proof_owner=?,source_proof_incarnation=?,source_proof_sequence=?,source_proof_publication_id=?,source_proof_provider=?
 WHERE id=? AND entity_id=? AND revision=? AND sealed=0 AND source_proof_owner IS NULL`,
			Params: []stmt.Param{text("source_owner"), blob("source_incarnation"), integer("source_sequence"), text("source_publication"), text("source_provider"), integer("image"), integer("entity"), integer("image_revision")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_credit_images"}, Seeks: []string{"rowid=?"}}},
		{ID: effectiveBeginReceiptSource, SQL: `UPDATE enrichment_command_receipts SET source_dependency_owner=?,source_dependency_incarnation=?,source_dependency_sequence=?,source_dependency_publication_id=?,source_dependency_provider=?
 WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='effective_stage'
 AND operation_id=? AND request_digest=? AND effective_image_id=? AND effective_image_revision=? AND entity_id=? AND result_digest=? AND source_dependency_owner IS NULL`,
			Params: []stmt.Param{text("source_owner"), blob("source_incarnation"), integer("source_sequence"), text("source_publication"), text("source_provider"), text("owner"), blob("incarnation"), integer("sequence"), blob("operation"), blob("request"), integer("image"), integer("image_revision"), integer("entity"), blob("result")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"enrichment_command_receipts"}, Seeks: []string{"provider_operation_id=? AND command_incarnation=? AND command_sequence=?"}}},
	}
	for i := range defs {
		defs[i].Owner = "writer.enrichment"
		if defs[i].ID != effectiveBeginReservation {
			defs[i].Kind = stmt.Write
		}
		defs[i].CallSites = []string{"writer/enrichment_effective_verified_writes.go:retainEffectiveBeginSource", "writer/enrichment_effective_verified_writes.go:attachEffectiveBeginReceiptSource"}
		defs[i].Cohorts = []stmt.Cohort{{Name: "missing_native_proof", Bindings: missingSealBindings(defs[i].Params)}}
	}
	return defs
}

func effectiveBeginSourceValues(input *effectiveStageBeginCommand, proof *effectiveVerifiedSource) []any {
	s := proof.source.saved.facts
	ref := input.sourceReferences.source
	return []any{ref.owner, ref.receipt.Identity.Incarnation[:], ref.receipt.Identity.Sequence, s.PublicationID.Value, s.Provider}
}

func retainEffectiveBeginSource(ctx context.Context, turn *commandTurn, input *effectiveStageBeginCommand, proof *effectiveVerifiedSource, image effectiveBeginImage) error {
	if err := checkEffectiveVerifiedSource(ctx, turn, input, proof); err != nil {
		return err
	}
	e, s := proof.evidence.saved.facts, proof.source.saved.facts
	if image.id <= 0 || image.revision <= 0 || e.RetainedBytes <= 0 || s.RetainedBytes <= 0 || e.RetainedBytes > math.MaxInt64-s.RetainedBytes {
		return ErrInvalid
	}
	ref := input.sourceReferences.source
	required := e.RetainedBytes + s.RetainedBytes
	step, err := sealStep(turn, effectiveBeginReservation)
	if err != nil {
		return err
	}
	row, err := turn.queryRow(ctx, step, input.request.ownerID, input.identity.Incarnation[:], input.request.entity.id, input.request.owner.provider, input.request.owner.generation, s.PublicationID.Value, s.ContentRevision, required)
	if err != nil {
		return err
	}
	var reserved int64
	if err = row.Scan(&reserved); err != nil {
		return ErrConflict
	}
	if reserved < required {
		return ErrConflict
	}
	args := []any{ref.owner, ref.receipt.Identity.Incarnation[:], ref.receipt.Identity.Sequence, s.PublicationID.Value, s.EntityID, s.Provider, proof.source.proof.revision, proof.source.proof.pinCount}
	if err := execSealOne(ctx, turn, effectiveBeginSourceRetain, args...); err != nil {
		return err
	}
	args = append(effectiveBeginSourceValues(input, proof), image.id, input.request.entity.id, image.revision)
	return execSealOne(ctx, turn, effectiveBeginImageSource, args...)
}

func attachEffectiveBeginReceiptSource(ctx context.Context, turn *commandTurn, input *effectiveStageBeginCommand, proof *effectiveVerifiedSource, image effectiveBeginImage, result ResultDigest) error {
	if err := checkEffectiveVerifiedSource(ctx, turn, input, proof); err != nil {
		return err
	}
	args := append(effectiveBeginSourceValues(input, proof), input.request.ownerID, input.identity.Incarnation[:], input.identity.Sequence, input.identity.OperationID[:], input.digest[:], image.id, image.revision, input.request.entity.id, result[:])
	return execSealOne(ctx, turn, effectiveBeginReceiptSource, args...)
}
