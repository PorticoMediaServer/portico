package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const basicAcceptOwners stmt.ID = "scan.basic_accept.owners"
const basicAcceptSuccessor stmt.ID = "scan.basic_accept.successor"

// Two native owner/successor points used by FIRST leaf settlement. Input
// staging/freezing remains separate; this provider cannot advance its cursor.
func basicStreamAcceptDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicAcceptOwners, Kind: stmt.Read, SQL: `SELECT entity_id,library_id,cue_ordinal FROM catalog_asset_links INDEXED BY catalog_asset_links_asset
WHERE asset_id=? ORDER BY entity_id LIMIT ?`, Params: []stmt.Param{i("asset_id"), i("row_limit")}, Cohorts: []stmt.Cohort{{Name: "one_ordinary_owner", Bindings: []any{int64(41), int64(2)}}}},
		{ID: basicAcceptSuccessor, Kind: stmt.Read, SQL: `SELECT id FROM catalog_info_changes WHERE asset_id=? AND asset_token=? AND slot=?`, Params: []stmt.Param{i("asset_id"), s("asset_token"), i("slot")}, Cohorts: []stmt.Cohort{{Name: "successor_absent", Bindings: []any{int64(41), "basic-asset", int64(1)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_accept.go"}
		defs[n].Plan.Hot = true
		if defs[n].ID == basicAcceptOwners {
			defs[n].Plan.Indexes = []string{"catalog_asset_links_asset"}
			defs[n].Plan.Seeks = []string{"asset_id=?"}
		} else {
			defs[n].Plan.Indexes = []string{"sqlite_autoindex_catalog_info_changes_1"}
			defs[n].Plan.Seeks = []string{"asset_id=? AND asset_token=? AND slot=?"}
		}
	}
	return defs
}
