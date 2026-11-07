package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicBeginExisting       stmt.ID = "scan.basic_stage.begin_existing"
	basicBeginChangeInsert   stmt.ID = "scan.basic_stage.begin_change_insert"
	basicBeginWitnessInsert  stmt.ID = "scan.basic_stage.begin_witness_insert"
	basicBeginFamiliesInsert stmt.ID = "scan.basic_stage.begin_families_insert"
)

// The first ordinary input has exactly one captured source witness and nine
// closed media families. This finite provider grants no admission or source
// authority; W composes it with the existing asset/budget points and receipt.
func basicBeginDefinitions() []stmt.Definition {
	i := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Integer} }
	s := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	b := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Blob} }
	fp := make([]byte, 32)
	defs := []stmt.Definition{
		{ID: basicBeginExisting, Kind: stmt.Read,
			SQL:     `SELECT id,slot FROM catalog_info_changes WHERE asset_id=? AND asset_token=? ORDER BY slot LIMIT ?`,
			Params:  []stmt.Param{i("asset_id"), s("asset_token"), i("row_limit")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_changes_1"}, Seeks: []string{"asset_id=? AND asset_token=?"}},
			Cohorts: []stmt.Cohort{{Name: "existing_input_conflict", Bindings: []any{int64(41), "basic-asset", int64(3)}}}},
		{ID: basicBeginChangeInsert, Kind: stmt.Write,
			SQL: `INSERT INTO catalog_info_changes(asset_id,asset_token,slot,phase,expected_epoch,input_revision,family_mask,fingerprint,created_ms,job_revision)
VALUES(?,?,?,?,?,?,?,?,?,?) RETURNING id,asset_id,asset_token,slot,phase,expected_epoch,input_revision,family_mask,fingerprint,created_ms,job_revision`,
			Params: []stmt.Param{i("asset_id"), s("asset_token"), i("slot"), i("phase"), i("expected_epoch"), i("input_revision"), i("family_mask"), b("fingerprint"), i("created_ms"), i("job_revision")},
			Plan:   stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "new_ordinary_input", Bindings: []any{int64(41), "basic-asset", int64(0), int64(0), int64(17), int64(1), int64(511), fp, int64(2000), int64(1)}}}},
		{ID: basicBeginWitnessInsert, Kind: stmt.Write,
			SQL: `INSERT INTO catalog_info_input_witnesses(change_id,ordinal,kind,job_id,source_id,source_generation,root_incarnation,object_id,object_revision,policy_revision,expected_choice_revision,fingerprint)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?) RETURNING change_id,ordinal,kind,job_id,source_id,source_generation,root_incarnation,object_id,object_revision,policy_revision,expected_choice_revision,fingerprint`,
			Params: []stmt.Param{i("change_id"), i("ordinal"), i("kind"), s("job_id"), s("source_id"), i("source_generation"), s("root_incarnation"), s("object_id"), s("object_revision"), i("policy_revision"), i("expected_choice_revision"), b("fingerprint")},
			Plan:   stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "one_original_native_witness", Bindings: []any{int64(51), int64(0), int64(0), "basic-run", "basic-source", int64(23), "basic-incarnation", "basic-object", "object:019", int64(29), int64(0), fp}}}},
		{ID: basicBeginFamiliesInsert, Kind: stmt.Write,
			SQL: `INSERT INTO catalog_info_input_families(change_id,family,witness_ordinal,rows_expected,rows_staged,sealed) VALUES(?,?,?,?,?,?)
RETURNING change_id,family,witness_ordinal,rows_expected,rows_staged,sealed`,
			Params: []stmt.Param{i("change_id"), i("family"), i("witness_ordinal"), i("rows_expected"), i("rows_staged"), i("sealed")},
			Plan:   stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "one_of_nine_ordinary_manifests", Bindings: []any{int64(51), int64(1), int64(0), int64(1), int64(0), int64(0)}}}},
	}
	// The body uses this one literal INSERT exactly nine times, one closed family
	// per use. Multi-row VALUES is outside the shared closed grammar.
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_begin.go"}
	}
	return append(defs, basicBeginFirstDefinitions()...)
}

// Full command composition must forecast all nine uses before admission.
// Existing asset/budget reads/CAS and receipt work compose separately.
func basicBeginMaxUses(id stmt.ID) int {
	switch id {
	case basicBeginFamiliesInsert:
		return 9
	case basicBeginExisting, basicBeginChangeInsert, basicBeginWitnessInsert, basicBeginFirstAsset, basicBeginFirstOwners, basicBeginFirstEntityAssets:
		return 1
	default:
		return 0
	}
}
