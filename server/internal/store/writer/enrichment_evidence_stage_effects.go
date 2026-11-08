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

const evidenceStageHeader009 stmt.ID = "enrichment.evidence_stage.header_009"
const evidenceStagePrefixRead stmt.ID = "enrichment.evidence_stage.prefix"

func enrichmentEvidenceStageEffectDefinitions() []stmt.Definition {

	prefix := `id=? AND evidence_id=? AND generation=? AND content_revision=? AND cursor=? AND materialized_rows=cursor AND materialized_charge=? AND expected_rows=? AND owner_operation=? AND owner_incarnation=? AND owner_generation=? AND state='building' AND phase='children'`
	pp := []stmt.Param{{Name: "build", Type: stmt.Text}, {Name: "evidence", Type: stmt.Text}, {Name: "build_generation", Type: stmt.Integer}, {Name: "revision", Type: stmt.Integer}, {Name: "next", Type: stmt.Integer}, {Name: "charge", Type: stmt.Integer}, {Name: "expected", Type: stmt.Integer}, {Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "owner_generation", Type: stmt.Integer}}

	var header stmt.Definition
	for _, d := range enrichmentEvidenceDefinitions() {
		if d.ID == enrichmentEvidenceInsert {
			header = d
		}
	}
	header.ID = evidenceStageHeader009
	header.SQL = `INSERT INTO metadata_evidence(id,subject_id,parser_version,provider,subject_kind,provider_id,language,region,request_shape,digest,observed_at,source_url,etag,last_modified,title,original_title,overview,tagline,content_rating,studio,network,country,release_date,year,artist,length_millis,artist_type,begin_year,end_year,wikidata_id,biography,biography_source,biography_source_url,disambiguation,release_group_id,group_title,primary_type,barcode,sealed) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`
	header.Params = append(header.Params, stmt.Param{Name: "sealed", Type: stmt.Integer})
	for i := range header.Cohorts {
		header.Cohorts[i].Bindings = append(header.Cohorts[i].Bindings, int64(0))
	}
	defs := []stmt.Definition{header,
		{ID: evidenceStageBuild009, SQL: `INSERT INTO metadata_evidence_builds(id,evidence_id,generation,owner_operation,owner_incarnation,owner_generation,parent_build,state,phase,cursor,expected_rows,materialized_rows,expected_shape_digest,materialized_digest,materialized_charge,expires_at,content_revision) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{{Name: "build", Type: stmt.Text}, {Name: "evidence", Type: stmt.Text}, {Name: "build_generation", Type: stmt.Integer}, {Name: "owner", Type: stmt.Text}, {Name: "incarnation", Type: stmt.Blob}, {Name: "owner_generation", Type: stmt.Integer}, {Name: "parent", Type: stmt.Text, Nullable: true}, {Name: "state", Type: stmt.Text}, {Name: "phase", Type: stmt.Text}, {Name: "cursor", Type: stmt.Integer}, {Name: "expected", Type: stmt.Integer}, {Name: "materialized", Type: stmt.Integer}, {Name: "declared", Type: stmt.Text}, {Name: "prefix", Type: stmt.Text}, {Name: "charge", Type: stmt.Integer}, {Name: "expires", Type: stmt.Text}, {Name: "revision", Type: stmt.Integer}}},
		{ID: evidenceStageCredit, SQL: `INSERT INTO metadata_evidence_credits(provider,evidence_kind,credit_class,evidence_id,scope,ordinal,provider_person_id,name,role,department,credit_id,source_ordinal,billing) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, Params: []stmt.Param{{Name: "provider", Type: stmt.Text}, {Name: "kind", Type: stmt.Text}, {Name: "class", Type: stmt.Text}, {Name: "evidence", Type: stmt.Text}, {Name: "scope", Type: stmt.Text}, {Name: "ordinal", Type: stmt.Integer}, {Name: "person", Type: stmt.Text}, {Name: "name", Type: stmt.Text}, {Name: "role", Type: stmt.Text}, {Name: "department", Type: stmt.Text}, {Name: "credit", Type: stmt.Text}, {Name: "source_ordinal", Type: stmt.Integer}, {Name: "billing", Type: stmt.Integer, Nullable: true}}},
		{ID: evidenceStagePrefixRead, Kind: stmt.Read, SQL: `SELECT cursor FROM metadata_evidence_builds WHERE ` + prefix, Params: pp},
		{ID: evidenceStageAdvance, SQL: `UPDATE metadata_evidence_builds SET cursor=cursor+?,materialized_rows=materialized_rows+?,materialized_charge=materialized_charge+?,content_revision=content_revision+1 WHERE ` + prefix + ` RETURNING cursor,content_revision,materialized_charge`, Params: append([]stmt.Param{{Name: "count", Type: stmt.Integer}, {Name: "row_count", Type: stmt.Integer}, {Name: "delta", Type: stmt.Integer}}, pp...)},
		{ID: evidenceStageAccountingInsert, SQL: `INSERT INTO metadata_evidence_accounting(evidence_id,charge,observed_at,retention_refs,direct_refs,release_refs) VALUES(?,?,?,?,?,?)`, Params: []stmt.Param{{Name: "evidence", Type: stmt.Text}, {Name: "charge", Type: stmt.Integer}, {Name: "observed", Type: stmt.Text}, {Name: "retention", Type: stmt.Integer}, {Name: "direct", Type: stmt.Integer}, {Name: "release", Type: stmt.Integer}}},
		{ID: evidenceStageAccountingDelta, SQL: `UPDATE metadata_evidence_accounting SET charge=charge+? WHERE evidence_id=? AND charge=? AND retention_refs=0 AND direct_refs=0 AND release_refs=0`, Params: []stmt.Param{{Name: "delta", Type: stmt.Integer}, {Name: "evidence", Type: stmt.Text}, {Name: "old_charge", Type: stmt.Integer}}},
		{ID: evidenceStageTotalsDelta, SQL: `UPDATE metadata_evidence_totals SET charge=charge+?,other_evidence=other_evidence+? WHERE provider=? AND charge=? AND other_evidence=?`, Params: []stmt.Param{{Name: "delta", Type: stmt.Integer}, {Name: "new_other", Type: stmt.Integer}, {Name: "provider", Type: stmt.Text}, {Name: "old_charge", Type: stmt.Integer}, {Name: "old_other", Type: stmt.Integer}}},
	}
	for i := range defs {
		defs[i].Owner = "writer.enrichment"
		if defs[i].ID != evidenceStagePrefixRead {
			defs[i].Kind = stmt.Write
		}
		defs[i].CallSites = []string{"writer/enrichment_evidence_stage_effects.go:createEvidenceBuild", "writer/enrichment_evidence_stage_effects.go:stageEvidenceCredits", "writer/enrichment_source_seal_receipts.go:accountEvidenceSealReference"}
		if len(defs[i].Cohorts) == 0 {
			defs[i].Cohorts = []stmt.Cohort{{Name: "missing_native_keys", Bindings: missingSealBindings(defs[i].Params)}}
		}
	}
	return enrichmentStagePlanHints(defs)
}

