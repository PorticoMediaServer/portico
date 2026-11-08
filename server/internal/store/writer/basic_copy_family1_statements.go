package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCopy1Staged    stmt.ID = "scan.basic_copy.family1_staged"
	basicCopy1Canonical stmt.ID = "scan.basic_copy.family1_canonical"
	basicCopy1Children  stmt.ID = "scan.basic_copy.family1_children"
	basicCopy1Delete    stmt.ID = "scan.basic_copy.family1_delete"
	basicCopy1Insert    stmt.ID = "scan.basic_copy.family1_insert"
	basicCopy1Update    stmt.ID = "scan.basic_copy.family1_update"
)

// Family 1 (asset_stream_facts): one row per asset, asset-scoped by token.
// Every statement is a PRIMARY KEY access; nothing scans a family.
func basicCopyFamily1Definitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicCopy1Staged, Kind: stmt.Read,
			SQL:    `SELECT input_ordinal,revision,size,modified_ns,fingerprint,detail_version FROM catalog_info_new_stream_facts WHERE change_id=? AND asset_id=?`,
			Params: []stmt.Param{i("change_id"), t("asset_token")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_new_stream_facts"}, Seeks: []string{"change_id=? AND asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "staged_asset_fact", Bindings: []any{int64(51), "basic-asset"}}}},
		{ID: basicCopy1Canonical, Kind: stmt.Read,
			SQL:    `SELECT revision,size,modified_ns,fingerprint,detail_version FROM asset_stream_facts WHERE asset_id=?`,
			Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_stream_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_asset_fact", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopy1Children, Kind: stmt.Read,
			SQL:    `SELECT stream_index FROM asset_streams WHERE asset_id=? ORDER BY stream_index LIMIT 1`,
			Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_child_veto", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopy1Delete, Kind: stmt.Write,
			SQL:    `DELETE FROM asset_stream_facts WHERE asset_id=? AND revision=? RETURNING asset_id`,
			Params: []stmt.Param{t("asset_token"), i("revision")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_stream_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "obsolete_canonical_fact", Bindings: []any{"basic-asset", int64(1)}}}},
		{ID: basicCopy1Insert, Kind: stmt.Write,
			SQL:    `INSERT INTO asset_stream_facts(asset_id,revision,size,modified_ns,fingerprint,detail_version) VALUES(?,?,?,?,?,?) RETURNING asset_id,revision,size,modified_ns,fingerprint,detail_version`,
			Params: []stmt.Param{t("asset_token"), i("revision"), i("size"), i("modified_ns"), t("fingerprint"), i("detail_version")}, Plan: stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "install_new_staged_fact", Bindings: []any{"basic-asset", int64(31), int64(42), int64(-11), "probe-fingerprint", int64(1)}}}},
		{ID: basicCopy1Update, Kind: stmt.Write,
			SQL:     `UPDATE asset_stream_facts SET revision=?,size=?,modified_ns=?,fingerprint=?,detail_version=? WHERE asset_id=? AND revision=? RETURNING asset_id,revision,size,modified_ns,fingerprint,detail_version`,
			Params:  []stmt.Param{i("revision"), i("size"), i("modified_ns"), t("fingerprint"), i("detail_version"), t("asset_token"), i("expected_revision")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_stream_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "replace_canonical_fact", Bindings: []any{int64(31), int64(42), int64(-11), "probe-fingerprint", int64(1), "basic-asset", int64(30)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_family1.go"}
	}
	return defs
}
