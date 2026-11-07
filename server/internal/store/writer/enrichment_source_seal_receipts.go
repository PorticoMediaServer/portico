package writer

import (
	"bytes"
	"context"
	"encoding/hex"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	evidenceSealReceiptInsert stmt.ID = "enrichment.evidence_seal.receipt"
	sourceSealReceiptInsert   stmt.ID = "enrichment.credit_source_seal.receipt"
	evidenceSealBodyAttach    stmt.ID = "enrichment.evidence_seal.attach_body"
	sourceSealBodyAttach      stmt.ID = "enrichment.credit_source_seal.attach_body"
	sourceSealUpstreamPin     stmt.ID = "enrichment.credit_source_seal.upstream_pin"
	evidenceSealBuildFinalize stmt.ID = "enrichment.evidence_seal.finalize_prefix"
)

// W provides its CLOSED result digest after actual RETURNING facts, never the
// request/observation/normalized commitment. These private bodies neither mint
// that digest nor return Applied. All steps, last/receipt and terminal remain
// one W transaction; any failed link/pin/accounting rolls everything back.
type sealReceiptContent struct {
	header        EvidenceHeaderInput
	manifest      normalizedSealManifest
	retainedBytes int64 // trusted complete prospective W forecast, not a limit
}

func enrichmentSealReceiptDefinitions() []stmt.Definition {
	defs := []stmt.Definition{
		{ID: evidenceSealReceiptInsert, Owner: "writer.enrichment", Kind: stmt.Write,
			SQL: `INSERT INTO enrichment_command_receipts(provider_operation_id,command_incarnation,command_sequence,operation_id,request_digest,accepted_revision,applied_ms,entity_id,after_item,after_ordinal,result_digest,seal_version,seal_evidence_id,seal_subject_id,seal_parser_version,seal_provider,seal_subject_kind,seal_provider_id,seal_language,seal_region,seal_request_shape,seal_observation_digest,seal_owner_generation,seal_content_revision,seal_algorithm,seal_commitment,seal_verified_rows,seal_canonical_bytes,seal_retained_bytes,seal_build_id,seal_build_generation,command_kind,result_count,proof_pin_count) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{
				{Name: "provider_operation_id", Type: stmt.Text},
				{Name: "command_incarnation", Type: stmt.Blob},
				{Name: "command_sequence", Type: stmt.Integer},
				{Name: "operation_id", Type: stmt.Blob},
				{Name: "request_digest", Type: stmt.Blob},
				{Name: "accepted_revision", Type: stmt.Integer},
				{Name: "applied_ms", Type: stmt.Integer},
				{Name: "entity_id", Type: stmt.Integer},
				{Name: "result_after_item", Type: stmt.Text},
				{Name: "after_ordinal", Type: stmt.Integer},
				{Name: "result_digest", Type: stmt.Blob},
				{Name: "seal_version", Type: stmt.Integer},
				{Name: "seal_evidence_id", Type: stmt.Text},
				{Name: "seal_subject_id", Type: stmt.Integer},
				{Name: "seal_parser_version", Type: stmt.Text},
				{Name: "seal_provider", Type: stmt.Text},
				{Name: "seal_subject_kind", Type: stmt.Text},
				{Name: "seal_provider_id", Type: stmt.Text},
				{Name: "seal_language", Type: stmt.Text},
				{Name: "seal_region", Type: stmt.Text},
				{Name: "seal_request_shape", Type: stmt.Text},
				{Name: "seal_observation_digest", Type: stmt.Text},
				{Name: "seal_owner_generation", Type: stmt.Integer},
				{Name: "seal_content_revision", Type: stmt.Integer},
				{Name: "seal_algorithm", Type: stmt.Text},
				{Name: "seal_commitment", Type: stmt.Blob},
				{Name: "seal_verified_rows", Type: stmt.Integer},
				{Name: "seal_canonical_bytes", Type: stmt.Integer},
				{Name: "seal_retained_bytes", Type: stmt.Integer},
				{Name: "seal_build_id", Type: stmt.Text},
				{Name: "seal_build_generation", Type: stmt.Integer},
				{Name: "native_kind", Type: stmt.Text}, {Name: "native_count", Type: stmt.Integer}, {Name: "native_pins", Type: stmt.Integer},
			}},
		{ID: sourceSealReceiptInsert, Owner: "writer.enrichment", Kind: stmt.Write,
			SQL: `INSERT INTO enrichment_command_receipts(provider_operation_id,command_incarnation,command_sequence,operation_id,request_digest,accepted_revision,applied_ms,entity_id,after_item,after_ordinal,result_digest,seal_version,seal_evidence_id,seal_subject_id,seal_parser_version,seal_provider,seal_subject_kind,seal_provider_id,seal_language,seal_region,seal_request_shape,seal_observation_digest,seal_owner_generation,seal_content_revision,seal_algorithm,seal_commitment,seal_verified_rows,seal_canonical_bytes,seal_retained_bytes,publication_id,upstream_evidence_owner,upstream_evidence_incarnation,upstream_evidence_sequence,command_kind,result_count,proof_pin_count) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			Params: []stmt.Param{
				{Name: "provider_operation_id", Type: stmt.Text},
				{Name: "command_incarnation", Type: stmt.Blob},
				{Name: "command_sequence", Type: stmt.Integer},
				{Name: "operation_id", Type: stmt.Blob},
				{Name: "request_digest", Type: stmt.Blob},
				{Name: "accepted_revision", Type: stmt.Integer},
				{Name: "applied_ms", Type: stmt.Integer},
				{Name: "entity_id", Type: stmt.Integer},
				{Name: "result_after_item", Type: stmt.Text},
				{Name: "after_ordinal", Type: stmt.Integer},
				{Name: "result_digest", Type: stmt.Blob},
				{Name: "seal_version", Type: stmt.Integer},
				{Name: "seal_evidence_id", Type: stmt.Text},
				{Name: "seal_subject_id", Type: stmt.Integer},
				{Name: "seal_parser_version", Type: stmt.Text},
				{Name: "seal_provider", Type: stmt.Text},
				{Name: "seal_subject_kind", Type: stmt.Text},
				{Name: "seal_provider_id", Type: stmt.Text},
				{Name: "seal_language", Type: stmt.Text},
				{Name: "seal_region", Type: stmt.Text},
				{Name: "seal_request_shape", Type: stmt.Text},
				{Name: "seal_observation_digest", Type: stmt.Text},
				{Name: "seal_owner_generation", Type: stmt.Integer},
				{Name: "seal_content_revision", Type: stmt.Integer},
				{Name: "seal_algorithm", Type: stmt.Text},
				{Name: "seal_commitment", Type: stmt.Blob},
				{Name: "seal_verified_rows", Type: stmt.Integer},
				{Name: "seal_canonical_bytes", Type: stmt.Integer},
				{Name: "seal_retained_bytes", Type: stmt.Integer},
				{Name: "publication_id", Type: stmt.Text},
				{Name: "upstream_evidence_owner", Type: stmt.Text},
				{Name: "upstream_evidence_incarnation", Type: stmt.Blob},
				{Name: "upstream_evidence_sequence", Type: stmt.Integer},
				{Name: "native_kind", Type: stmt.Text}, {Name: "native_count", Type: stmt.Integer}, {Name: "native_pins", Type: stmt.Integer},
			}},
		{ID: evidenceSealBodyAttach, Owner: "writer.enrichment", Kind: stmt.Write,
			SQL: `UPDATE metadata_evidence SET sealed=1,seal_proof_owner=?,seal_proof_incarnation=?,seal_proof_sequence=?
 WHERE id=? AND subject_id=? AND parser_version=? AND sealed=0 AND seal_proof_owner IS NULL`,
			Params: []stmt.Param{{Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}, {Name: "evidence", Type: stmt.Text}, {Name: "subject", Type: stmt.Integer}, {Name: "parser", Type: stmt.Text}}},
		{ID: sourceSealBodyAttach, Owner: "writer.enrichment", Kind: stmt.Write,
			SQL: `UPDATE credit_source_publications SET sealed=1,seal_proof_owner=?,seal_proof_incarnation=?,seal_proof_sequence=?
 WHERE publication_id=? AND entity_id=? AND provider=? AND member_count=? AND sealed=0 AND seal_proof_owner IS NULL`,
			Params: []stmt.Param{{Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}, {Name: "publication", Type: stmt.Text}, {Name: "entity", Type: stmt.Integer}, {Name: "provider", Type: stmt.Text}, {Name: "count", Type: stmt.Integer}}},
		{ID: sourceSealUpstreamPin, Owner: "writer.enrichment", Kind: stmt.Write,
			SQL: `UPDATE enrichment_command_receipts SET proof_pin_count=proof_pin_count+1,proof_revision=proof_revision+1
 WHERE provider_operation_id=? AND command_incarnation=? AND command_sequence=? AND command_kind='evidence_seal'
 AND seal_evidence_id=? AND seal_subject_id=? AND seal_parser_version=? AND proof_revision=? AND proof_pin_count=? AND proof_pin_count>0 AND proof_revision<9223372036854775807 AND proof_pin_count<9223372036854775807`,
			Params: []stmt.Param{{Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "sequence", Type: stmt.Integer}, {Name: "evidence", Type: stmt.Text}, {Name: "subject", Type: stmt.Integer}, {Name: "parser", Type: stmt.Text}, {Name: "proof_revision", Type: stmt.Integer}, {Name: "proof_pin_count", Type: stmt.Integer}}},
	}

	defs = append(defs, stmt.Definition{ID: evidenceSealBuildFinalize, Owner: "writer.enrichment", Kind: stmt.Write,
		SQL: `UPDATE metadata_evidence_builds SET materialized_charge=materialized_charge+?,materialized_digest=?
 WHERE id=? AND evidence_id=? AND generation=? AND content_revision=? AND cursor=? AND materialized_rows=cursor AND expected_rows=cursor AND materialized_charge=?`,
		Params: []stmt.Param{{Name: "proof_bytes_charge", Type: stmt.Integer}, {Name: "manifest", Type: stmt.Text}, {Name: "build", Type: stmt.Text}, {Name: "evidence", Type: stmt.Text}, {Name: "generation", Type: stmt.Integer}, {Name: "revision", Type: stmt.Integer}, {Name: "cursor", Type: stmt.Integer}, {Name: "old_charge", Type: stmt.Integer}}})
	for i := range defs {
		switch defs[i].ID {
		case evidenceSealReceiptInsert, evidenceSealBodyAttach:
			defs[i].CallSites = []string{"writer/enrichment_source_seal_receipts.go:writeEvidenceSealReceipt"}
		case sourceSealReceiptInsert, sourceSealBodyAttach:
			defs[i].CallSites = []string{"writer/enrichment_source_seal_receipts.go:writeSourceSealReceipt"}
		case sourceSealUpstreamPin:
			defs[i].CallSites = []string{"writer/enrichment_source_seal_receipts.go:writeSourceSealReceipt", "writer/enrichment_source_seal_allocate.go:createSourceStage"}
		case evidenceSealBuildFinalize:
			defs[i].CallSites = []string{"writer/enrichment_source_seal_receipts.go:accountEvidenceSealReference"}
		}
		// Closed missing-owner preparation cohort. Actual component effects have
		// separate fully populated native controls, never golden-plan authority.
		var args []any
		for _, p := range defs[i].Params {
			switch p.Type {
			case stmt.Text:
				args = append(args, "missing")
			case stmt.Integer:
				args = append(args, int64(1))
			case stmt.Blob:
				args = append(args, make([]byte, 32))
			}
		}
		defs[i].Cohorts = []stmt.Cohort{{Name: "missing_owner_shape", Bindings: args}}
	}
	return enrichmentStagePlanHints(defs)
}

