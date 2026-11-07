package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RBNumericTitleFilterDefinitions is inert explicit harness inventory. All
// request values are bindings; request fields cannot select SQL or identifiers.
func RBNumericTitleFilterDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var defs []stmt.Definition
	for _, keep := range []bool{false, true} {
		key, index, variant := "sort_key", "catalog_entities_title", "ignore."
		if keep {
			key, index, variant = "sort_key_keep_articles", "catalog_entities_title_keep_articles", "keep."
		}
		for _, desc := range []bool{false, true} {
			direction, op, order := "asc.", ">", "ASC"
			if desc {
				direction, op, order = "desc.", "<", "DESC"
			}
			base := "SELECT id," + key + ",public_id,title,retired,revision,admission_own_age,year FROM catalog_entities INDEXED BY " + index + " WHERE library_id=? AND kind=?"
			end := " ORDER BY " + key + " COLLATE NOCASE " + order + ",id " + order + " LIMIT ?"
			add := func(part, sql, seek string, params []stmt.Param, bindings []any) {
				empty := append([]any(nil), bindings...)
				empty[0] = int64(-1)
				defs = append(defs, stmt.Definition{ID: readStatementID("rb.filter.numeric.title." + variant + direction + part), Owner: "store/read", Kind: stmt.Read, CallSites: []string{"store/read/rb_filter_numeric_title.go:BrowseReader.NumericTitleFilterWindow"}, SQL: sql, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}}})
			}
			add("first", base+end, "library_id=? AND kind=?", []stmt.Param{i("library"), i("kind"), i("limit")}, []any{int64(1), int64(1), int64(MaxRaw)})
			add("tail", base+" AND "+key+" COLLATE NOCASE=? AND id"+op+"?"+end, key+"=? AND id"+op+"?", []stmt.Param{i("library"), i("kind"), s("key"), i("after"), i("limit")}, []any{int64(1), int64(1), "same", int64(3), int64(MaxRaw)})
			add("next", base+" AND "+key+" COLLATE NOCASE"+op+"?"+end, key+op+"?", []stmt.Param{i("library"), i("kind"), s("key"), i("limit")}, []any{int64(1), int64(1), "same", int64(MaxRaw)})
		}
	}
	return defs
}
