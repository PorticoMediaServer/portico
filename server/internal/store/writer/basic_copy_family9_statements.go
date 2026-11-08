package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCopy9Staged  stmt.ID = "scan.basic_copy.family9_staged"
	basicCopy9Present stmt.ID = "scan.basic_copy.family9_present"
	basicCopy9Insert  stmt.ID = "scan.basic_copy.family9_insert"
	basicCopy9Update  stmt.ID = "scan.basic_copy.family9_update"
	basicCopy9Clear   stmt.ID = "scan.basic_copy.family9_clear"
)

var _ = registerBasicCopyFamily(9, basicCopyFamilySpec{copy: copyBasicFamily9, defs: basicCopyFamily9Definitions, maxUses: basicCopyFamily9MaxUses})

func basicCopyFamily9MaxUses(id stmt.ID) int {
	switch id {
	case basicCopy9Staged, basicCopy9Present, basicCopy9Insert, basicCopy9Update, basicCopy9Clear:
		return 1
	}
	return 0
}

// Family 9 (subtitle inventory evidence): one row per asset, folded into the
// asset_subtitle_facts row in schema v1. Every statement is a point access.
func basicCopyFamily9Definitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	defs := []stmt.Definition{
		{ID: basicCopy9Staged, Kind: stmt.Read,
			SQL:    `SELECT input_ordinal,revision,evidence FROM catalog_info_new_subtitle_inventory_evidence WHERE change_id=? AND asset_id=?`,
			Params: []stmt.Param{i("change_id"), t("asset_token")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_new_subtitle_inventory_evidence"}, Seeks: []string{"change_id=? AND asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "staged_subtitle_evidence", Bindings: []any{int64(51), "basic-asset"}}}},
		{ID: basicCopy9Present, Kind: stmt.Read,
			SQL:    `SELECT revision,has_subtitle_inventory_evidence,inventory_revision,inventory_evidence FROM asset_subtitle_facts WHERE asset_id=?`,
			Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitle_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "folded_canonical_evidence", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopy9Insert, Kind: stmt.Write,
			SQL:    `UPDATE asset_subtitle_facts SET has_subtitle_inventory_evidence=1,inventory_revision=?,inventory_evidence=? WHERE asset_id=? AND has_subtitle_inventory_evidence=0 AND inventory_revision IS NULL AND inventory_evidence IS NULL RETURNING asset_id,has_subtitle_inventory_evidence,inventory_revision,inventory_evidence`,
			Params: []stmt.Param{i("revision"), t("evidence"), t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitle_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "install_absent_evidence", Bindings: []any{int64(31), "inventory:31", "basic-asset"}}}},
		{ID: basicCopy9Update, Kind: stmt.Write,
			SQL:    `UPDATE asset_subtitle_facts SET has_subtitle_inventory_evidence=1,inventory_revision=?,inventory_evidence=? WHERE asset_id=? AND has_subtitle_inventory_evidence=1 AND inventory_revision=? AND inventory_evidence=? RETURNING asset_id,has_subtitle_inventory_evidence,inventory_revision,inventory_evidence`,
			Params: []stmt.Param{i("revision"), t("evidence"), t("asset_token"), i("expected_revision"), t("expected_evidence")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitle_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "replace_canonical_evidence", Bindings: []any{int64(31), "inventory:31", "basic-asset", int64(30), "inventory:30"}}}},
		{ID: basicCopy9Clear, Kind: stmt.Write,
			SQL:    `UPDATE asset_subtitle_facts SET has_subtitle_inventory_evidence=?,inventory_revision=?,inventory_evidence=? WHERE asset_id=? AND has_subtitle_inventory_evidence=1 AND inventory_revision=? AND inventory_evidence=? RETURNING asset_id,has_subtitle_inventory_evidence,inventory_revision,inventory_evidence`,
			Params: []stmt.Param{i("next_has_evidence"), ni("next_revision"), nt("next_evidence"), t("asset_token"), i("revision"), t("evidence")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitle_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "clear_obsolete_evidence", Bindings: []any{int64(0), nil, nil, "basic-asset", int64(30), "inventory:30"}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_family9.go"}
	}
	return defs
}
