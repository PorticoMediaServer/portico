package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

func RBExactFacetDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), rbExactFacetDefinitions()...)
}
func rbExactFacetDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	base := "SELECT id,year,retired,admission_own_age FROM catalog_entities INDEXED BY catalog_entities_year WHERE library_id=? AND kind=?"
	end := " ORDER BY year ASC,id ASC LIMIT ?"
	var out []stmt.Definition
	add := func(name, where, seek string, params []stmt.Param, bindings []any) {
		empty := append([]any(nil), bindings...)
		empty[0] = int64(3)
		out = append(out, stmt.Definition{ID: statementID("rb.prepare.year." + name), SQL: base + where + end, Owner: "store/read", CallSites: []string{"store/read/rb_facet_year.go:NumericFacetPreparation.Advance"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_year"}, Seeks: []string{seek}}})
	}
	add("first", "", "library_id=? AND kind=?", []stmt.Param{i("library"), i("kind"), i("limit")}, []any{int64(1), int64(1), int64(rbFacetRaw)})
	add("tail", " AND year=? AND id>?", "year=? AND id>?", []stmt.Param{i("library"), i("kind"), i("key"), i("after"), i("limit")}, []any{int64(1), int64(1), int64(2001), int64(0), int64(rbFacetRaw)})
	add("next", " AND year>?", "year>?", []stmt.Param{i("library"), i("kind"), i("key"), i("limit")}, []any{int64(1), int64(1), int64(2001), int64(rbFacetRaw)})
	return out
}
