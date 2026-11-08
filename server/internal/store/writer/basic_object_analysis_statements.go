package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicObjectAnalysisPolicy stmt.ID = "scan.basic_object_analysis.policy"
	basicObjectAnalysisCAS    stmt.ID = "scan.basic_object_analysis.cas"
)

func basicObjectAnalysisDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicObjectAnalysisPolicy, Kind: stmt.Read, SQL: `SELECT revision FROM library_scan_policies WHERE library_id=? AND revision=? AND operations_json=?`, Params: []stmt.Param{t("library_key"), i("policy_revision"), t("policy_json")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_scan_policies_1"}, Seeks: []string{"library_id=?"}}, Cohorts: []stmt.Cohort{{Name: "exact_native_policy", Bindings: []any{"basic-library", int64(29), `["probe"]`}}}},
		{ID: basicObjectAnalysisCAS, Kind: stmt.Write, SQL: `UPDATE inventory_objects INDEXED BY inventory_objects_reading_source_object SET analysis_revision=?,analysis_policy=?,analysis_operations_json=?,analysis_state=?,analysis_error=?,analysis_algorithm=?,analysis_generation=? WHERE id=? AND source_id=? AND asset_id=? AND root_incarnation=? AND revision=? AND size=? AND modified_ns=? AND seen_job=? AND state='available' AND retired=0 AND analysis_revision='' AND analysis_policy=0 AND analysis_operations_json='[]' AND analysis_state='pending' AND analysis_error='' AND analysis_algorithm='' AND analysis_generation=0 RETURNING id,source_id,asset_id,root_incarnation,revision,seen_job,size,modified_ns,analysis_revision,analysis_policy,analysis_operations_json,analysis_state,analysis_error,analysis_algorithm,analysis_generation`, Params: []stmt.Param{t("analysis_revision"), i("analysis_policy"), t("operations_json"), t("analysis_state"), t("analysis_error"), t("algorithm"), i("generation"), t("object_id"), t("source_id"), t("asset_token"), t("incarnation"), t("object_revision"), i("size"), i("modified_ns"), t("run_id")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"inventory_objects_reading_source_object"}, Seeks: []string{"source_id=? AND id=?"}}, Cohorts: []stmt.Cohort{{Name: "first_native_analysis", Bindings: []any{"object:019", int64(29), `["probe"]`, "complete", "", "actual-algorithm", int64(23), "basic-object", "basic-source", "basic-asset", "basic-incarnation", "object:019", int64(42), int64(-11), "basic-run"}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_object_analysis.go"}
	}
	return defs
}
