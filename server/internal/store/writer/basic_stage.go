package writer

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// These are private, prepared effect values for family1, not a command,
// principal, request digest or accepted input. The future Basic dispatcher
// resolves original history and proves current source/run/lease/scope, complete
// raw caps and its OWN funded preparation before calling these bodies. No
// Inventory time/memory wrapper or admission profile is borrowed here.
type basicStreamFactStage struct {
	changeID, inputRevision, libraryID, assetID, expectedEpoch         int64
	assetToken, runID, sourceID, incarnation, objectID, objectRevision string
	sourceGeneration, policyRevision                                   int64
	witnessFingerprint                                                 [32]byte
	factRevision, size, modifiedNS, detailVersion                      int64
	fingerprint                                                        string
}

type basicFamilyProgress struct {
	changeID, inputRevision, rowsStaged, sealed int64
}

const (
	basicStageHeader       stmt.ID = "scan.basic_stage.header"
	basicStageAsset        stmt.ID = "scan.basic_stage.asset"
	basicStageWitness      stmt.ID = "scan.basic_stage.witness"
	basicStageFamily       stmt.ID = "scan.basic_stage.family"
	basicStageBudget       stmt.ID = "scan.basic_stage.budget"
	basicStageBudgetCAS    stmt.ID = "scan.basic_stage.budget_cas"
	basicStageStreamInsert stmt.ID = "scan.basic_stage.stream_fact_insert"
	basicStageFamilyCAS    stmt.ID = "scan.basic_stage.family_cas"
	basicSealStreamFact    stmt.ID = "scan.basic_seal.stream_fact"
	basicSealFamilyCAS     stmt.ID = "scan.basic_seal.family_cas"
)

