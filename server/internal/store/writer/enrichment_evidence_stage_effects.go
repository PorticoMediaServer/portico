package writer

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"math"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	evidenceStageBuild009         stmt.ID = "enrichment.evidence_stage.build_009"
	evidenceStageCredit           stmt.ID = "enrichment.evidence_stage.credit"
	evidenceStageAdvance          stmt.ID = "enrichment.evidence_stage.advance"
	evidenceStageAccountingInsert stmt.ID = "enrichment.evidence_stage.accounting_insert"
	evidenceStageAccountingDelta  stmt.ID = "enrichment.evidence_stage.accounting_delta"
	evidenceStageTotalsDelta      stmt.ID = "enrichment.evidence_stage.totals_delta"
	evidenceStageChunkCredits             = 16 // effect quantum; complete selected input remains <=150
	evidenceStageContentBytes     uint64  = 6 << 20
)

const evidenceStageMaximumCharge = 512*(1+MaxCastCredits+MaxCrewCredits) + 8*evidenceStageContentBytes

type evidenceStageForecast struct{ contentBytes, charge uint64 }

// Includes the future persisted owner TEXT and incarnation BLOB of the seal
// reference. Sequence/generated kind add no stored variable CONTENT bytes.
// This is a fixed adopted CONTENT forecast, not W/GV admission or a RAM cap.
func forecastEvidenceStage(p EvidenceStagePayload, proofOwner string) (evidenceStageForecast, error) {
	if !p.valid || proofOwner == "" || !sealNativeText(proofOwner) {
		return evidenceStageForecast{}, ErrInvalid
	}
	referenceBytes := uint64(len(proofOwner)) + 16
	if err := CheckEvidenceSealContentBytes(p.baseBytes, referenceBytes); err != nil {
		return evidenceStageForecast{}, err
	}
	bytes := p.baseBytes + referenceBytes
	return evidenceStageForecast{bytes, uint64(512*(1+len(p.credits))) + 8*bytes}, nil
}

// Native prefix witnesses, never caller-chosen limits or admission. The outer W
// entry reserves the WHOLE prospective stage and all control/source/proof costs
// before a first INSERT. Committed prefixes keep their charge through abort.
type evidenceStagePrefix struct {
	root                         evidenceSealRoot
	next, charge, providerCharge int64
	providerOther                int64
	manifest                     normalizedSealManifest
	expires                      string
}