// Explicit absent-key bind shape, not a successful/qualified producer cohort.
func missingSealBindings(params []stmt.Param) []any {
	args := make([]any, len(params))
	for i, p := range params {
		if p.Nullable {
			continue
		}
		switch p.Type {
		case stmt.Text:
			args[i] = "missing"
		case stmt.Integer:
			args[i] = int64(1)
		case stmt.Blob:
			args[i] = make([]byte, 32)
		case stmt.Real:
			args[i] = 1.0
		}
	}
	return args
}
func (p evidenceStagePrefix) values(scope sealOwnerScope) []any {
	return []any{p.root.buildID, p.root.evidenceID, p.root.buildGeneration, p.root.revision, p.next, p.charge, p.root.rows, scope.ownerID, scope.identity.Incarnation[:], scope.generation}
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
	if _, _, err := observeSealOwner(ctx, turn, scope); err != nil {
		return out, err
	}
	build, err := readStageBuild(ctx, turn, p.root.buildID)
	if err != nil {
		return out, err
	}
	if !build.matches(scope, p.root) || build.cursor != p.next || build.rows != p.next || build.charge != p.charge {
		return out, ErrConflict
	}

	for _, r := range rows {
		if err := execSealOne(ctx, turn, evidenceStageCredit, scope.provider, "movie", string(r.Class), p.root.evidenceID, "", r.Ordinal, r.ProviderPersonID, r.Name, r.Role, r.Department, r.CreditID, r.SourceOrdinal, evidenceIntegerValue(r.Billing)); err != nil {
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
	if _, _, err := observeSealOwner(ctx, turn, scope); err != nil {
		return out, err
	}
	if err := execSealOne(ctx, turn, evidenceStageHeader009, append(evidenceHeaderValues(payload.header.input), int64(0))...); err != nil {
		return out, err
	}
	if err := execSealOne(ctx, turn, evidenceStageBuild009, p.root.buildID, p.root.evidenceID, p.root.buildGeneration, scope.ownerID, scope.identity.Incarnation[:], scope.generation, nil, "building", "children", int64(1), p.root.rows, int64(1), hex.EncodeToString(p.manifest.commitment[:]), "", headerCharge, p.expires, int64(1)); err != nil {
		return out, err
	}
	if err := execSealOne(ctx, turn, evidenceStageAccountingInsert, p.root.evidenceID, headerCharge, payload.header.input.ObservedAt, int64(0), int64(0), int64(0)); err != nil {
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