// No registry constructor, generic family selector or public SQL surface.
// W composes these literal definitions into the complete closed purposes.
func basicStreamStageDefinitions() []stmt.Definition {
	i := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	s := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicStageHeader, Kind: stmt.Read, SQL: `SELECT asset_id,asset_token,slot,phase,expected_epoch,input_revision,family_mask,fingerprint,copy_aux,fault
FROM catalog_info_changes WHERE id=?`, Params: []stmt.Param{i("change_id")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "staging_header", Bindings: []any{int64(51)}}}},
		{ID: basicStageAsset, Kind: stmt.Read, SQL: `SELECT library_id,token,size,modified_ns,info_epoch FROM catalog_assets WHERE id=?`,
			Params: []stmt.Param{i("asset_id")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "same_library_asset", Bindings: []any{int64(41)}}}},
		{ID: basicStageWitness, Kind: stmt.Read, SQL: `SELECT ordinal,kind,job_id,source_id,source_generation,root_incarnation,object_id,object_revision,policy_revision,expected_choice_revision,fingerprint
FROM catalog_info_input_witnesses WHERE change_id=? ORDER BY ordinal LIMIT ?`, Params: []stmt.Param{i("change_id"), i("row_limit")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_input_witnesses"}, Seeks: []string{"change_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "inventory_witness", Bindings: []any{int64(51), int64(2)}}}},
		{ID: basicStageFamily, Kind: stmt.Read, SQL: `SELECT witness_ordinal,rows_expected,rows_staged,sealed FROM catalog_info_input_families WHERE change_id=? AND family=?`,
			Params: []stmt.Param{i("change_id"), i("family")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_input_families"}, Seeks: []string{"change_id=? AND family=?"}},
			Cohorts: []stmt.Cohort{{Name: "unsealed_stream_fact", Bindings: []any{int64(51), int64(1)}}}},
		{ID: basicStageBudget, Kind: stmt.Read, SQL: `SELECT used_bytes,cap_bytes FROM catalog_info_input_budget WHERE singleton=?`,
			Params: []stmt.Param{i("singleton")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_input_budget"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "staging_debt", Bindings: []any{int64(1)}}}},
		{ID: basicStageBudgetCAS, Kind: stmt.Write, SQL: `UPDATE catalog_info_input_budget SET used_bytes=? WHERE singleton=? AND used_bytes=? AND cap_bytes=? RETURNING used_bytes`,
			Params:  []stmt.Param{i("next_bytes"), i("singleton"), i("expected_bytes"), i("cap_bytes")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_input_budget"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "charge_stream_fact", Bindings: []any{int64(1267), int64(1), int64(1000), int64(33554432)}}}},
		{ID: basicStageStreamInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_info_new_stream_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,fingerprint,detail_version)
VALUES(?,?,?,?,?,?,?,?)`, Params: []stmt.Param{i("change_id"), i("input_ordinal"), s("asset_token"), i("fact_revision"), i("size"), i("modified_ns"), s("fingerprint"), i("detail_version")},
			Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "one_stream_fact", Bindings: []any{int64(51), int64(0), "basic-asset", int64(29), int64(42), int64(-11), "probe-fingerprint", int64(1)}}}},
		{ID: basicStageFamilyCAS, Kind: stmt.Write, SQL: `UPDATE catalog_info_input_families SET rows_staged=?
WHERE change_id=? AND family=? AND witness_ordinal=? AND rows_expected=? AND rows_staged=? AND sealed=? RETURNING rows_staged,sealed`,
			Params:  []stmt.Param{i("next_rows"), i("change_id"), i("family"), i("witness_ordinal"), i("rows_expected"), i("expected_rows"), i("sealed")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_input_families"}, Seeks: []string{"change_id=? AND family=?"}},
			Cohorts: []stmt.Cohort{{Name: "advance_stream_fact_count", Bindings: []any{int64(1), int64(51), int64(1), int64(0), int64(1), int64(0), int64(0)}}}},
		{ID: basicSealStreamFact, Kind: stmt.Read, SQL: `SELECT input_ordinal,asset_id,revision,size,modified_ns,fingerprint,detail_version
FROM catalog_info_new_stream_facts WHERE change_id=? ORDER BY asset_id LIMIT ?`, Params: []stmt.Param{i("change_id"), i("row_limit")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_new_stream_facts"}, Seeks: []string{"change_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "complete_stream_fact", Bindings: []any{int64(51), int64(2)}}}},
		{ID: basicSealFamilyCAS, Kind: stmt.Write, SQL: `UPDATE catalog_info_input_families SET sealed=?
WHERE change_id=? AND family=? AND witness_ordinal=? AND rows_expected=? AND rows_staged=? AND sealed=? RETURNING rows_staged,sealed`,
			Params:  []stmt.Param{i("next_sealed"), i("change_id"), i("family"), i("witness_ordinal"), i("rows_expected"), i("rows_staged"), i("expected_sealed")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_input_families"}, Seeks: []string{"change_id=? AND family=?"}},
			Cohorts: []stmt.Cohort{{Name: "seal_stream_fact", Bindings: []any{int64(1), int64(51), int64(1), int64(0), int64(1), int64(1), int64(0)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_stage.go"}
	}
	return defs
}

func basicStageRead(ctx context.Context, turn *commandTurn, id stmt.ID, args ...any) (commandRow, error) {
	step, ok := turn.steps[id]
	if !ok {
		return commandRow{}, invalid("unregistered_basic_step")
	}
	return turn.queryRow(ctx, step.statementStep, args...)
}

func basicStageError(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrConflict
	}
	return err
}

// Only this closed INSERT's two native key constraints denote a stale or
// duplicate fact. CHECK/FK failures and operational errors retain their type.
func basicStreamFactInsertError(err error) error {
	var coded interface{ Code() int }
	if errors.As(err, &coded) {
		switch coded.Code() {
		case 1555, 2067: // SQLITE_CONSTRAINT_PRIMARYKEY, SQLITE_CONSTRAINT_UNIQUE
			return errors.Join(ErrConflict, err)
		}
	}
	return err
}

func checkBasicStreamInput(ctx context.Context, turn *commandTurn, in basicStreamFactStage) error {
	return checkBasicInputAtPhase(ctx, turn, in, 0)
}

func checkBasicInputAtPhase(ctx context.Context, turn *commandTurn, in basicStreamFactStage, expectedPhase int64) (err error) {
	if expectedPhase != 0 && expectedPhase != 1 && expectedPhase != 2 && expectedPhase != 3 {
		return ErrInvalid
	}

	if in.changeID <= 0 || in.inputRevision <= 0 || in.libraryID <= 0 || in.assetID <= 0 || in.expectedEpoch <= 0 || in.sourceGeneration <= 0 ||
		in.policyRevision <= 0 || in.factRevision <= 0 || in.size < 0 || in.detailVersion < 0 || in.witnessFingerprint == ([32]byte{}) {
		return ErrInvalid
	}
	texts := [...]string{in.assetToken, in.runID, in.sourceID, in.incarnation, in.objectID, in.objectRevision, in.fingerprint}
	remaining := 33554432
	for _, text := range texts {
		if len(text) > remaining {
			return ErrCap
		}
		remaining -= len(text)
	}
	for _, text := range texts {
		if text == "" || !utf8.ValidString(text) {
			return ErrInvalid
		}
	}
	row, err := basicStageRead(ctx, turn, basicStageHeader, in.changeID)
	if err != nil {
		return err
	}
	var asset, slot, phase, epoch, revision, mask, aux int64
	var token, fault string
	var fingerprint []byte
	if err = row.Scan(&asset, &token, &slot, &phase, &epoch, &revision, &mask, &fingerprint, &aux, &fault); err != nil {
		return basicStageError(err)
	}
	if asset != in.assetID || token != in.assetToken || slot != 0 || phase != expectedPhase || epoch != in.expectedEpoch || revision != in.inputRevision ||
		mask < 1 || mask > 1<<18-1 || mask&1 == 0 || aux < 0 || fault != "" || len(fingerprint) != 32 || [32]byte(fingerprint) != in.witnessFingerprint {
		return ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicStageAsset, in.assetID)
	if err != nil {
		return err
	}
	var library, size, modified int64
	if err = row.Scan(&library, &token, &size, &modified, &epoch); err != nil {
		return basicStageError(err)
	}
	if library != in.libraryID || token != in.assetToken || size != in.size || modified != in.modifiedNS || epoch != in.expectedEpoch {
		return ErrConflict
	}
	step, ok := turn.steps[basicStageWitness]
	if !ok {
		return invalid("unregistered_basic_step")
	}
	rows, err := turn.query(ctx, step.statementStep, in.changeID, int64(2))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return err
		}
		return ErrConflict
	}
	var ordinal, kind, generation, policy, choice int64
	var run, source, incarnation, object, objectRevision string
	if err = rows.Scan(&ordinal, &kind, &run, &source, &generation, &incarnation, &object, &objectRevision, &policy, &choice, &fingerprint); err != nil {
		return basicStageError(err)
	}
	if rows.Next() {
		return ErrConflict
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if ordinal != 0 || kind != 0 || run != in.runID || source != in.sourceID || generation != in.sourceGeneration || incarnation != in.incarnation || object != in.objectID ||
		objectRevision != in.objectRevision || policy != in.policyRevision || choice != 0 || len(fingerprint) != 32 || [32]byte(fingerprint) != in.witnessFingerprint {
		return ErrConflict
	}
	return nil
}

func checkBasicStreamStage(ctx context.Context, turn *commandTurn, in basicStreamFactStage, wantRows int64) error {
	if err := checkBasicStreamInput(ctx, turn, in); err != nil {
		return err
	}
	row, err := basicStageRead(ctx, turn, basicStageFamily, in.changeID, int64(1))
	if err != nil {
		return err
	}
	var ordinal, expected, staged, sealed int64
	if err = row.Scan(&ordinal, &expected, &staged, &sealed); err != nil {
		return basicStageError(err)
	}
	if ordinal != 0 || expected != 1 || staged != wantRows || sealed != 0 {
		return ErrConflict
	}
	return nil
}

// stageBasicStreamFact is a bounded EFFECT BODY, not the full BasicStage
// command. Its caller must add the SAME transaction's durable receipt/sequence
// and roll the whole command back on any error. It changes no accepted epoch,
// availability, source revision or Basic reuse witness.
func stageBasicStreamFact(ctx context.Context, turn *commandTurn, in basicStreamFactStage) (value basicFamilyProgress, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil {
		return value, ErrInvalid
	}
	if err = checkBasicStreamStage(ctx, turn, in, 0); err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicStageBudget, int64(1))
	if err != nil {
		return value, err
	}
	var used, cap int64
	if err = row.Scan(&used, &cap); err != nil {
		return value, basicStageError(err)
	}
	// Exact retained family1 logical charge:192 + byte lengths, as in the
	// removed staging charge trigger. This is debt, not W's physical budget.
	if cap != 33554432 || used < 0 || used > cap || len(in.assetToken) > int(cap-used) || len(in.fingerprint) > int(cap-used)-len(in.assetToken) {
		return value, ErrCap
	}
	charge := int64(192) + int64(len(in.assetToken)) + int64(len(in.fingerprint))
	if charge > cap-used {
		return value, ErrCap
	}
	row, err = basicStageRead(ctx, turn, basicStageBudgetCAS, used+charge, int64(1), used, cap)
	if err != nil {
		return value, err
	}
	var actual int64
	if err = row.Scan(&actual); err != nil {
		return value, basicStageError(err)
	}
	if actual != used+charge {
		return value, ErrConflict
	}
	step, ok := turn.steps[basicStageStreamInsert]
	if !ok {
		return value, invalid("unregistered_basic_step")
	}
	result, err := turn.exec(ctx, step.statementStep, in.changeID, int64(0), in.assetToken, in.factRevision, in.size, in.modifiedNS, in.fingerprint, in.detailVersion)
	if err != nil {
		return value, basicStreamFactInsertError(err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return value, err
	}
	if affected != 1 {
		return value, ErrConflict
	}
	row, err = basicStageRead(ctx, turn, basicStageFamilyCAS, int64(1), in.changeID, int64(1), int64(0), int64(1), int64(0), int64(0))
	if err != nil {
		return value, err
	}
	value = basicFamilyProgress{changeID: in.changeID, inputRevision: in.inputRevision}
	if err = row.Scan(&value.rowsStaged, &value.sealed); err != nil {
		return value, basicStageError(err)
	}
	if value.rowsStaged != 1 || value.sealed != 0 {
		return value, ErrConflict
	}
	return value, nil
}

func checkBasicStreamFact(ctx context.Context, turn *commandTurn, in basicStreamFactStage) (err error) {
	step, ok := turn.steps[basicSealStreamFact]
	if !ok {
		return invalid("unregistered_basic_step")
	}
	rows, err := turn.query(ctx, step.statementStep, in.changeID, int64(2))
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	if !rows.Next() {
		if err = rows.Err(); err != nil {
			return err
		}
		return ErrConflict
	}
	var ordinal, revision, size, modified, detail int64
	var token, fingerprint string
	if err = rows.Scan(&ordinal, &token, &revision, &size, &modified, &fingerprint, &detail); err != nil {
		return basicStageError(err)
	}
	if rows.Next() {
		return ErrConflict
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if ordinal != 0 || token != in.assetToken || revision != in.factRevision || size != in.size || modified != in.modifiedNS || fingerprint != in.fingerprint || detail != in.detailVersion {
		return ErrConflict
	}
	return nil
}

func sealBasicStreamFact(ctx context.Context, turn *commandTurn, in basicStreamFactStage) (value basicFamilyProgress, err error) {
	defer func() { value, err = finishCommand(ctx, commandLifetime(turn), value, err) }()
	if ctx == nil || turn == nil || turn.transaction == nil {
		return value, ErrInvalid
	}
	if err = checkBasicStreamStage(ctx, turn, in, 1); err != nil {
		return value, err
	}
	if err = checkBasicStreamFact(ctx, turn, in); err != nil {
		return value, err
	}
	row, err := basicStageRead(ctx, turn, basicSealFamilyCAS, int64(1), in.changeID, int64(1), int64(0), int64(1), int64(1), int64(0))
	if err != nil {
		return value, err
	}
	value = basicFamilyProgress{changeID: in.changeID, inputRevision: in.inputRevision}
	if err = row.Scan(&value.rowsStaged, &value.sealed); err != nil {
		return value, basicStageError(err)
	}
	if value.rowsStaged != 1 || value.sealed != 1 {
		return value, ErrConflict
	}
	return value, nil
}