// Same-turn OLD->NEW native CONTENT Q exactly once. The build charge is a
// committed-prefix witness, not another provider debit. No ACK/abort/status
// discount. Complete source/proof/control/index/WAL costs stay separately W-
// admitted; all errors require whole-command rollback, including body sealing.
func accountEvidenceSealReference(ctx context.Context, turn *commandTurn, scope sealOwnerScope, root evidenceSealRoot, facts sealMutationFacts, m normalizedSealManifest, oldCharge, providerCharge, providerOther int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	delta := 8 * int64(len(scope.ownerID)+16)
	if !scope.valid() || facts.revision != root.revision+1 || facts.next != root.rows || oldCharge < 512 || providerCharge < oldCharge || oldCharge > int64(evidenceStageMaximumCharge)-delta || providerCharge > 9223372036854775807-delta || providerOther < 1 || m.algorithm != normalizedEvidenceAlgorithm || m.rows != root.rows {
		return ErrInvalid
	}
	proof, err := readStageEvidenceProof(ctx, turn, root.evidenceID)
	if err != nil {
		return err
	}
	if proof.sealed != 1 || !proof.owner.Valid || proof.owner.String != scope.ownerID || !bytes.Equal(proof.inc, scope.identity.Incarnation[:]) || proof.sequence != scope.identity.Sequence {
		return ErrConflict
	}

	if err := execSealOne(ctx, turn, evidenceSealBuildFinalize, delta, hex.EncodeToString(m.commitment[:]), root.buildID, root.evidenceID, root.buildGeneration, facts.revision, facts.next, oldCharge); err != nil {
		return err
	}
	if err := execSealOne(ctx, turn, evidenceStageAccountingDelta, delta, root.evidenceID, oldCharge); err != nil {
		return err
	}
	return execSealOne(ctx, turn, evidenceStageTotalsDelta, delta, int64(0), scope.provider, providerCharge, providerOther)
}

