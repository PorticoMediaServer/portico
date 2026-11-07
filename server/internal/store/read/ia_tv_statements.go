package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IATVDefinitions is inert harness composition data. No product registration is
// performed. Capture must certify this exact closed family before execution.
func IATVDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	point := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}}
	var out []stmt.Definition
	add := func(n, sql string, p []stmt.Param, args []any, plan stmt.Plan) {
		if n == "link_octets" {
			out = append(out, iaLinkOctetsDefinition(args))
			return
		}
		empty := append([]any(nil), args...)
		empty[0] = int64(999999)
		out = append(out, stmt.Definition{ID: readStatementID("foreground.ia.tv." + n), SQL: sql, Params: p, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/ia_tv.go:ForegroundReader"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: plan})
	}
	params := []stmt.Param{i("item"), i("library")}
	args := []any{int64(2201), int64(1)}
	add("entity_octets", "SELECT octet_length(public_id),octet_length(title),octet_length(numbering),octet_length(tv_incarnation) FROM catalog_entities WHERE id=? AND library_id=?", params, args, point)
	add("entity", "SELECT id,public_id,title,kind,parent_id,show_id,season_id,season_number,episode_number,numbering,retired,revision,admission_own_age,tv_incarnation,tv_source_revision,tv_hierarchy_revision,tv_selection_revision,tv_descriptive_revision FROM catalog_entities WHERE id=? AND library_id=?", params, args, point)
	add("source", "SELECT id,kind,parent_id,show_id,season_id,retired,revision,admission_own_age,tv_source_revision,tv_hierarchy_revision,tv_selection_revision,tv_descriptive_revision,public_id,tv_incarnation FROM catalog_entities WHERE id=? AND library_id=?", params, args, point)
	add("source_octets", "SELECT octet_length(public_id),octet_length(tv_incarnation) FROM catalog_entities WHERE id=? AND library_id=?", params, args, point)
	add("children", "SELECT id,retired,admission_own_age,library_id FROM catalog_entities INDEXED BY catalog_entities_parent WHERE parent_id IS NOT NULL AND parent_id=? AND id>? ORDER BY id LIMIT 64", []stmt.Param{i("parent"), i("after")}, []any{int64(2100), int64(0)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_parent"}, Seeks: []string{"parent_id=? AND id>?"}})
	add("asset_source", "SELECT id,available,playback_origin_revision,prepared_source_revision,playback_origin_incarnation FROM catalog_assets WHERE id=? AND library_id=?", []stmt.Param{i("asset"), i("library")}, []any{int64(2000), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}})
	add("asset_source_octets", "SELECT octet_length(playback_origin_incarnation) FROM catalog_assets WHERE id=? AND library_id=?", []stmt.Param{i("asset"), i("library")}, []any{int64(2000), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}})
	// Select the whole RAW cohort before applying any visibility/kind predicate.
	// A ninth link refuses the complete unit, including links that would be denied.
	add("members", "SELECT entity_id FROM catalog_asset_links INDEXED BY catalog_asset_links_asset WHERE asset_id=? ORDER BY entity_id LIMIT 9", []stmt.Param{i("asset")}, []any{int64(2000)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_asset_links_asset"}, Seeks: []string{"asset_id=?"}})
	link := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}}
	lp := []stmt.Param{i("item"), i("asset"), i("library")}
	la := []any{int64(2201), int64(2000), int64(1)}
	add("link_octets", "SELECT octet_length(edition),octet_length(playback_origin_incarnation) FROM catalog_asset_links WHERE entity_id=? AND asset_id=? AND library_id=?", lp, la, link)
	add("link", "SELECT entity_id,asset_id,part_index,available,start_seconds,end_seconds,edition,playback_origin_incarnation,playback_origin_revision FROM catalog_asset_links WHERE entity_id=? AND asset_id=? AND library_id=?", lp, la, link)
	return out
}
