package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IAReadingDefinitions is inert harness composition. The parent deduplicates iaLinkOctetsID.
func IAReadingDefinitions() []stmt.Definition {
	var out []stmt.Definition
	add := func(n, sql string, p []stmt.Param, args []any, pl stmt.Plan) {
		empty := append([]any(nil), args...)
		switch empty[0].(type) {
		case int64:
			empty[0] = int64(999999)
		case string:
			empty[0] = "missing"
		}
		out = append(out, stmt.Definition{ID: readStatementID("foreground.ia.reading." + n), SQL: sql, Params: p, Kind: stmt.Read, Owner: "store/read/ia_reading", CallSites: []string{"store/read/ia_reading.go"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: pl})
	}
	add("entity", `SELECT id,public_id,title,kind,retired,revision,admission_own_age FROM catalog_entities WHERE id=? AND library_id=?`, []stmt.Param{{Name: "item", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}}, []any{int64(4000), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}})
	add("entity_octets", `SELECT octet_length(public_id),octet_length(title) FROM catalog_entities WHERE id=? AND library_id=?`, []stmt.Param{{Name: "item", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}}, []any{int64(4000), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}})
	add("publication", `SELECT format,has_reading_path_head,path_publisher_sequence,path_object_id,path_source_revision,path_author,path_author_source,path_issue_date,path_series,path_series_source,path_series_number FROM reading_publications WHERE item_id=? AND asset_id=?`, []stmt.Param{{Name: "item", Type: stmt.Integer}, {Name: "asset", Type: stmt.Integer}}, []any{int64(4000), int64(4000)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"reading_publications"}, Seeks: []string{"item_id=? AND asset_id=?"}})
	add("publication_octets", `SELECT octet_length(format),octet_length(path_object_id),octet_length(path_source_revision),octet_length(path_author),octet_length(path_author_source),octet_length(path_issue_date),octet_length(path_series),octet_length(path_series_source) FROM reading_publications WHERE item_id=? AND asset_id=?`, []stmt.Param{{Name: "item", Type: stmt.Integer}, {Name: "asset", Type: stmt.Integer}}, []any{int64(4000), int64(4000)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"reading_publications"}, Seeks: []string{"item_id=? AND asset_id=?"}})
	add("type", `SELECT publication_type,source_format,source_field,evidence_complete,inventory_object_id,source_revision,classifier_version FROM reading_publication_types WHERE item_id=? AND asset_id=?`, []stmt.Param{{Name: "item", Type: stmt.Integer}, {Name: "asset", Type: stmt.Integer}}, []any{int64(4000), int64(4000)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"reading_publication_types"}, Seeks: []string{"item_id=? AND asset_id=?"}})
	add("type_octets", `SELECT octet_length(publication_type),octet_length(source_format),octet_length(source_field),octet_length(inventory_object_id),octet_length(source_revision) FROM reading_publication_types WHERE item_id=? AND asset_id=?`, []stmt.Param{{Name: "item", Type: stmt.Integer}, {Name: "asset", Type: stmt.Integer}}, []any{int64(4000), int64(4000)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"reading_publication_types"}, Seeks: []string{"item_id=? AND asset_id=?"}})
	add("asset", `SELECT token,available,playback_origin_revision FROM catalog_assets WHERE id=? AND library_id=?`, []stmt.Param{{Name: "asset", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}}, []any{int64(4000), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}})
	add("asset_octets", `SELECT octet_length(token) FROM catalog_assets WHERE id=? AND library_id=?`, []stmt.Param{{Name: "asset", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}}, []any{int64(4000), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}})
	add("object", `SELECT source_id,relative_path,revision,state,asset_id,retired FROM inventory_objects INDEXED BY sqlite_autoindex_inventory_objects_1 WHERE id=?`, []stmt.Param{{Name: "object", Type: stmt.Text}}, []any{"reading-object"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_objects_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "inventory_objects", Name: "sqlite_autoindex_inventory_objects_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}, Seeks: []string{"id=?"}})
	add("object_octets", `SELECT octet_length(source_id),octet_length(relative_path),octet_length(revision),octet_length(state),octet_length(asset_id) FROM inventory_objects INDEXED BY sqlite_autoindex_inventory_objects_1 WHERE id=?`, []stmt.Param{{Name: "object", Type: stmt.Text}}, []any{"reading-object"}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_objects_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "inventory_objects", Name: "sqlite_autoindex_inventory_objects_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}}, Seeks: []string{"id=?"}})
	add("clock", "SELECT sequence FROM reading_path_clock WHERE singleton=?", []stmt.Param{{Name: "singleton", Type: stmt.Integer}}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"reading_path_clock"}, Seeks: []string{"rowid=?"}})
	add("members", "SELECT asset_id FROM reading_publications WHERE item_id=? ORDER BY asset_id LIMIT 9", []stmt.Param{{Name: "item", Type: stmt.Integer}}, []any{int64(4000)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"reading_publications"}, Seeks: []string{"item_id=?"}})
	out = append(out, iaLinkOctetsDefinition([]any{int64(4000), int64(4000), int64(1)}))
	add("link", `SELECT entity_id,asset_id,part_index,available,start_seconds,end_seconds,edition,playback_origin_incarnation,playback_origin_revision FROM catalog_asset_links WHERE entity_id=? AND asset_id=? AND library_id=?`, []stmt.Param{{Name: "entity", Type: stmt.Integer}, {Name: "asset", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}}, []any{int64(4000), int64(4000), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}})
	return out
}