func hexObservation(d [32]byte) string { return hex.EncodeToString(d[:]) }

func sealReceiptValues(scope sealOwnerScope, facts sealMutationFacts, c sealReceiptContent, result ResultDigest) []any {
	h, m := c.header, c.manifest
	return []any{scope.ownerID, scope.identity.Incarnation[:], scope.identity.Sequence, scope.identity.OperationID[:], scope.request[:], facts.observedCatalogRevision, facts.appliedMS, scope.entityID, facts.afterItem, facts.next, result[:],
		int64(1), h.ID, h.SubjectID, h.ParserVersion, h.Provider, h.SubjectKind, h.ProviderID, h.Language, h.Region, h.RequestShape, hexObservation(h.ObservationDigest), scope.generation, facts.revision, m.algorithm, m.commitment[:], m.rows, m.bytes, c.retainedBytes}
}
func validSealReceipt(scope sealOwnerScope, facts sealMutationFacts, c sealReceiptContent, algorithm string) bool {
	return scope.valid() && facts.revision > 0 && facts.next == c.manifest.rows && facts.afterItem == scope.afterItem && facts.observedCatalogRevision == scope.entityRevision && facts.appliedMS >= 0 && c.header.ID != "" && c.header.SubjectID > 0 && c.header.ParserVersion != "" && c.header.Provider == scope.provider && c.header.SubjectKind == "movie" &&
		c.manifest.version == 1 && c.manifest.algorithm == algorithm && c.manifest.rows >= 0 && c.manifest.bytes > 0 && uint64(c.manifest.bytes) <= maxCanonicalRequestBytes && c.retainedBytes > 0 && c.retainedBytes <= maxSealRetainedBytes
}

