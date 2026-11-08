package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RS related availability statements. They are inert compiled definitions:
// task 09 composes them only through an actual Reader Capture and a bounded
// governor scope. The family deliberately uses closed points on the existing
// catalog_asset_links_order, asset/source/object keys and the accepted
// information-publication flags; it never trusts a bare asset/link flag.
const rsRelatedInventorySourceIDsSQL = `SELECT id FROM library_sources INDEXED BY library_sources_library WHERE library_id=? ORDER BY id LIMIT ?`
const rsRelatedInventorySourceSQL = `SELECT root,configured_root,enabled,health,incarnation,generation FROM library_sources INDEXED BY sqlite_autoindex_library_sources_1 WHERE id=? AND library_id=?`
const rsRelatedInventorySourceOctetsSQL = `SELECT octet_length(root),octet_length(configured_root),octet_length(health),octet_length(incarnation) FROM library_sources INDEXED BY sqlite_autoindex_library_sources_1 WHERE id=? AND library_id=?`
const rsRelatedInventoryVerifySourceSQL = `SELECT 1 FROM library_sources INDEXED BY sqlite_autoindex_library_sources_1 WHERE id=? AND library_id=? AND root=? AND configured_root=? AND enabled=? AND health=? AND incarnation=? AND generation=?`
const rsRelatedInventoryRetiredSQL = `SELECT item_id FROM dvr_catalog_retirements WHERE item_id=?`
const rsRelatedInventoryLinksSQL = `SELECT l.asset_id,l.part_index,l.available,l.info_pending_change FROM catalog_asset_links AS l INDEXED BY catalog_asset_links_order WHERE l.entity_id=? ORDER BY l.part_index,l.asset_id LIMIT ?`
const rsRelatedInventoryAssetSQL = `SELECT token,path,available,size,modified_ns,info_publication_pending FROM catalog_assets WHERE id=? AND library_id=?`
const rsRelatedInventoryAssetOctetsSQL = `SELECT octet_length(token),octet_length(path) FROM catalog_assets WHERE id=? AND library_id=?`
const rsRelatedInventoryVerifyAssetSQL = `SELECT 1 FROM catalog_assets WHERE id=? AND library_id=? AND token=? AND path=? AND available=? AND size=? AND modified_ns=? AND info_publication_pending=?`
const rsRelatedInventoryObjectsSQL = `SELECT o.retired,length(COALESCE(NULLIF(o.state,'available'),'')),length(COALESCE(NULLIF(o.unsupported_reason,''),'')),length(COALESCE(NULLIF(o.root_incarnation,s.incarnation),'')),s.enabled,length(COALESCE(NULLIF(s.health,'root_changed'),'')),length(COALESCE(NULLIF(s.health,'removing'),'')),o.size,o.modified_ns FROM inventory_objects AS o INDEXED BY inventory_objects_reading_asset_object CROSS JOIN library_sources AS s INDEXED BY library_sources_library ON s.id=o.source_id WHERE o.asset_id=? AND s.library_id=? ORDER BY o.id LIMIT ?`

func init() {
	// Exact-byte pairs stay same-snapshot: the payload is decoded only after
	// the registered octet shape rechecks the actual variable-width bytes.
	registerRSBytePoint("rs.related.inventory.source", rsBytePoint{lengths: "rs.related.inventory.source_octets", columns: []int{0, 1, 3, 4}})
	registerRSBytePoint("rs.related.inventory.asset", rsBytePoint{lengths: "rs.related.inventory.asset_octets", columns: []int{0, 1}})
}

// RSRelatedInventoryDefinitions is inert. Composition must select one exact
// definition per ID together with the rest of the related family.
func RSRelatedInventoryDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	add := func(name, sql string, params []stmt.Param, present, empty []any, plan stmt.Plan) stmt.Definition {
		cohorts := []stmt.Cohort{{Name: "empty", Bindings: empty}}
		if present != nil {
			cohorts = []stmt.Cohort{{Name: "present", Bindings: present}, {Name: "empty", Bindings: empty}}
		}
		return stmt.Definition{
			ID:        readStatementID("rs.related.inventory." + name),
			SQL:       sql,
			Kind:      stmt.Read,
			Owner:     "store/read",
			CallSites: []string{"store/read/rs_related_inventory.go:rsRelatedInventory.step"},
			Params:    params,
			Cohorts:   cohorts,
			Plan:      plan,
		}
	}
	pkAsset := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}}
	pkSource := stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_library_sources_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "library_sources", Name: "sqlite_autoindex_library_sources_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}}
	return []stmt.Definition{
		add("source_ids", rsRelatedInventorySourceIDsSQL, []stmt.Param{s("library"), i("limit")}, []any{"movies", int64(MaxRaw + 1)}, []any{"missing", int64(MaxRaw + 1)}, stmt.Plan{Hot: true, Indexes: []string{"library_sources_library"}, Seeks: []string{"library_id=?"}}),
		add("retired", rsRelatedInventoryRetiredSQL, []stmt.Param{i("item")}, nil, []any{int64(999999)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"dvr_catalog_retirements"}}),
		add("source", rsRelatedInventorySourceSQL, []stmt.Param{s("source"), s("library")}, []any{"source", "movies"}, []any{"missing", "movies"}, pkSource),
		add("source_octets", rsRelatedInventorySourceOctetsSQL, []stmt.Param{s("source"), s("library")}, []any{"source", "movies"}, []any{"missing", "movies"}, pkSource),
		add("links", rsRelatedInventoryLinksSQL, []stmt.Param{i("entity"), i("limit")}, []any{int64(1), int64(MaxRaw + 1)}, []any{int64(999999), int64(MaxRaw + 1)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_order"}, Seeks: []string{"entity_id=?"}}),
		add("asset", rsRelatedInventoryAssetSQL, []stmt.Param{i("asset"), i("library")}, []any{int64(1), int64(1)}, []any{int64(999999), int64(1)}, pkAsset),
		add("asset_octets", rsRelatedInventoryAssetOctetsSQL, []stmt.Param{i("asset"), i("library")}, []any{int64(1), int64(1)}, []any{int64(999999), int64(1)}, pkAsset),
		add("objects", rsRelatedInventoryObjectsSQL, []stmt.Param{s("asset"), s("library"), i("limit")}, []any{"asset", "movies", int64(MaxRaw)}, []any{"missing", "movies", int64(MaxRaw)}, stmt.Plan{Hot: true, Indexes: []string{"inventory_objects_reading_asset_object", "library_sources_library"}, Seeks: []string{"asset_id=?"}}),
		add("verify_source", rsRelatedInventoryVerifySourceSQL, []stmt.Param{s("source"), s("library"), s("root"), s("configured"), i("enabled"), s("health"), s("incarnation"), i("generation")}, []any{"source", "movies", "/media", "/media", int64(1), "ready", "inc", int64(1)}, []any{"missing", "movies", "/media", "/media", int64(1), "ready", "inc", int64(1)}, pkSource),
		add("verify_asset", rsRelatedInventoryVerifyAssetSQL, []stmt.Param{i("asset"), i("library"), s("token"), s("path"), i("available"), i("size"), i("mtime"), i("pending")}, []any{int64(1), int64(1), "asset", "/media/title.mkv", int64(1), int64(1000), int64(1), int64(0)}, []any{int64(999999), int64(1), "asset", "/media/title.mkv", int64(1), int64(1000), int64(1), int64(0)}, pkAsset),
	}
}
