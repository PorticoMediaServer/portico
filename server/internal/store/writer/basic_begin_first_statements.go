package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const basicBeginFirstAsset stmt.ID = "scan.basic_stage.begin_first_asset"
const basicBeginFirstOwners stmt.ID = "scan.basic_stage.begin_first_owners"

func basicBeginFirstDefinitions() []stmt.Definition {
	defs := []stmt.Definition{
		{ID: basicBeginFirstAsset, Kind: stmt.Read, SQL: `SELECT id FROM catalog_assets WHERE id=? AND token=? AND library_id=? AND info_epoch=? AND available=1 AND info_publication_pending=0 AND info_owner_object IS NULL AND info_owner_revision IS NULL`, Params: []stmt.Param{{Name: "asset_id", Type: stmt.Integer}, {Name: "asset_token", Type: stmt.Text}, {Name: "library_id", Type: stmt.Integer}, {Name: "expected_epoch", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "unfenced_ordinary_asset", Bindings: []any{int64(41), "basic-asset", int64(7), int64(17)}}}},
		{ID: basicBeginFirstOwners, Kind: stmt.Read, SQL: `SELECT l.entity_id,l.library_id,l.info_shape_id,l.info_duration,l.info_pending_change,e.kind,e.retired,sub.id,sub.library_id,sub.kind
FROM catalog_asset_links AS l INDEXED BY catalog_asset_links_asset
INNER JOIN catalog_entities AS e ON e.id=l.entity_id AND e.library_id=l.library_id
INNER JOIN catalog_subjects AS sub ON sub.id=e.id AND sub.public_id=e.public_id AND sub.library_id=e.library_id AND sub.kind=e.kind
WHERE l.asset_id=? ORDER BY l.entity_id LIMIT 2`, Params: []stmt.Param{{Name: "asset_id", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_asset"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "complete_owner_set", Bindings: []any{int64(41)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic FIRST eligibility"
		defs[n].CallSites = []string{"store/writer/basic_begin_first.go"}
	}
	return defs
}
