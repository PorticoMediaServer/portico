package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Inert, own definitions only. Composition must Capture its actual Reader.
func rbHomeBindingDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	key := func(table, name string, cols ...string) stmt.AutoIndexBinding {
		k := stmt.AutoIndexBinding{Table: table, Name: name, Origin: "pk"}
		for _, c := range cols {
			k.Columns = append(k.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return k
	}
	lib := key("libraries", "sqlite_autoindex_libraries_1", "id")
	src := key("library_sources", "sqlite_autoindex_library_sources_1", "id")
	add := func(name, sql string, p []stmt.Param, args, empty []any, plan stmt.Plan) stmt.Definition {
		return stmt.Definition{ID: readStatementID("rb.home.binding." + name), Owner: "store/read", Kind: stmt.Read, CallSites: []string{"store/read/rb_home_binding.go:readHomeWholePlayableBinding"}, SQL: sql, Params: p, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: plan}
	}
	return []stmt.Definition{
		add("item", `SELECT e.retired,e.public_id,e.metadata_incarnation,e.playback_origin_incarnation,e.playback_origin_revision,e.metadata_revision,e.source_revision,e.identity_revision,cl.library_id,gone.item_id,recording.item_id,length(NULLIF(cl.root,l.root)),l.age_pending_operations,l.age_input_revision,l.age_published_revision,l.playback_origin_incarnation,l.playback_origin_revision,l.data_revision,l.data_metadata_revision,e.kind,e.revision
 FROM catalog_entities AS e CROSS JOIN catalog_kinds AS k ON k.id=e.kind AND k.playable=1
 CROSS JOIN catalog_libraries AS cl ON cl.id=e.library_id AND cl.retired=0
 CROSS JOIN libraries AS l INDEXED BY sqlite_autoindex_libraries_1 ON l.id=cl.library_id
 LEFT JOIN dvr_catalog_retirements AS gone ON gone.item_id=e.id
 LEFT JOIN dvr_catalog_provenance AS recording ON recording.item_id=e.id
 WHERE e.id=? AND e.library_id=?`, []stmt.Param{i("item"), i("library")}, []any{int64(1), int64(1)}, []any{int64(999999), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "k", "cl", "gone", "recording"}, Indexes: []string{lib.Name}, AutoIndexes: []stmt.AutoIndexBinding{lib}}),
		add("links", `SELECT l.asset_id,l.part_index,l.start_seconds,l.end_seconds,l.info_pending_change,a.info_publication_pending,a.available,a.playback_origin_incarnation,a.playback_origin_revision,l.playback_origin_incarnation,l.playback_origin_revision,a.prepared_source_revision,a.duration,l.info_duration,a.token,l.library_id,l.available,a.size,a.modified_ns
 FROM catalog_asset_links AS l INDEXED BY catalog_asset_links_order
 LEFT JOIN catalog_assets AS a ON a.id=l.asset_id AND a.library_id=l.library_id AND a.library_id=?
 WHERE l.entity_id=? ORDER BY l.part_index,l.asset_id LIMIT ?`, []stmt.Param{i("library"), i("item"), i("limit")}, []any{int64(1), int64(1), int64(2)}, []any{int64(1), int64(999999), int64(2)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"a"}, Indexes: []string{"catalog_asset_links_order"}, Seeks: []string{"entity_id=?"}}),
		add("objects", `SELECT o.id,o.revision,o.source_id,s.incarnation,o.retired,length(NULLIF(o.state,'available')),length(NULLIF(o.unsupported_reason,'')),length(NULLIF(o.root_incarnation,s.incarnation)),s.enabled,length(NULLIF(s.health,'root_changed')),length(NULLIF(s.health,'removing')),s.generation,length(NULLIF(s.library_id,?)),o.size,o.modified_ns,s.root,s.configured_root,s.identity_confirmed
 FROM inventory_objects AS o INDEXED BY inventory_objects_reading_asset_object
 LEFT JOIN library_sources AS s INDEXED BY sqlite_autoindex_library_sources_1 ON s.id=o.source_id
 WHERE o.asset_id=? ORDER BY o.id LIMIT ?`, []stmt.Param{s("library"), s("asset"), i("limit")}, []any{"movies", "home-1", int64(MaxRaw)}, []any{"movies", "missing", int64(MaxRaw)}, stmt.Plan{Hot: true, Indexes: []string{"inventory_objects_reading_asset_object", src.Name}, AutoIndexes: []stmt.AutoIndexBinding{src}, Seeks: []string{"asset_id=?"}}),
	}
}