func writeEvidenceSealReceipt(ctx context.Context, turn *commandTurn, scope sealOwnerScope, root evidenceSealRoot, facts sealMutationFacts, c sealReceiptContent, result ResultDigest) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if !validSealReceipt(scope, facts, c, normalizedEvidenceAlgorithm) || facts.revision != root.revision+1 || facts.next != root.rows || root.rows < 1 || c.header.ID != root.evidenceID || c.header.SubjectID != root.subjectID || c.header.ParserVersion != root.parser {
		return ErrInvalid
	}
	build, err := readStageBuild(ctx, turn, root.buildID)
	if err != nil {
		return err
	}
	currentRoot := root
	currentRoot.revision = facts.revision
	if !build.matches(scope, currentRoot) || build.cursor != facts.next || build.rows != facts.next || build.declared != hex.EncodeToString(c.manifest.commitment[:]) {
		return ErrConflict
	}

	args := append(sealReceiptValues(scope, facts, c, result), root.buildID, root.buildGeneration)
	if _, _, err := observeSealOwner(ctx, turn, scope); err != nil {
		return err
	}
	args = append(args, "evidence_seal", int64(0), int64(1))
	if err := execSealOne(ctx, turn, evidenceSealReceiptInsert, args...); err != nil {
		return err
	}
	return execSealOne(ctx, turn, evidenceSealBodyAttach, scope.ownerID, scope.identity.Incarnation[:], scope.identity.Sequence, root.evidenceID, root.subjectID, root.parser)
}

func writeSourceSealReceipt(ctx context.Context, turn *commandTurn, scope sealOwnerScope, root sourceSealRoot, facts sealMutationFacts, c sealReceiptContent, result ResultDigest, upstreamProofRevision, upstreamPins int64) (err error) {
	defer func() { _, err = finishCommand(ctx, commandLifetime(turn), struct{}{}, err) }()
	if !validSealReceipt(scope, facts, c, normalizedSourceAlgorithm) || facts.revision != scope.sourceRevision+1 || facts.next != root.expected || c.header.ID != root.evidenceID || c.header.SubjectID != root.subjectID || c.header.ParserVersion != root.parser || upstreamProofRevision <= 0 || upstreamPins <= 0 {
		return ErrInvalid
	}
	// The advance already changed sourceR. Preserve every other scope witness.
	current := scope
	current.sourceRevision = facts.revision
	args := append(sealReceiptValues(scope, facts, c, result), root.publicationID, root.upstreamOwner, root.upstreamIncarnation[:], root.upstreamSequence)
	if _, _, err := observeSealOwner(ctx, turn, current); err != nil {
		return err
	}
	args = append(args, "credit_source_seal", int64(0), int64(1))
	if err := execSealOne(ctx, turn, sourceSealReceiptInsert, args...); err != nil {
		return err
	}
	if err := execSealOne(ctx, turn, sourceSealUpstreamPin, root.upstreamOwner, root.upstreamIncarnation[:], root.upstreamSequence, root.evidenceID, root.subjectID, root.parser, upstreamProofRevision, upstreamPins); err != nil {
		return err
	}
	return execSealOne(ctx, turn, sourceSealBodyAttach, scope.ownerID, scope.identity.Incarnation[:], scope.identity.Sequence, root.publicationID, scope.entityID, scope.provider, root.expected)
}
