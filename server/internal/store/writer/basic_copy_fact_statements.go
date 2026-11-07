package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCopyFactScope   stmt.ID = "scan.basic_stage.copy_fact_scope"
	basicCopyFactPresent stmt.ID = "scan.basic_stage.copy_fact_present"
	basicCopyFactInsert  stmt.ID = "scan.basic_stage.copy_fact_insert"
	basicCopyFactUpdate  stmt.ID = "scan.basic_stage.copy_fact_update"
)

func basicCopyFactDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicCopyFactScope, Kind: stmt.Read, SQL: `SELECT a.info_publication_pending,l.info_pending_change,a.file_size_revision,a.info_invalidation_revision,l.library_id FROM catalog_assets AS a INNER JOIN catalog_asset_links AS l ON l.asset_id=a.id WHERE a.id=? AND l.entity_id=? AND l.asset_id=?`, Params: []stmt.Param{i("asset_id"), i("entity_id"), i("linked_asset_id")},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"a", "l"}, Seeks: []string{"rowid=?", "entity_id=? AND asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "fenced_single_holder", Bindings: []any{int64(41), int64(31), int64(41)}}}},
		{ID: basicCopyFactPresent, Kind: stmt.Read, SQL: `SELECT 1 FROM asset_stream_facts WHERE asset_id=?`, Params: []stmt.Param{t("asset_token")},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_stream_facts_1"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "canonical_fact_key", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopyFactInsert, Kind: stmt.Write, SQL: `INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint,detail_version) VALUES(?,?,?,?,?,?) RETURNING asset_id,revision,size,modified_ns,fingerprint,detail_version`, Params: []stmt.Param{t("asset_token"), i("revision"), i("size"), i("modified_ns"), t("fingerprint"), i("detail_version")},
			Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "first_canonical_probe", Bindings: []any{"basic-asset", int64(31), int64(42), int64(-11), "probe-fingerprint", int64(1)}}}},
		{ID: basicCopyFactUpdate, Kind: stmt.Write, SQL: `UPDATE asset_stream_facts SET revision=?,size=?,modified_ns=?,fingerprint=?,detail_version=? WHERE asset_id=? AND revision=? AND size=? AND modified_ns=? AND fingerprint=? AND detail_version=? RETURNING asset_id,revision,size,modified_ns,fingerprint,detail_version`, Params: []stmt.Param{i("next_revision"), i("next_size"), i("next_modified_ns"), t("next_fingerprint"), i("next_detail_version"), t("asset_token"), i("revision"), i("size"), i("modified_ns"), t("fingerprint"), i("detail_version")},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_stream_facts_1"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "replace_exact_captured_fact", Bindings: []any{int64(31), int64(42), int64(-11), "probe-fingerprint", int64(1), "basic-asset", int64(30), int64(40), int64(-12), "old-probe", int64(0)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_fact.go"}
	}
	return defs
}
