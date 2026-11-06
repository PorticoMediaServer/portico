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

	defs := []stmt.Definition{{
		ID: enrichmentEvidenceSealAdvance, Owner: "writer.enrichment", Kind: stmt.Write,
		CallSites: []string{"writer/enrichment_source_seal_effects.go:advanceEvidenceSeal"},
		SQL: `UPDATE metadata_evidence_builds SET content_revision=content_revision+1
 WHERE metadata_evidence_builds.id=? AND metadata_evidence_builds.evidence_id=? AND metadata_evidence_builds.generation=? AND metadata_evidence_builds.content_revision=?
 AND metadata_evidence_builds.cursor=? AND metadata_evidence_builds.expected_rows=? AND metadata_evidence_builds.materialized_rows=? AND metadata_evidence_builds.state='building' AND metadata_evidence_builds.phase='children'
 AND owner_operation=? AND owner_incarnation=? AND owner_generation=?
 RETURNING content_revision,cursor`,
		Params: []stmt.Param{{Name: "build", Type: stmt.Text}, {Name: "evidence", Type: stmt.Text}, {Name: "build_generation", Type: stmt.Integer}, {Name: "revision", Type: stmt.Integer}, {Name: "cursor", Type: stmt.Integer}, {Name: "expected", Type: stmt.Integer}, {Name: "materialized", Type: stmt.Integer}, {Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "owner_generation", Type: stmt.Integer}},
	}, {
		ID: enrichmentSourceSealAdvance, Owner: "writer.enrichment", Kind: stmt.Write,
		CallSites: []string{"writer/enrichment_source_seal_effects.go:advanceSourceSeal"},
		SQL: `UPDATE metadata_provider_operations SET source_content_revision=source_content_revision+1
 WHERE ` + sealTargetOwnerPredicate + `
 AND source_publication_id=? AND source_owner_generation=metadata_provider_operations.generation AND source_evidence_id=? AND source_subject_id=? AND source_parser_version=?
 AND source_next_position=? AND source_expected_members=? AND source_next_position=source_expected_members
 AND source_algorithm=? AND source_version=? AND source_expected_bytes=? AND source_expected_commitment=?
 AND source_evidence_owner=? AND source_evidence_incarnation=? AND source_evidence_sequence=?
 RETURNING source_content_revision,source_next_position`,
		Params: append(sealScopeParams(), []stmt.Param{{Name: "publication", Type: stmt.Text}, {Name: "evidence", Type: stmt.Text}, {Name: "subject", Type: stmt.Integer}, {Name: "parser", Type: stmt.Text},
			{Name: "next", Type: stmt.Integer}, {Name: "expected", Type: stmt.Integer}, {Name: "algorithm", Type: stmt.Text}, {Name: "version", Type: stmt.Integer}, {Name: "bytes", Type: stmt.Integer}, {Name: "commitment", Type: stmt.Blob},
			{Name: "upstream_owner", Type: stmt.Text}, {Name: "upstream_incarnation", Type: stmt.Blob}, {Name: "upstream_sequence", Type: stmt.Integer}}...),
	}}
	for i := range defs {
		defs[i].Cohorts = []stmt.Cohort{{Name: "missing_native_keys", Bindings: missingSealBindings(defs[i].Params)}}
	}
	return enrichmentStagePlanHints(defs)

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
	if _, _, err := observeSealOwner(ctx, turn, scope); err != nil {
		return out, err
	}
	ready, err := sealCandidatesRetired(ctx, turn, scope)
	if err != nil {
		return out, err
	}
	if !ready {
		return out, ErrBusy
	}
	build, err := readStageBuild(ctx, turn, root.buildID)
	if err != nil {
		return out, err
	}
	if !build.matches(scope, root) || build.cursor != root.rows || build.rows != root.rows {
		return out, ErrConflict
	}
	args := []any{root.buildID, root.evidenceID, root.buildGeneration, root.revision, root.rows, root.rows, root.rows, scope.ownerID, scope.identity.Incarnation[:], scope.generation}

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
	if _, _, err := observeSealOwner(ctx, turn, scope); err != nil {
		return out, err
	}
	ready, err := sealCandidatesRetired(ctx, turn, scope)
	if err != nil {
		return out, err
	}
	if !ready {
		return out, ErrBusy
	}
	prefix := sourceStagePrefix{operationID: scope.ownerID, provider: scope.provider, publicationID: root.publicationID, phase: effectiveBeginPhase, status: "staged", lease: scope.lease, leaseUntil: scope.leaseUntil, incarnation: scope.identity.Incarnation, entityID: scope.entityID, generation: scope.generation, ownerGeneration: scope.generation, revision: scope.sourceRevision, next: root.next, expected: root.expected, lastSequence: scope.last, acknowledgedSequence: scope.ack}
	check, err := sealStep(turn, sourceStagePrefixRead)
	if err != nil {
		return out, err
	}
	r, err := turn.queryRow(ctx, check, sourcePrefixBindings(prefix, scope.identity)...)
	if err != nil {
		return out, err
	}
	var next int64
	if err := r.Scan(&next); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrConflict
		}
		return out, err
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
	if err := row.Scan(&out.revision, &out.next); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return sealMutationFacts{}, ErrConflict
		}
		return sealMutationFacts{}, err
	}
	current := scope
	if id == enrichmentSourceSealAdvance {
		current.sourceRevision = out.revision
	}
	out.observedCatalogRevision, out.afterItem, err = observeSealOwner(ctx, turn, current)
	if err != nil {
		return sealMutationFacts{}, err
	}

	if out.revision != oldRevision+1 || out.next != next || out.observedCatalogRevision != scope.entityRevision || out.afterItem != scope.afterItem {
		return sealMutationFacts{}, ErrConflict
	}
	out.appliedMS = ms
	return out, nil
}
