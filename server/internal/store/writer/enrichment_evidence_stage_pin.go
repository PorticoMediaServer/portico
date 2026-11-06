package writer

import (
	"bytes"
	"context"
	"database/sql"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const enrichmentEvidencePinBuild stmt.ID = "enrichment.evidence_stage.pin_build"

// These are original native inputs, never receipt publication/image slots.
// The command constructor will freeze the complete input, resolve history and
// recheck current authority before this private effect runs in its SAME turn.
type evidenceBuildPinInput struct {
	operationID, buildID, evidenceID, provider, lease, leaseUntil string
	entityID, operationGeneration, buildGeneration                int64
	lastSequence, acknowledgedSequence                            int64
}

type evidenceBuildPinResult struct {
	buildID                          string
	buildGeneration, ownerGeneration int64
}

func enrichmentEvidencePinDefinitions() []stmt.Definition {
	incarnation := make([]byte, 16)
	for i := range incarnation {
		incarnation[i] = byte(16 + i)
	}
	defs := []stmt.Definition{{
		ID: enrichmentEvidencePinBuild, Owner: "writer.enrichment", Kind: stmt.Write,
		CallSites: []string{"writer/enrichment_evidence_stage_pin.go:pinEvidenceBuild"},
		SQL: `UPDATE metadata_provider_operations SET evidence_build_id=?
 WHERE id=? AND command_incarnation=? AND generation=? AND entity_id=? AND provider=?
 AND phase='enrichment-v1' AND status='staged' AND lease=? AND lease_until=?
 AND last_command_sequence=? AND acknowledged_command_sequence=? AND evidence_build_id IS NULL
 RETURNING evidence_build_id,generation`,
		Params: []stmt.Param{
			{Name: "build_id", Type: stmt.Text}, {Name: "operation_id", Type: stmt.Text},
			{Name: "incarnation", Type: stmt.Blob}, {Name: "operation_generation", Type: stmt.Integer},
			{Name: "entity_id", Type: stmt.Integer}, {Name: "provider", Type: stmt.Text},
			{Name: "lease", Type: stmt.Text}, {Name: "lease_until", Type: stmt.Text},
			{Name: "last_sequence", Type: stmt.Integer}, {Name: "ack_sequence", Type: stmt.Integer},
		},
		Cohorts: []stmt.Cohort{{Name: "exact_fresh_build", Bindings: []any{
			"build-original:211", "operation-one", incarnation, int64(31), int64(101), "fixture",
			"native-lease", "2100-11-05T12:00:00Z", int64(11), int64(11),
		}}},
	}}
	return enrichmentStagePlanHints(defs)
}

// pinEvidenceBuild is only the005 custody effect. Header/build construction,
// accounting, receipt/last, full current authority and complete resource
// admission belong to the enclosing command transaction. A failure must roll
// that whole transaction back. No result digest, Applied or source proof here.
func pinEvidenceBuild(ctx context.Context, turn *commandTurn, step statementStep, input evidenceBuildPinInput, identity CommandIdentity) (out evidenceBuildPinResult, err error) {
	if turn == nil || turn.transaction == nil || turn.transaction.descriptor.ID != EvidenceStage || step.id != enrichmentEvidencePinBuild {
		return out, ErrInvalid
	}
	defer func() { out, err = finishCommand(ctx, turn.transaction.ctx, out, err) }()
	if input.operationID == "" || input.buildID == "" || input.evidenceID == "" || input.provider == "" || input.entityID <= 0 ||
		input.operationGeneration <= 0 || input.buildGeneration <= 0 || input.lease == "" || input.leaseUntil == "" ||
		input.lastSequence < 0 || input.lastSequence != input.acknowledgedSequence || identity.Sequence <= input.lastSequence || identity.Sequence-input.lastSequence != 1 {
		return out, ErrConflict
	}
	build, err := readStageBuild(ctx, turn, input.buildID)
	if err != nil {
		return out, err
	}
	if build.evidence != input.evidenceID || build.generation != input.buildGeneration || build.owner != input.operationID || !bytes.Equal(build.inc, identity.Incarnation[:]) || build.ownerGeneration != input.operationGeneration || build.parent.Valid || build.state != "building" || build.phase != "children" || build.cursor != 1 || build.rows != 1 || build.provider != input.provider || build.sealed != 0 {
		return out, ErrConflict
	}
	row, err := turn.queryRow(ctx, step, input.buildID, input.operationID, identity.Incarnation[:], input.operationGeneration, input.entityID, input.provider, input.lease, input.leaseUntil, input.lastSequence, input.acknowledgedSequence)

	if err != nil {
		return out, err
	}
	if err := row.Scan(&out.buildID, &out.ownerGeneration); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return out, ErrConflict
		}
		return out, err
	}
	out.buildGeneration = build.generation
	if out.buildID != input.buildID || out.buildGeneration != input.buildGeneration || out.ownerGeneration != input.operationGeneration {
		return out, invalid("evidence_build_pin_returned_identity")
	}
	return out, nil
}