func enrichmentEvidenceStageEffectDefinitions() []stmt.Definition {
	guard := `EXISTS(SELECT 1 FROM metadata_provider_operations o WHERE ` + sealOwnerPredicate + ` AND o.evidence_build_id IS NULL)`
	prefix := `b.id=? AND b.evidence_id=? AND b.generation=? AND b.content_revision=? AND b.cursor=? AND b.materialized_rows=b.cursor AND b.materialized_charge=?
 AND b.expected_rows=? AND b.state='building' AND b.phase='children'
 AND EXISTS(SELECT 1 FROM metadata_evidence e WHERE e.id=b.evidence_id AND e.sealed=0 AND e.seal_proof_owner IS NULL)
 AND EXISTS(SELECT 1 FROM metadata_provider_operations o WHERE ` + sealOwnerPredicate + ` AND o.evidence_build_id=b.id AND b.owner_operation=o.id AND b.owner_incarnation=o.command_incarnation AND b.owner_generation=o.generation)`
	prefixParams := []stmt.Param{{Name: "build", Type: stmt.Text}, {Name: "evidence", Type: stmt.Text}, {Name: "build_generation", Type: stmt.Integer}, {Name: "content_revision", Type: stmt.Integer}, {Name: "next", Type: stmt.Integer}, {Name: "charge", Type: stmt.Integer}, {Name: "expected_rows", Type: stmt.Integer}}
	defs := []stmt.Definition{{
		ID: evidenceStageBuild009, Owner: "writer.enrichment", Kind: stmt.Write,
		SQL: `INSERT INTO metadata_evidence_builds(id,evidence_id,generation,owner_operation,owner_incarnation,owner_generation,parent_build,state,phase,cursor,expected_rows,materialized_rows,expected_shape_digest,materialized_digest,materialized_charge,expires_at,content_revision)
 SELECT ?,?,?,o.id,o.command_incarnation,o.generation,NULL,'building','children',1,?,1,?,'',?,?,1 FROM metadata_provider_operations o
 WHERE ` + sealOwnerPredicate + ` AND o.evidence_build_id IS NULL`,
		Params: append([]stmt.Param{{Name: "build", Type: stmt.Text}, {Name: "evidence", Type: stmt.Text}, {Name: "build_generation", Type: stmt.Integer}, {Name: "expected_rows", Type: stmt.Integer}, {Name: "declared_manifest", Type: stmt.Text}, {Name: "header_charge", Type: stmt.Integer}, {Name: "expires", Type: stmt.Text}}, sealScopeParams()...),
	}, {
		ID: evidenceStageCredit, Owner: "writer.enrichment", Kind: stmt.Write,
		SQL: `INSERT INTO metadata_evidence_credits(provider,evidence_kind,credit_class,evidence_id,scope,ordinal,provider_person_id,name,role,department,credit_id,source_ordinal,billing)
 SELECT ?,'movie',?,?,'',?,?,?,?,?,?,?,? WHERE EXISTS(SELECT 1 FROM metadata_evidence_builds b WHERE ` + prefix + `)`,
		Params: append(append([]stmt.Param{{Name: "row_provider", Type: stmt.Text}, {Name: "class", Type: stmt.Text}, {Name: "evidence_id", Type: stmt.Text}, {Name: "ordinal", Type: stmt.Integer}, {Name: "provider_person", Type: stmt.Text}, {Name: "name", Type: stmt.Text}, {Name: "role", Type: stmt.Text}, {Name: "department", Type: stmt.Text}, {Name: "credit_id", Type: stmt.Text}, {Name: "source_ordinal", Type: stmt.Integer}, {Name: "billing", Type: stmt.Integer, Nullable: true}}, prefixParams...), sealScopeParams()...),
	}, {
		ID: evidenceStageAdvance, Owner: "writer.enrichment", Kind: stmt.Write,
		SQL:    `UPDATE metadata_evidence_builds AS b SET cursor=cursor+?,materialized_rows=materialized_rows+?,materialized_charge=materialized_charge+?,content_revision=content_revision+1 WHERE ` + prefix + ` RETURNING cursor,content_revision,materialized_charge`,
		Params: append(append([]stmt.Param{{Name: "count", Type: stmt.Integer}, {Name: "row_count", Type: stmt.Integer}, {Name: "charge_delta", Type: stmt.Integer}}, prefixParams...), sealScopeParams()...),
	}, {
		ID: evidenceStageAccountingInsert, Owner: "writer.enrichment", Kind: stmt.Write,
		SQL:    `INSERT INTO metadata_evidence_accounting(evidence_id,charge,observed_at,retention_refs,direct_refs,release_refs) SELECT ?,?,?,0,0,0 WHERE ` + guard,
		Params: append([]stmt.Param{{Name: "evidence", Type: stmt.Text}, {Name: "charge", Type: stmt.Integer}, {Name: "observed_at", Type: stmt.Text}}, sealScopeParams()...),
	}, {
		ID: evidenceStageAccountingDelta, Owner: "writer.enrichment", Kind: stmt.Write,
		SQL:    `UPDATE metadata_evidence_accounting SET charge=charge+? WHERE evidence_id=? AND charge=? AND retention_refs=0 AND direct_refs=0 AND release_refs=0`,
		Params: []stmt.Param{{Name: "delta", Type: stmt.Integer}, {Name: "evidence", Type: stmt.Text}, {Name: "old_charge", Type: stmt.Integer}},
	}, {
		ID: evidenceStageTotalsDelta, Owner: "writer.enrichment", Kind: stmt.Write,
		SQL:    `UPDATE metadata_evidence_totals SET charge=charge+?,other_evidence=other_evidence+? WHERE provider=? AND charge=? AND other_evidence=?`,
		Params: []stmt.Param{{Name: "delta", Type: stmt.Integer}, {Name: "new_other", Type: stmt.Integer}, {Name: "provider", Type: stmt.Text}, {Name: "old_charge", Type: stmt.Integer}, {Name: "old_other", Type: stmt.Integer}},
	}}
	// Named deterministic bind shapes are not plan/resource qualification.
	for i := range defs {
		switch defs[i].ID {
		case evidenceStageBuild009, evidenceStageAccountingInsert:
			defs[i].CallSites = []string{"writer/enrichment_evidence_stage_effects.go:createEvidenceBuild"}
		case evidenceStageCredit, evidenceStageAdvance:
			defs[i].CallSites = []string{"writer/enrichment_evidence_stage_effects.go:stageEvidenceCredits"}
		case evidenceStageAccountingDelta:
			defs[i].CallSites = []string{"writer/enrichment_evidence_stage_effects.go:stageEvidenceCredits", "writer/enrichment_source_seal_receipts.go:accountEvidenceSealReference"}
		case evidenceStageTotalsDelta:
			defs[i].CallSites = []string{"writer/enrichment_evidence_stage_effects.go:createEvidenceBuild", "writer/enrichment_evidence_stage_effects.go:stageEvidenceCredits", "writer/enrichment_source_seal_receipts.go:accountEvidenceSealReference"}
		}
		var args []any
		switch defs[i].ID {
		case evidenceStageBuild009:
			args = append([]any{"build-new", "evidence-native:149", int64(47), int64(4), makeHexZero(), int64(4096), "2100-11-05T13:00:00Z"}, sealScopeCohort()...)
		case evidenceStageCredit:
			args = append([]any{"fixture", "cast", "evidence-original:197", int64(0), "173", "Name", "Pilot", "Acting", "credit:193", int64(7), int64(0)}, evidencePrefixCohort()...)
		case evidenceStageAdvance:
			args = append([]any{int64(1), int64(1), int64(512)}, evidencePrefixCohort()...)
		case evidenceStageAccountingInsert:
			args = append([]any{"evidence-original:197", int64(1024), "2026-11-06T12:00:00Z"}, sealScopeCohort()...)
		case evidenceStageAccountingDelta:
			args = []any{int64(512), "evidence-original:197", int64(1024)}
		case evidenceStageTotalsDelta:
			args = []any{int64(512), int64(0), "fixture", int64(1024), int64(1)}
		}
		defs[i].Cohorts = []stmt.Cohort{{Name: "native_prefix_shape", Bindings: args}}
	}
	return defs
}

