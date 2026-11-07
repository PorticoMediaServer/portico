package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// RBChronologyDefinitions is a separate inert composition, never a shared list.
func RBChronologyDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), rbChronologyDefinitions()...)
}
func rbChronologyDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	for _, clock := range []Chronology{DateAdded, ContentAdded} {
		column, index := "added_at", "catalog_entities_added_v1"
		kind := int64(1)
		if clock == ContentAdded {
			column, index = "last_content_added_at", "catalog_entities_content_added"
			kind = 2
		}
		for _, desc := range []bool{false, true} {
			direction, order, op := "asc.", "ASC", ">"
			if desc {
				direction, order, op = "desc.", "DESC", "<"
			}
			base := "SELECT id," + column + ",public_id,title,retired,revision,admission_own_age FROM catalog_entities INDEXED BY " + index + " WHERE library_id=? AND kind=?"
			end := " ORDER BY " + column + " " + order + ",id " + order + " LIMIT ?"
			add := func(name, predicate, seek string, params []stmt.Param, args ...any) {
				bindings := append([]any{int64(1), kind}, args...)
				bindings = append(bindings, int64(MaxRaw))
				empty := append([]any(nil), bindings...)
				empty[0] = int64(3)
				all := []stmt.Param{i("library"), i("kind")}
				all = append(all, params...)
				all = append(all, i("limit"))
				out = append(out, stmt.Definition{ID: statementID("rb.chronology." + string(clock) + "." + direction + name), SQL: base + predicate + end, Owner: "store/read", CallSites: []string{"store/read/rb_chronology.go:BrowseReader.ChronologyWindow"}, Kind: stmt.Read, Params: all, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}}})
			}
			add("first", "", "library_id=? AND kind=?", nil)
			id := int64(0)
			key := "2026-01-01T00:00:00.000000000Z"
			if desc {
				id = 1 << 62
				key = "2026-03-01T00:00:00.000000000Z"
			}
			add("null_tail", " AND "+column+" IS NULL AND id"+op+"?", column+"=? AND id"+op+"?", []stmt.Param{i("after")}, id)
			add("values", " AND "+column+" IS NOT NULL", column+">?", nil)
			add("nulls", " AND "+column+" IS NULL", column+"=?", nil)
			add("tail", " AND "+column+"=? AND id"+op+"?", column+"=? AND id"+op+"?", []stmt.Param{s("key"), i("after")}, "2026-02-01T00:00:00.000000000Z", id)
			add("next", " AND "+column+op+"?", column+op+"?", []stmt.Param{s("key")}, key)
		}
	}
	return out
}
