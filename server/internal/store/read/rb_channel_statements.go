package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const rbChannelOctetsSQL = "SELECT octet_length(c.config_json),octet_length(g.config_json) FROM lc_channels AS c INDEXED BY sqlite_autoindex_lc_channels_1 CROSS JOIN lc_generations AS g INDEXED BY sqlite_autoindex_lc_generations_1 ON g.channel_id=c.id WHERE c.id=? AND g.id=?"

func RBSharedChannelDefinitions() []stmt.Definition {
	out := rbChannelDefinitions()
	out = append(out, rbHomeBindingDefinitions()...)
	return append(out, rbChannelCandidateDefinitions()...)
}
func rbChannelDefinitions() []stmt.Definition {
	s := func(name string) stmt.Param { return stmt.Param{Name: name, Type: stmt.Text} }
	pin := func(table string) stmt.AutoIndexBinding {
		return stmt.AutoIndexBinding{Table: table, Name: "sqlite_autoindex_" + table + "_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "id", Collation: "BINARY"}}}
	}
	channel, generation, library := pin("lc_channels"), pin("lc_generations"), pin("libraries")
	bridge := stmt.AutoIndexBinding{Table: "catalog_libraries", Name: "sqlite_autoindex_catalog_libraries_1", Origin: "u", Columns: []stmt.IndexColumn{{Name: "library_id", Collation: "BINARY"}}}
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, args, empty []any, policy stmt.Plan) {
		out = append(out, stmt.Definition{ID: statementID("rb.channel." + name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/rb_channel_authority.go:SharedChannelReader.OpenSharedChannelScope"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: policy})
	}
	points := stmt.Plan{Hot: true, Indexes: []string{channel.Name, generation.Name}, AutoIndexes: []stmt.AutoIndexBinding{channel, generation}, Seeks: []string{"id=?"}}
	add("octets", rbChannelOctetsSQL, []stmt.Param{s("channel"), s("generation")}, []any{"channel", "generation"}, []any{"missing", "generation"}, points)
	add("source", `SELECT c.revision,c.config_json,c.enabled,c.removed,g.channel_id,g.config_revision,g.config_json,g.status,g.created_ms FROM lc_channels AS c INDEXED BY sqlite_autoindex_lc_channels_1 CROSS JOIN lc_generations AS g INDEXED BY sqlite_autoindex_lc_generations_1 ON g.channel_id=c.id WHERE c.id=? AND g.id=?`, []stmt.Param{s("channel"), s("generation")}, []any{"channel", "generation"}, []any{"missing", "generation"}, points)
	add("library", `SELECT c.id,c.retired,c.root,l.root,l.data_revision,l.data_order_revision,l.data_metadata_revision,l.age_input_revision,l.age_published_revision,l.age_pending_operations,l.playback_origin_revision,l.playback_origin_incarnation,l.configuration_revision,l.configuration_metadata_revision FROM catalog_libraries AS c INDEXED BY sqlite_autoindex_catalog_libraries_1 CROSS JOIN libraries AS l INDEXED BY sqlite_autoindex_libraries_1 ON l.id=c.library_id WHERE c.library_id=?`, []stmt.Param{s("library")}, []any{"movies"}, []any{"missing"}, stmt.Plan{Hot: true, Indexes: []string{bridge.Name, library.Name}, AutoIndexes: []stmt.AutoIndexBinding{bridge, library}, Seeks: []string{"library_id=?", "id=?"}})
	return out
}