func makeHexZero() string { return "0000000000000000000000000000000000000000000000000000000000000000" }
func evidencePrefixCohort() []any {
	return append([]any{"build-original:211", "evidence-original:197", int64(47), int64(1), int64(1), int64(1024), int64(3)}, sealScopeCohort()...)
}
func (p evidenceStagePrefix) values(scope sealOwnerScope) []any {
	return append([]any{p.root.buildID, p.root.evidenceID, p.root.buildGeneration, p.root.revision, p.next, p.charge, p.root.rows}, scope.values()...)
}

func stageEvidenceCredits(ctx context.Context, turn *commandTurn, scope sealOwnerScope, p evidenceStagePrefix, rows []EvidenceCreditInput) (out evidenceStagePrefix, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if _, err := scope.checkLease(turn); err != nil {
		return out, err
	}
	if len(rows) == 0 || len(rows) > evidenceStageChunkCredits || p.root.revision <= 0 || p.root.revision == math.MaxInt64 || p.next < 1 || p.next+int64(len(rows)) > p.root.rows || p.charge < 512 || p.providerCharge < p.charge {
		return out, ErrInvalid
	}
	var delta int64
	for i, r := range rows {
		if r.Ordinal != p.next-1+int64(i) || r.SourceOrdinal < 0 || (r.Class != Cast && r.Class != Crew) || (r.Class == Cast && (!r.Billing.Present || r.Billing.Value < 0)) || (r.Class == Crew && (r.Billing.Present || r.Billing.Value != 0)) {
			return out, ErrInvalid
		}
		bytes := len(scope.provider) + len("movie") + len(r.Class) + len(p.root.evidenceID)
		for _, v := range []string{r.ProviderPersonID, r.Name, r.Role, r.Department, r.CreditID} {
			if !sealNativeText(v) {
				return out, ErrInvalid
			}
			bytes += len(v)
		}
		if uint64(bytes) > evidenceStageContentBytes || delta > math.MaxInt64-512-8*int64(bytes) {
			return out, ErrCap
		}
		delta += 512 + 8*int64(bytes)
	}
	if p.charge > int64(evidenceStageMaximumCharge)-delta || p.providerCharge > math.MaxInt64-delta {
		return out, ErrCap
	}
	for _, r := range rows {
		args := append([]any{scope.provider, string(r.Class), p.root.evidenceID, r.Ordinal, r.ProviderPersonID, r.Name, r.Role, r.Department, r.CreditID, r.SourceOrdinal, evidenceIntegerValue(r.Billing)}, p.values(scope)...)
		if err := execSealOne(ctx, turn, evidenceStageCredit, args...); err != nil {
			return out, err
		}
	}
	step, err := sealStep(turn, evidenceStageAdvance)
	if err != nil {
		return out, err
	}
	args := append([]any{int64(len(rows)), int64(len(rows)), delta}, p.values(scope)...)
	row, err := turn.queryRow(ctx, step, args...)
	if err != nil {
		return out, err
	}
	out = p
	if err := row.Scan(&out.next, &out.root.revision, &out.charge); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return evidenceStagePrefix{}, ErrConflict
		}
		return evidenceStagePrefix{}, err
	}
	if out.next != p.next+int64(len(rows)) || out.root.revision != p.root.revision+1 || out.charge != p.charge+delta {
		return evidenceStagePrefix{}, ErrConflict
	}
	if err := execSealOne(ctx, turn, evidenceStageAccountingDelta, delta, p.root.evidenceID, p.charge); err != nil {
		return evidenceStagePrefix{}, err
	}
	if err := execSealOne(ctx, turn, evidenceStageTotalsDelta, delta, int64(0), scope.provider, p.providerCharge, p.providerOther); err != nil {
		return evidenceStagePrefix{}, err
	}
	out.providerCharge += delta
	return out, nil
}

