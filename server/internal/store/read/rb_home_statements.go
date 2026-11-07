package read

import "strings"

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RBHomeDefinitions is an inert harness composition. Every executable registry
// still needs Capture on the actual Reader connection.
func RBHomeDefinitions() []stmt.Definition {
	return append(append(RBChronologyDefinitions(), rbHomeDefinitions()...), append(rbHomeActivityDefinitions(), rbHomeMemberDefinitions()...)...)
}

func rbHomeDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, args []any, p stmt.Plan) {
		empty := append([]any(nil), args...)
		if _, ok := empty[0].(string); ok {
			empty[0] = "missing"
		} else {
			empty[0] = int64(999999)
		}
		out = append(out, stmt.Definition{ID: readStatementID("rb.home." + name), SQL: sql, Params: params, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rb_home.go:BrowseReader.HomeRecentTarget", "store/read/rb_home.go:BrowseReader.HomeRecentRow"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	sourceKey := stmt.AutoIndexBinding{Table: "library_sources", Name: "sqlite_autoindex_library_sources_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
	entity := "e.id,e.added_at,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.kind,e.library_id,e.season_id,e.show_id,e.last_content_added_at,e.album_id,e.book_id"
	add("item", "SELECT "+entity+" FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=?", []stmt.Param{i("item"), i("library"), i("kind")}, []any{int64(2000), int64(1), int64(2)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}})
	joins := ` LEFT JOIN catalog_asset_links AS l INDEXED BY catalog_asset_links_available_seek ON l.entity_id=e.id AND l.available=1
 LEFT JOIN catalog_assets AS a ON a.id=l.asset_id AND a.library_id=e.library_id
 LEFT JOIN dvr_catalog_retirements AS gone ON gone.item_id=e.id
 LEFT JOIN inventory_objects AS o INDEXED BY inventory_objects_asset ON o.asset_id=a.token
 LEFT JOIN library_sources AS s INDEXED BY sqlite_autoindex_library_sources_1 ON s.id=o.source_id AND s.library_id=?`
	for _, v := range []struct{ name, column, index string }{{"show", "show_id", "catalog_entities_show_fk"}, {"season", "parent_id", "catalog_entities_parent"}} {
		p := stmt.Plan{Hot: true, PrimaryKeys: []string{"a", "gone"}, Indexes: []string{v.index, "catalog_asset_links_available_seek", "inventory_objects_asset", sourceKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{sourceKey}, Seeks: []string{v.column + "=? AND id>?"}}
		add("members."+v.name, "SELECT "+entity+",a.available,gone.item_id,o.retired,length(NULLIF(o.state,'available')),length(NULLIF(o.unsupported_reason,'')),length(NULLIF(o.root_incarnation,s.incarnation)),s.enabled,length(NULLIF(s.health,'root_changed')),length(NULLIF(s.health,'removing')) FROM catalog_entities AS e INDEXED BY "+v.index+joins+" WHERE "+v.column+" IS NOT NULL AND e."+v.column+"=? AND e.id>? ORDER BY e.id LIMIT ?", []stmt.Param{s("library_name"), i("parent"), i("after"), i("limit")}, []any{"movies", int64(2000), int64(0), int64(MaxRaw)}, p)
	}
	add("assets", `SELECT l.asset_id,a.available,gone.item_id,l.start_seconds,l.end_seconds,a.duration,o.retired,length(NULLIF(o.state,'available')),length(NULLIF(o.unsupported_reason,'')),length(NULLIF(o.root_incarnation,s.incarnation)),s.enabled,length(NULLIF(s.health,'root_changed')),length(NULLIF(s.health,'removing')) FROM catalog_asset_links AS l INDEXED BY catalog_asset_links_available_seek
 LEFT JOIN catalog_assets AS a ON a.id=l.asset_id AND a.library_id=l.library_id
 LEFT JOIN dvr_catalog_retirements AS gone ON gone.item_id=l.entity_id
 LEFT JOIN inventory_objects AS o INDEXED BY inventory_objects_asset ON o.asset_id=a.token
 LEFT JOIN library_sources AS s INDEXED BY sqlite_autoindex_library_sources_1 ON s.id=o.source_id AND s.library_id=?
 WHERE l.entity_id=? AND l.available=1 LIMIT ?`, []stmt.Param{s("library_name"), i("item"), i("limit")}, []any{"movies", int64(1), int64(MaxRaw)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"a", "gone"}, Indexes: []string{"catalog_asset_links_available_seek", "inventory_objects_asset", sourceKey.Name}, AutoIndexes: []stmt.AutoIndexBinding{sourceKey}, Seeks: []string{"entity_id=? AND available=?"}})
	// One point per secondary library fits alongside the five common fence calls
	// in final publication. No JSON table walk or shared fence modification.
	key := func(table, name string, columns ...string) stmt.AutoIndexBinding {
		k := stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk"}
		for _, c := range columns {
			k.Columns = append(k.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return k
	}
	lk := key("libraries", "sqlite_autoindex_libraries_1", "id")
	vk := key("viewer_revisions", "sqlite_autoindex_viewer_revisions_1", "profile_id", "library_id")
	add("library", `SELECT cl.library_id,cl.root,l.data_revision,l.data_order_revision,l.data_metadata_revision,COALESCE(v.revision,0),l.age_input_revision,l.age_published_revision,l.age_pending_operations,l.root,l.playback_origin_incarnation,l.playback_origin_revision,ls.enabled,ls.health,ls.incarnation,ls.generation
FROM catalog_libraries AS cl
CROSS JOIN authorization_family_tokens AS t INDEXED BY sqlite_autoindex_authorization_family_tokens_1 ON t.token_hash=?
CROSS JOIN authorization_session_families AS f INDEXED BY sqlite_autoindex_authorization_session_families_1 ON f.id=t.family_id
CROSS JOIN libraries AS l INDEXED BY sqlite_autoindex_libraries_1 ON l.id=cl.library_id
LEFT JOIN viewer_revisions AS v INDEXED BY sqlite_autoindex_viewer_revisions_1 ON v.profile_id=f.profile_id AND v.library_id=l.id
LEFT JOIN library_sources AS ls INDEXED BY library_sources_library ON ls.library_id=cl.library_id
WHERE cl.id=? AND cl.retired=0 LIMIT 64`, []stmt.Param{s("token"), i("library")}, []any{"token", int64(2)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"cl"}, Indexes: []string{lk.Name, vk.Name, "library_sources_library", "sqlite_autoindex_authorization_family_tokens_1", "sqlite_autoindex_authorization_session_families_1"}, AutoIndexes: []stmt.AutoIndexBinding{lk, vk, key("authorization_family_tokens", "sqlite_autoindex_authorization_family_tokens_1", "token_hash"), key("authorization_session_families", "sqlite_autoindex_authorization_session_families_1", "id")}})
	out[len(out)-1].Cohorts[1].Bindings[0] = "missing"
	for n := range out {
		if strings.Contains(string(out[n].ID), "members.") || strings.HasSuffix(string(out[n].ID), "assets") {
			out[n].Cohorts[1].Bindings[1] = int64(999999)
		}
	}
	return out
}
