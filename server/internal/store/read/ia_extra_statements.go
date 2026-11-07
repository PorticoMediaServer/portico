package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const iaExtraSQL = `SELECT x.item_id,x.library_id,x.parent_path,x.owner_id,x.kind,x.title,x.asset_id,x.relative_path,l.id FROM item_extras AS x CROSS JOIN catalog_libraries AS l ON l.library_id=x.library_id WHERE x.item_id=?`
const iaExtraOctetsSQL = `SELECT octet_length(library_id),octet_length(parent_path),octet_length(kind),octet_length(title),octet_length(asset_id),octet_length(relative_path) FROM item_extras WHERE item_id=?`

// IAExtraHarnessDefinitions is independently composable. The parent must retain
// shared audio entity/link definitions (including iaLinkOctetsID) only once.
func IAExtraHarnessDefinitions() []stmt.Definition {
	var out []stmt.Definition
	for _, d := range IAAudioHarnessDefinitions() {
		switch string(d.ID) {
		case "read.foreground.ia.audio.entity", "read.foreground.ia.audio.entity_octets", "read.foreground.ia.audio.links", "read.foreground.ia.audio.link", "read.foreground.ia.link_octets":
			out = append(out, d)
		}
	}
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	text := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	add := func(name, sql string, params []stmt.Param, args []any, p stmt.Plan) {
		empty := append([]any(nil), args...)
		empty[0] = int64(9999999)
		out = append(out, stmt.Definition{ID: readStatementID("foreground.ia.extra." + name), SQL: sql, Params: params, Kind: stmt.Read, Owner: "store/read/ia_extra", CallSites: []string{"store/read/ia_extra.go"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	point := stmt.Plan{Hot: true, PrimaryKeys: []string{"item_extras"}, Seeks: []string{"rowid=?"}}
	add("extra_octets", iaExtraOctetsSQL, []stmt.Param{i("item")}, []any{int64(2003)}, point)
	add("extra", iaExtraSQL, []stmt.Param{i("item")}, []any{int64(2003)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"x"}, Indexes: []string{"sqlite_autoindex_catalog_libraries_1"}, Seeks: []string{"rowid=?", "library_id=?"}})
	add("self", `SELECT item_id FROM item_extras WHERE item_id=?`, []stmt.Param{i("item")}, []any{int64(2003)}, point)
	add("owner", `SELECT item_id FROM item_extras INDEXED BY item_extras_owner WHERE owner_id=? AND kind=? AND item_id>? ORDER BY item_id LIMIT ?`, []stmt.Param{i("owner"), text("kind"), i("after"), i("limit")}, []any{int64(2002), "trailer", int64(0), int64(MaxRaw)}, stmt.Plan{Hot: true, Indexes: []string{"item_extras_owner"}, Seeks: []string{"owner_id=? AND kind=? AND item_id>?"}})
	// asset token is unique; preserve the native token and integer link identity.
	out = append(out, stmt.Definition{ID: readStatementID("foreground.ia.extra.asset"), SQL: `SELECT id FROM catalog_assets WHERE token=? AND library_id=?`, Params: []stmt.Param{text("token"), i("library")}, Kind: stmt.Read, Owner: "store/read/ia_extra", CallSites: []string{"store/read/ia_extra.go"}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"asset", int64(1)}}, {Name: "empty", Bindings: []any{"absent", int64(1)}}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_assets_1"}, Seeks: []string{"token=?"}}})
	return out
}