func execSealOne(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) error {
	step, err := sealStep(turn, id)
	if err != nil {
		return err
	}
	result, err := turn.exec(ctx, step, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrConflict
	}
	return nil
}

func createEvidenceBuild(ctx context.Context, turn *commandTurn, scope sealOwnerScope, p evidenceStagePrefix, payload EvidenceStagePayload) (out evidenceStagePrefix, err error) {
	defer func() { out, err = finishCommand(ctx, commandLifetime(turn), out, err) }()
	if _, err := scope.checkLease(turn); err != nil {
		return out, err
	}
	if _, err := forecastEvidenceStage(payload, scope.ownerID); err != nil {
		return out, err
	}
	if p.root.buildID == "" || p.root.buildGeneration <= 0 || p.root.evidenceID != payload.header.ID() || p.root.subjectID != payload.header.SubjectID() || p.root.parser != payload.header.input.ParserVersion || p.root.rows != int64(1+len(payload.credits)) || scope.provider != payload.header.input.Provider || scope.selectedID != payload.header.input.ProviderID ||
		p.manifest.algorithm != normalizedEvidenceAlgorithm || p.manifest.version != 1 || p.manifest.rows != p.root.rows || p.manifest.bytes <= 0 || uint64(p.manifest.bytes) > maxCanonicalRequestBytes || p.expires == "" || p.providerCharge < 0 || p.providerOther < 0 || p.providerOther == math.MaxInt64 {
		return out, ErrInvalid
	}
	headerCharge := int64(512 + 8*(payload.header.TextBytes()+64))
	if p.providerCharge > math.MaxInt64-headerCharge {
		return out, ErrCap
	}
	if err := execSealOne(ctx, turn, enrichmentEvidenceInsert, evidenceHeaderValues(payload.header.input)...); err != nil {
		return out, err
	}
	args := append([]any{p.root.buildID, p.root.evidenceID, p.root.buildGeneration, p.root.rows, hex.EncodeToString(p.manifest.commitment[:]), headerCharge, p.expires}, scope.values()...)
	if err := execSealOne(ctx, turn, evidenceStageBuild009, args...); err != nil {
		return out, err
	}
	args = append([]any{p.root.evidenceID, headerCharge, payload.header.input.ObservedAt}, scope.values()...)
	if err := execSealOne(ctx, turn, evidenceStageAccountingInsert, args...); err != nil {
		return out, err
	}
	if err := execSealOne(ctx, turn, evidenceStageTotalsDelta, headerCharge, int64(1), scope.provider, p.providerCharge, p.providerOther); err != nil {
		return out, err
	}
	step, err := sealStep(turn, enrichmentEvidencePinBuild)
	if err != nil {
		return out, err
	}
	input := evidenceBuildPinInput{scope.ownerID, p.root.buildID, p.root.evidenceID, scope.provider, scope.lease, scope.leaseUntil, scope.entityID, scope.generation, p.root.buildGeneration, scope.last, scope.ack}
	if _, err := pinEvidenceBuild(ctx, turn, step, input, scope.identity); err != nil {
		return out, err
	}
	out = p
	out.root.revision = 1
	out.next = 1
	out.charge = headerCharge
	out.providerCharge += headerCharge
	out.providerOther++
	return out, nil
}
