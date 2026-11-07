package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Inert own inventory. Composition captures these and the unchanged common
// principal/saved-view recipes on its actual Reader; this registers no runtime.
func rbHomeManifestDefinitions() []stmt.Definition {
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	integer := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	pk := func(table, name, origin string, cols ...string) stmt.AutoIndexBinding {
		p := stmt.AutoIndexBinding{Table: table, Name: name, Origin: origin}
		for _, c := range cols {
			p.Columns = append(p.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		return p
	}
	lib := pk("libraries", "sqlite_autoindex_libraries_1", "pk", "id")
	cl := pk("catalog_libraries", "sqlite_autoindex_catalog_libraries_1", "u", "library_id")
	viewer := pk("viewer_revisions", "sqlite_autoindex_viewer_revisions_1", "pk", "profile_id", "library_id")
	source := pk("server_setting_sources", "sqlite_autoindex_server_setting_sources_1", "pk", "field")
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, args []any, plan stmt.Plan) {
		out = append(out, stmt.Definition{ID: readStatementID("rb.home.manifest." + name), Owner: "store/read", Kind: stmt.Read, CallSites: []string{"store/read/rb_home_manifest.go"}, SQL: sql, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}}, Plan: plan})
	}
	actor := []stmt.Param{text("authority"), text("account"), text("profile"), text("scope"), text("device")}
	bindings := []any{"local", "a", "p", "profile-server", ""}
	where := " WHERE authority=? AND account_id=? AND profile_id=? AND scope=? AND device_key=?"
	add("scope", "SELECT revision,updated_ms FROM preference_scopes"+where, actor, bindings, stmt.Plan{Hot: true, PrimaryKeys: []string{"preference_scopes"}, Seeks: []string{"authority=? AND account_id=? AND profile_id=? AND scope=? AND device_key=?"}})
	params := append(append([]stmt.Param{}, actor...), text("key"))
	args := append(append([]any{}, bindings...), "home.rowOrder")
	keywhere := where + " AND key=?"
	add("key", "SELECT length(NULLIF(kind,'string-list')),bool_value,number_value FROM preference_values"+keywhere, params, args, stmt.Plan{Hot: true, PrimaryKeys: []string{"preference_values"}, Seeks: []string{"authority=? AND account_id=? AND profile_id=? AND scope=? AND device_key=? AND key=?"}})
	add("positions", "SELECT position FROM preference_list_items"+keywhere+" ORDER BY position LIMIT 65", params, args, stmt.Plan{Hot: true, PrimaryKeys: []string{"preference_list_items"}, Seeks: []string{"authority=? AND account_id=? AND profile_id=? AND scope=? AND device_key=? AND key=?"}})
	pointParams := append(append([]stmt.Param{}, params...), integer("position"))
	pointArgs := append(append([]any{}, args...), int64(0))
	pointwhere := keywhere + " AND position=?"
	pointPlan := stmt.Plan{Hot: true, PrimaryKeys: []string{"preference_list_items"}, Seeks: []string{"authority=? AND account_id=? AND profile_id=? AND scope=? AND device_key=? AND key=? AND position=?"}}
	add("list.bytes", "SELECT octet_length(value) FROM preference_list_items"+pointwhere, pointParams, pointArgs, pointPlan)
	add("list.value", "SELECT value FROM preference_list_items"+pointwhere, pointParams, pointArgs, pointPlan)
	add("community", "SELECT updated_ms,\"home.communityActivityEnabled\" FROM server_settings WHERE singleton=?", []stmt.Param{integer("singleton")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"server_settings"}})
	add("community.source", "SELECT source FROM server_setting_sources INDEXED BY sqlite_autoindex_server_setting_sources_1 WHERE field=?", []stmt.Param{text("field")}, []any{"home.communityActivityEnabled"}, stmt.Plan{Hot: true, Indexes: []string{source.Name}, AutoIndexes: []stmt.AutoIndexBinding{source}, Seeks: []string{"field=?"}})
	add("settings.revision", "SELECT revision FROM console_settings_revision WHERE singleton=?", []stmt.Param{integer("singleton")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"console_settings_revision"}})
	add("libraries", "SELECT id FROM libraries INDEXED BY sqlite_autoindex_libraries_1 WHERE id>=? ORDER BY id LIMIT 17", []stmt.Param{text("start")}, []any{""}, stmt.Plan{Hot: true, Indexes: []string{lib.Name}, AutoIndexes: []stmt.AutoIndexBinding{lib}, Seeks: []string{"id>?"}})
	libPlan := stmt.Plan{Hot: true, Indexes: []string{lib.Name}, AutoIndexes: []stmt.AutoIndexBinding{lib}, Seeks: []string{"id=?"}}
	add("library.bytes", "SELECT octet_length(name),octet_length(kind) FROM libraries INDEXED BY sqlite_autoindex_libraries_1 WHERE id=?", []stmt.Param{text("library")}, []any{"movies"}, libPlan)
	add("library", "SELECT name,kind,position,data_revision,data_order_revision,data_metadata_revision,age_input_revision,age_published_revision,age_pending_operations,configuration_present,configuration_revision,preference_present,preference_revision FROM libraries INDEXED BY sqlite_autoindex_libraries_1 WHERE id=?", []stmt.Param{text("library")}, []any{"movies"}, libPlan)
	add("catalogue", "SELECT id,retired FROM catalog_libraries INDEXED BY sqlite_autoindex_catalog_libraries_1 WHERE library_id=?", []stmt.Param{text("library")}, []any{"movies"}, stmt.Plan{Hot: true, Indexes: []string{cl.Name}, AutoIndexes: []stmt.AutoIndexBinding{cl}, Seeks: []string{"library_id=?"}})
	add("viewer", "SELECT revision FROM viewer_revisions INDEXED BY sqlite_autoindex_viewer_revisions_1 WHERE profile_id=? AND library_id=?", []stmt.Param{text("profile"), text("library")}, []any{"p", "movies"}, stmt.Plan{Hot: true, Indexes: []string{viewer.Name}, AutoIndexes: []stmt.AutoIndexBinding{viewer}, Seeks: []string{"profile_id=? AND library_id=?"}})
	return out
}
