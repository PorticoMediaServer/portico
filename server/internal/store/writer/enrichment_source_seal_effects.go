package writer

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	enrichmentEvidenceSealAdvance stmt.ID = "enrichment.evidence_seal.advance"
	enrichmentSourceSealAdvance   stmt.ID = "enrichment.credit_source_seal.advance"
)

// These are private effect inputs, NOT verified plans. W's single entry must
// resolve the ORIGINAL receipt, validate current disclosure/full native intent,
// reserve the complete proof/debt forecast and verify the closed snapshot using
// its codec BEFORE invoking an effect. No boolean/digest here grants authority.
type evidenceSealRoot struct {
	buildID, evidenceID, parser                string
	buildGeneration, revision, subjectID, rows int64
}

type sourceSealRoot struct {
	publicationID, evidenceID, parser string
	subjectID, next, expected         int64
	upstreamOwner                     string
	upstreamIncarnation               [16]byte
	upstreamSequence                  int64
	manifest                          normalizedSealManifest
}

// Actual RETURNING facts precede W's closed result encoding. In particular,
// neither expectedR+1 nor the request/normalized commitment is a result digest.
type sealMutationFacts struct {
	revision, next, observedCatalogRevision, appliedMS int64
	afterItem                                          string
}

func enrichmentSealAdvanceDefinitions() []stmt.Definition {
	owner := `EXISTS(SELECT 1 FROM metadata_provider_operations o WHERE ` + sealOwnerPredicate + `
 AND NOT EXISTS(SELECT 1 FROM metadata_candidates c WHERE c.operation_id=o.id)`
	defs := []stmt.Definition{{
		ID: enrichmentEvidenceSealAdvance, Owner: "writer.enrichment", Kind: stmt.Write,
		CallSites: []string{"writer/enrichment_source_seal_effects.go:advanceEvidenceSeal"},
		SQL: `UPDATE metadata_evidence_builds AS b SET content_revision=content_revision+1
 WHERE b.id=? AND b.evidence_id=? AND b.generation=? AND b.content_revision=?
 AND b.cursor=? AND b.expected_rows=? AND b.materialized_rows=? AND b.state='building' AND b.phase='children'
 AND EXISTS(SELECT 1 FROM metadata_evidence e WHERE e.id=b.evidence_id AND e.subject_id=? AND e.parser_version=? AND e.sealed=0 AND e.seal_proof_owner IS NULL)
 AND ` + owner + ` AND o.evidence_build_id=b.id AND b.owner_operation=o.id AND b.owner_incarnation=o.command_incarnation AND b.owner_generation=o.generation)
 RETURNING content_revision,cursor,
 (SELECT e.revision FROM catalog_entities e JOIN metadata_provider_operations o ON o.entity_id=e.id WHERE o.id=metadata_evidence_builds.owner_operation),
 (SELECT after_item FROM metadata_provider_operations o WHERE o.id=metadata_evidence_builds.owner_operation)`,
		Params: append([]stmt.Param{{Name: "build", Type: stmt.Text}, {Name: "evidence", Type: stmt.Text}, {Name: "build_generation", Type: stmt.Integer}, {Name: "content_revision", Type: stmt.Integer},
			{Name: "cursor", Type: stmt.Integer}, {Name: "expected_rows", Type: stmt.Integer}, {Name: "materialized_rows", Type: stmt.Integer}, {Name: "subject", Type: stmt.Integer}, {Name: "parser", Type: stmt.Text}}, sealScopeParams()...),
	}, {
		ID: enrichmentSourceSealAdvance, Owner: "writer.enrichment", Kind: stmt.Write,
		CallSites: []string{"writer/enrichment_source_seal_effects.go:advanceSourceSeal"},
		SQL: `UPDATE metadata_provider_operations AS o SET source_content_revision=source_content_revision+1
 WHERE ` + sealOwnerPredicate + ` AND NOT EXISTS(SELECT 1 FROM metadata_candidates c WHERE c.operation_id=o.id)
 AND source_publication_id=? AND source_owner_generation=o.generation AND source_evidence_id=? AND source_subject_id=? AND source_parser_version=?
 AND source_next_position=? AND source_expected_members=? AND source_next_position=source_expected_members
 AND source_algorithm=? AND source_version=? AND source_expected_bytes=? AND source_expected_commitment=?
 AND source_evidence_owner=? AND source_evidence_incarnation=? AND source_evidence_sequence=?
 AND EXISTS(SELECT 1 FROM credit_source_publications p WHERE p.publication_id=o.source_publication_id AND p.entity_id=o.entity_id AND p.provider=o.provider
 AND p.member_count=o.source_expected_members AND p.sealed=0 AND p.seal_proof_owner IS NULL)
 RETURNING source_content_revision,source_next_position,
 (SELECT revision FROM catalog_entities WHERE id=metadata_provider_operations.entity_id),after_item`,
		Params: append(sealScopeParams(), []stmt.Param{{Name: "publication", Type: stmt.Text}, {Name: "evidence", Type: stmt.Text}, {Name: "subject", Type: stmt.Integer}, {Name: "parser", Type: stmt.Text},
			{Name: "next", Type: stmt.Integer}, {Name: "expected", Type: stmt.Integer}, {Name: "algorithm", Type: stmt.Text}, {Name: "version", Type: stmt.Integer}, {Name: "bytes", Type: stmt.Integer}, {Name: "commitment", Type: stmt.Blob},
			{Name: "upstream_owner", Type: stmt.Text}, {Name: "upstream_incarnation", Type: stmt.Blob}, {Name: "upstream_sequence", Type: stmt.Integer}}...),
	}}
	defs[0].Cohorts = []stmt.Cohort{{Name: "native_complete_evidence_prefix", Bindings: append([]any{"build-original:211", "evidence-original:197", int64(47), int64(1), int64(3), int64(3), int64(3), int64(193), "fixture-parser"}, sealScopeCohort()...)}}
	defs[1].Cohorts = []stmt.Cohort{{Name: "missing_source_custody", Bindings: append(sealScopeCohort(), "00112233445566778899aabbccddeeff", "evidence-original:197", int64(193), "fixture-parser", int64(0), int64(0), normalizedSourceAlgorithm, int64(1), int64(917), make([]byte, 32), "operation-one", sealScopeCohort()[1], int64(7))}}
	return defs

}

