package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func RBResolutionDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), rbResolutionDefinitions()...)
}
func rbResolutionDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	base := "SELECT id,resolution_key,public_id,title,retired,revision,admission_own_age FROM catalog_entities INDEXED BY catalog_entities_resolution WHERE library_id=? AND kind=? AND kind IN(1,2,3,4,10)"
	var out []stmt.Definition
	for _, desc := range []bool{false, true} {
		dir, order, op := "asc.", "ASC", ">"
		after := int64(0)
		if desc {
			dir, order, op = "desc.", "DESC", "<"
			after = 1 << 62
		}
		add := func(name, where, end, seek string, params []stmt.Param, args []any) {
			empty := append([]any(nil), args...)
			empty[0] = int64(3)
			out = append(out, stmt.Definition{ID: statementID("rb.resolution." + dir + name), SQL: base + where + end, Owner: "store/read", CallSites: []string{"store/read/rb_resolution.go:BrowseReader.ResolutionWindow"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_resolution"}, Seeks: []string{seek}}})
		}
		end := " ORDER BY resolution_key " + order + ",id " + order + " LIMIT ?"
		add("value.first", " AND resolution_key IS NOT NULL", end, "resolution_key>?", []stmt.Param{i("library"), i("kind"), i("limit")}, []any{int64(1), int64(1), int64(MaxRaw)})
		add("value.tail", " AND resolution_key=? AND id"+op+"?", end, "resolution_key=? AND id"+op+"?", []stmt.Param{i("library"), i("kind"), i("key"), i("after"), i("limit")}, []any{int64(1), int64(1), int64(720), after, int64(MaxRaw)})
		add("value.next", " AND resolution_key"+op+"?", end, "resolution_key"+op+"?", []stmt.Param{i("library"), i("kind"), i("key"), i("limit")}, []any{int64(1), int64(1), int64(720), int64(MaxRaw)})
		predicate := " AND resolution_key IS NULL"
		end = " ORDER BY id " + order + " LIMIT ?"
		add("null.first", predicate, end, "resolution_key=?", []stmt.Param{i("library"), i("kind"), i("limit")}, []any{int64(1), int64(1), int64(MaxRaw)})
		add("null.tail", predicate+" AND id"+op+"?", end, "resolution_key=? AND id"+op+"?", []stmt.Param{i("library"), i("kind"), i("after"), i("limit")}, []any{int64(1), int64(1), after, int64(MaxRaw)})
	}
	return out
}
