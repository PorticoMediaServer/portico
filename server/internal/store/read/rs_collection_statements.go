package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// CollectionDefinitions is inert SQL metadata for explicit harness composition.
// Capture against the actual reader is required before registration/execution.
func CollectionDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, bindings []any, p stmt.Plan) {
		empty := append([]any(nil), bindings...)
		empty[0] = int64(999999)
		out = append(out, stmt.Definition{ID: readStatementID("rs.collection." + name), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_collection.go:CollectionReader"}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	entity := "e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age"
	add("source", "SELECT "+entity+`,e.collection_revision,e.metadata_incarnation FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=10 AND e.retired=0`, []stmt.Param{i("collection"), i("library")}, []any{int64(1101), int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}})
	base := "SELECT " + entity + `,m.order_key,e.library_id,e.kind,k.container FROM catalog_collection_members AS m INDEXED BY catalog_collection_members_page
CROSS JOIN catalog_entities AS e ON e.id=m.item_id
CROSS JOIN catalog_kinds AS k ON k.id=e.kind
WHERE m.collection_id=?`
	end := " ORDER BY m.order_key,m.item_id LIMIT ?"
	add("first", base+end, []stmt.Param{i("collection"), i("limit")}, []any{int64(1101), int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_page"}, PrimaryKeys: []string{"e", "k"}, Seeks: []string{"collection_id=?"}})
	add("tail", base+" AND m.order_key=? AND m.item_id>?"+end, []stmt.Param{i("collection"), s("key"), i("item"), i("limit")}, []any{int64(1101), "a", int64(1), int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_page"}, PrimaryKeys: []string{"e", "k"}, Seeks: []string{"collection_id=? AND order_key=? AND item_id>?"}})
	add("next", base+" AND m.order_key>?"+end, []stmt.Param{i("collection"), s("key"), i("limit")}, []any{int64(1101), "a", int64(64)}, stmt.Plan{Hot: true, Indexes: []string{"catalog_collection_members_page"}, PrimaryKeys: []string{"e", "k"}, Seeks: []string{"collection_id=? AND order_key>?"}})
	return out
}
