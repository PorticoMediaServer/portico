package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const stageBuildFacts stmt.ID = "enrichment.evidence_stage.build_facts"
const stageEvidenceProofFacts stmt.ID = "enrichment.stage.evidence_proof_facts"

type stageBuildNative struct {
	declared                                                                               string
	id, evidence, owner, phase, state, parser, provider                                    string
	generation, ownerGeneration, cursor, expected, rows, charge, revision, subject, sealed int64
	inc                                                                                    []byte
	parent, proofOwner                                                                     sql.NullString
}
type stageEvidenceProof struct {
	subject, sequence, sealed int64
	parser, provider          string
	owner                     sql.NullString
	inc                       []byte
}

func enrichmentStageFactDefinitions() []stmt.Definition {
	defs := []stmt.Definition{
		{ID: stageBuildFacts, Owner: "writer.enrichment", Kind: stmt.Read, CallSites: []string{"writer/enrichment_stage_facts.go:readStageBuild"}, SQL: `SELECT b.id,b.evidence_id,b.generation,b.owner_operation,b.owner_incarnation,b.owner_generation,b.parent_build,b.state,b.phase,b.cursor,b.expected_rows,b.materialized_rows,b.materialized_charge,b.content_revision,b.expected_shape_digest,e.subject_id,e.parser_version,e.provider,e.sealed,e.seal_proof_owner FROM metadata_evidence_builds AS b INNER JOIN metadata_evidence AS e ON e.id=b.evidence_id WHERE b.id=?`, Params: []stmt.Param{{Name: "build", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "native_build", Bindings: []any{"build-original:211"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_evidence_builds_1"}}},
		{ID: stageEvidenceProofFacts, Owner: "writer.enrichment", Kind: stmt.Read, CallSites: []string{"writer/enrichment_stage_facts.go:readStageEvidenceProof"}, SQL: `SELECT subject_id,parser_version,provider,sealed,seal_proof_owner,seal_proof_incarnation,seal_proof_sequence FROM metadata_evidence WHERE id=?`, Params: []stmt.Param{{Name: "evidence", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "native_evidence", Bindings: []any{"evidence-original:197"}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_metadata_evidence_1"}}}}
	return enrichmentStagePlanHints(defs)
}
func readStageBuild(ctx context.Context, turn *commandTurn, id string) (out stageBuildNative, err error) {
	step, err := sealStep(turn, stageBuildFacts)
	if err != nil {
		return out, err
	}
	row, err := turn.queryRow(ctx, step, id)
	if err != nil {
		return out, err
	}
	err = row.Scan(&out.id, &out.evidence, &out.generation, &out.owner, &out.inc, &out.ownerGeneration, &out.parent, &out.state, &out.phase, &out.cursor, &out.expected, &out.rows, &out.charge, &out.revision, &out.declared, &out.subject, &out.parser, &out.provider, &out.sealed, &out.proofOwner)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrConflict
	}
	return out, err
}
func (b stageBuildNative) matches(scope sealOwnerScope, root evidenceSealRoot) bool {
	return b.id == root.buildID && b.evidence == root.evidenceID && b.generation == root.buildGeneration && b.revision == root.revision && b.owner == scope.ownerID && bytes.Equal(b.inc, scope.identity.Incarnation[:]) && b.ownerGeneration == scope.generation && b.subject == root.subjectID && b.parser == root.parser && b.provider == scope.provider && b.expected == root.rows && !b.parent.Valid && b.state == "building" && b.phase == "children" && b.sealed == 0 && !b.proofOwner.Valid
}
func readStageEvidenceProof(ctx context.Context, turn *commandTurn, id string) (out stageEvidenceProof, err error) {
	step, err := sealStep(turn, stageEvidenceProofFacts)
	if err != nil {
		return out, err
	}
	row, err := turn.queryRow(ctx, step, id)
	if err != nil {
		return out, err
	}
	var sequence sql.NullInt64
	err = row.Scan(&out.subject, &out.parser, &out.provider, &out.sealed, &out.owner, &out.inc, &sequence)
	out.sequence = sequence.Int64
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrConflict
	}
	return out, err
}