func advanceEvidenceSeal(ctx context.Context, turn *commandTurn, scope sealOwnerScope, root evidenceSealRoot) (out sealMutationFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	ms, err := scope.checkLease(turn)
	if err != nil {
		return out, err
	}
	if root.buildID == "" || root.evidenceID == "" || root.parser == "" || root.buildGeneration <= 0 || root.subjectID <= 0 || root.revision <= 0 || root.revision == math.MaxInt64 || root.rows < 1 || root.rows > 1+MaxCastCredits+MaxCrewCredits {
		return out, ErrInvalid
	}
	args := []any{root.buildID, root.evidenceID, root.buildGeneration, root.revision, root.rows, root.rows, root.rows, root.subjectID, root.parser}
	args = append(args, scope.values()...)
	out, err = readSealAdvance(ctx, turn, enrichmentEvidenceSealAdvance, args, root.revision, root.rows, scope, ms)
	return out, err
}

func advanceSourceSeal(ctx context.Context, turn *commandTurn, scope sealOwnerScope, root sourceSealRoot) (out sealMutationFacts, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	ms, err := scope.checkLease(turn)
	if err != nil {
		return out, err
	}
	if !lowerHex(root.publicationID, 32) || root.evidenceID == "" || root.parser == "" || root.subjectID <= 0 || root.next != root.expected || root.next < 0 || root.next > MaxCastCredits+MaxCrewCredits || root.upstreamOwner == "" || root.upstreamSequence <= 0 ||
		root.manifest.algorithm != normalizedSourceAlgorithm || root.manifest.version != 1 || root.manifest.rows != root.expected || root.manifest.bytes <= 0 || uint64(root.manifest.bytes) > maxCanonicalRequestBytes {
		return out, ErrInvalid
	}
	args := append(scope.values(), root.publicationID, root.evidenceID, root.subjectID, root.parser, root.next, root.expected, root.manifest.algorithm, root.manifest.version, root.manifest.bytes, root.manifest.commitment[:], root.upstreamOwner, root.upstreamIncarnation[:], root.upstreamSequence)
	out, err = readSealAdvance(ctx, turn, enrichmentSourceSealAdvance, args, scope.sourceRevision, root.next, scope, ms)
	return out, err
}

func readSealAdvance(ctx context.Context, turn *commandTurn, id stmt.ID, args []any, oldRevision, next int64, scope sealOwnerScope, ms int64) (sealMutationFacts, error) {
	step, err := sealStep(turn, id)
	if err != nil {
		return sealMutationFacts{}, err
	}
	row, err := turn.queryRow(ctx, step, args...)
	if err != nil {
		return sealMutationFacts{}, err
	}
	var out sealMutationFacts
	if err := row.Scan(&out.revision, &out.next, &out.observedCatalogRevision, &out.afterItem); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sealMutationFacts{}, ErrConflict
		}
		return sealMutationFacts{}, err
	}
	if out.revision != oldRevision+1 || out.next != next || out.observedCatalogRevision != scope.entityRevision || out.afterItem != scope.afterItem {
		return sealMutationFacts{}, ErrConflict
	}
	out.appliedMS = ms
	return out, nil
}
