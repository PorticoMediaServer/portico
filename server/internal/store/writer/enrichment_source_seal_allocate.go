package writer

import (
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	sourceStagePublicationInsert stmt.ID = "enrichment.credit_stage.source_publication"
	sourceStageAllocate          stmt.ID = "enrichment.credit_stage.source_allocate"
)

func enrichmentSourceAllocationDefinitions() []stmt.Definition {
	defs := []stmt.Definition{{
		ID: sourceStagePublicationInsert, Owner: "writer.enrichment", Kind: stmt.Write,
		SQL: `INSERT INTO credit_source_publications(publication_id,entity_id,provider,accepted_record_reference,observed_at,member_count,sealed)
 SELECT ?,o.entity_id,o.provider,?,?,?,0 FROM metadata_provider_operations o WHERE ` + sealOwnerPredicate + ` AND o.source_publication_id IS NULL`,
		Params: append([]stmt.Param{{Name: "publication", Type: stmt.Text}, {Name: "record_reference", Type: stmt.Text}, {Name: "observed_at", Type: stmt.Text}, {Name: "complete_members", Type: stmt.Integer}}, sealScopeParams()...),
	}, {
		ID: sourceStageAllocate, Owner: "writer.enrichment", Kind: stmt.Write,
		SQL: `UPDATE metadata_provider_operations AS o SET source_content_revision=source_content_revision+1,source_publication_id=?,source_owner_generation=generation,
 source_evidence_id=?,source_subject_id=?,source_parser_version=?,source_next_position=0,source_expected_members=?,source_expected_bytes=?,source_algorithm=?,source_version=?,source_expected_commitment=?,
 source_evidence_owner=?,source_evidence_incarnation=?,source_evidence_sequence=?
 WHERE ` + sealOwnerPredicate + ` AND source_publication_id IS NULL
 AND EXISTS(SELECT 1 FROM credit_source_publications p WHERE p.publication_id=? AND p.entity_id=o.entity_id AND p.provider=o.provider AND p.member_count=? AND p.sealed=0 AND p.seal_proof_owner IS NULL)
 AND EXISTS(SELECT 1 FROM metadata_evidence e WHERE e.id=? AND e.subject_id=? AND e.parser_version=? AND e.provider=o.provider AND e.sealed=1 AND e.seal_proof_owner=? AND e.seal_proof_incarnation=? AND e.seal_proof_sequence=?)
 RETURNING source_next_position,source_content_revision`,
		Params: append(append([]stmt.Param{{Name: "publication", Type: stmt.Text}, {Name: "evidence", Type: stmt.Text}, {Name: "subject", Type: stmt.Integer}, {Name: "parser", Type: stmt.Text}, {Name: "complete_members", Type: stmt.Integer}, {Name: "manifest_bytes", Type: stmt.Integer}, {Name: "manifest_algorithm", Type: stmt.Text}, {Name: "manifest_version", Type: stmt.Integer}, {Name: "manifest_commitment", Type: stmt.Blob}, {Name: "upstream_owner", Type: stmt.Text}, {Name: "upstream_incarnation", Type: stmt.Blob}, {Name: "upstream_sequence", Type: stmt.Integer}}, sealScopeParams()...),
			[]stmt.Param{{Name: "expected_publication", Type: stmt.Text}, {Name: "expected_members", Type: stmt.Integer}, {Name: "expected_evidence", Type: stmt.Text}, {Name: "expected_subject", Type: stmt.Integer}, {Name: "expected_parser", Type: stmt.Text}, {Name: "expected_proof_owner", Type: stmt.Text}, {Name: "expected_proof_incarnation", Type: stmt.Blob}, {Name: "expected_proof_sequence", Type: stmt.Integer}}...),
	}}
	for i := range defs {
		defs[i].CallSites = []string{"writer/enrichment_source_seal_allocate.go:createSourceStage"}
		var args []any
		for _, p := range defs[i].Params {
			switch p.Type {
			case stmt.Text:
				args = append(args, "missing")
			case stmt.Blob:
				args = append(args, make([]byte, 32))
			case stmt.Integer:
				args = append(args, int64(1))
			}
		}
		defs[i].Cohorts = []stmt.Cohort{{Name: "missing_fresh_owner", Bindings: args}}
	}
	return defs
}

// First durable source header AND exclusive operation-owned custody AND its
// upstream pin are one transaction. The ID was part of the ORIGINAL immutable
// request before allocation; replays resolve that request before this effect.
// No generic owner, global-cache-head inference, opaque-reference parsing or
// proof minting. W admits the whole prospective closure, pins and reservation.
func createSourceStage(ctx context.Context, turn *commandTurn, scope sealOwnerScope, header normalizedSourceHeader, root sourceSealRoot, upstreamRevision, upstreamPins int64) (out sourceStageProgress, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if _, err := scope.checkLease(turn); err != nil {
		return out, err
	}
	if header.id != root.publicationID || !lowerHex(header.id, 32) || header.entityID != scope.entityID || header.provider != scope.provider || header.count != root.expected || root.next != 0 || root.expected < 0 || root.expected > MaxCastCredits+MaxCrewCredits ||
		header.record == "" || len(header.record) > 4096 || !sealNativeText(header.record) || header.observed == "" || !sealNativeText(header.observed) || root.evidenceID == "" || root.parser == "" || root.subjectID <= 0 || root.upstreamOwner == "" || root.upstreamSequence <= 0 || upstreamRevision <= 0 || upstreamPins <= 0 ||
		root.manifest.algorithm != normalizedSourceAlgorithm || root.manifest.version != 1 || root.manifest.rows != root.expected || root.manifest.bytes <= 0 || uint64(root.manifest.bytes) > maxCanonicalRequestBytes {
		return out, ErrInvalid
	}
	args := append([]any{header.id, header.record, header.observed, header.count}, scope.values()...)
	if err := execSealOne(ctx, turn, sourceStagePublicationInsert, args...); err != nil {
		return out, err
	}
	args = append([]any{header.id, root.evidenceID, root.subjectID, root.parser, root.expected, root.manifest.bytes, root.manifest.algorithm, root.manifest.version, root.manifest.commitment[:], root.upstreamOwner, root.upstreamIncarnation[:], root.upstreamSequence}, scope.values()...)
	args = append(args, header.id, root.expected, root.evidenceID, root.subjectID, root.parser, root.upstreamOwner, root.upstreamIncarnation[:], root.upstreamSequence)
	step, err := sealStep(turn, sourceStageAllocate)
	if err != nil {
		return out, err
	}
	row, err := turn.queryRow(ctx, step, args...)
	if err != nil {
		return out, err
	}
	if err := row.Scan(&out.next, &out.revision); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sourceStageProgress{}, ErrConflict
		}
		return sourceStageProgress{}, err
	}
	if out.next != 0 || out.revision != scope.sourceRevision+1 {
		return sourceStageProgress{}, ErrConflict
	}
	if err := execSealOne(ctx, turn, sourceSealUpstreamPin, root.upstreamOwner, root.upstreamIncarnation[:], root.upstreamSequence, root.evidenceID, root.subjectID, root.parser, upstreamRevision, upstreamPins); err != nil {
		return sourceStageProgress{}, err
	}
	return out, nil
}
