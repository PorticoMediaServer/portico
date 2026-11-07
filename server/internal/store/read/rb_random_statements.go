package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func RBRandomDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), rbRandomDefinitions()...)
}
func rbRandomDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var out []stmt.Definition
	for _, desc := range []bool{false, true} {
		direction, order, op, first, wrapped := "asc.", "ASC", ">", ">=", "<"
		firstKey, wrappedKey, after := int64(2863311530), int64(0), int64(0)
		if desc {
			direction, order, op, first, wrapped = "desc.", "DESC", "<", "<=", ">"
			firstKey, wrappedKey, after = 1431655765, 4294967295, 1<<62
		}
		base := "SELECT id,random_key,public_id,title,retired,revision,admission_own_age FROM catalog_entities INDEXED BY catalog_entities_random WHERE library_id=? AND kind=?"
		end := " ORDER BY random_key " + order + ",id " + order + " LIMIT ?"
		add := func(name, predicate, seek string, params []stmt.Param, args ...any) {
			bindings := append([]any{int64(1), int64(1)}, args...)
			bindings = append(bindings, int64(MaxRaw))
			empty := append([]any(nil), bindings...)
			empty[0] = int64(3)
			all := []stmt.Param{i("library"), i("kind")}
			all = append(all, params...)
			all = append(all, i("limit"))
			out = append(out, stmt.Definition{ID: statementID("rb.random." + direction + name), SQL: base + predicate + end, Owner: "store/read", CallSites: []string{"store/read/rb_random.go:BrowseReader.RandomWindow"}, Kind: stmt.Read, Params: all, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_random"}, Seeks: []string{seek}}})
		}
		add("first", " AND random_key"+first+"?", "random_key"+op+"?", []stmt.Param{i("seed")}, int64(2000000000))
		add("wrapped", " AND random_key"+wrapped+"?", "random_key"+wrapped+"?", []stmt.Param{i("seed")}, int64(2000000000))
		add("tail", " AND random_key=? AND id"+op+"?", "random_key=? AND id"+op+"?", []stmt.Param{i("key"), i("after")}, firstKey, after)
		add("next", " AND random_key"+op+"?", "random_key"+op+"?", []stmt.Param{i("key")}, firstKey)
		add("wrapped_next", " AND random_key"+op+"? AND random_key"+wrapped+"?", "random_key>? AND random_key<?", []stmt.Param{i("key"), i("seed")}, wrappedKey, int64(2000000000))
	}
	return out
}
