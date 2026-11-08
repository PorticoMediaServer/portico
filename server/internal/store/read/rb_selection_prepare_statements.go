package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Inert definitions; the caller must Capture the actual Reader before use.
// Every source leg reuses an existing index and seek:
//
//   - the ID recipe is the covering catalog_entities_library library/kind/id
//     seek used by the ordered-ID preparation;
//   - the title recipes are the existing dual title points, one payload and one
//     same-snapshot octet leg each.
//
// The payload carries the maintained duration metric so ONE completed walk can
// answer rank, range, selection and duration together, without a second count
// scan or an OFFSET. No new index or table is introduced.
func RBSelectionPrepareDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), rbSelectionPrepareDefinitions()...)
}

func rbSelectionPrepareDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	s := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, args, empty []any, plan stmt.Plan) {
		out = append(out, stmt.Definition{ID: statementID("rb.selection_prepare." + name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/rb_selection_prepare.go:RBSelectionPreparation.window"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: plan})
	}
	base := "SELECT e.id,e.retired,e.revision,e.admission_own_age,e.duration_key FROM catalog_entities AS e INDEXED BY catalog_entities_library WHERE e.library_id=? AND e.kind=?"
	end := " ORDER BY e.id ASC LIMIT ?"
	limit := int64(rbSelectionRaw + 1)
	add("first", base+end, []stmt.Param{i("library"), i("kind"), i("limit")}, []any{int64(1), int64(1), limit}, []any{int64(999999), int64(1), limit}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library"}, Seeks: []string{"library_id=? AND kind=?"}})
	add("tail", base+" AND e.id>?"+end, []stmt.Param{i("library"), i("kind"), i("after"), i("limit")}, []any{int64(1), int64(1), int64(0), limit}, []any{int64(999999), int64(1), int64(0), limit}, stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library"}, Seeks: []string{"library_id=? AND kind=? AND id>?"}})

	for _, keep := range []bool{false, true} {
		key, index, variant := "sort_key", "catalog_entities_title", "ignore."
		if keep {
			key, index, variant = "sort_key_keep_articles", "catalog_entities_title_keep_articles", "keep."
		}
		for _, part := range []string{"first", "tail", "next"} {
			where := " WHERE e.library_id=? AND e.kind=?"
			seek := "library_id=? AND kind=?"
			params := []stmt.Param{i("library"), i("kind")}
			args := []any{int64(1), int64(1)}
			if part == "tail" {
				where += " AND e." + key + " COLLATE NOCASE=? AND e.id>?"
				seek = key + "=? AND id>?"
				params = append(params, s("key"), i("after"))
				args = append(args, "same", int64(3))
			}
			if part == "next" {
				where += " AND e." + key + " COLLATE NOCASE>?"
				seek = key + ">?"
				params = append(params, s("key"))
				args = append(args, "same")
			}
			params = append(params, i("limit"))
			args = append(args, limit)
			end := " ORDER BY e." + key + " COLLATE NOCASE ASC,e.id ASC LIMIT ?"
			from := " FROM catalog_entities AS e INDEXED BY " + index
			columns := "e.id,e." + key + ",e.title,e.retired,e.revision,e.admission_own_age,e.duration_key"
			empty := append([]any(nil), args...)
			empty[0] = int64(-1)
			add("title."+variant+part, "SELECT "+columns+from+where+end, params, args, empty, stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}})
			lengths := append([]stmt.Param(nil), params...)
			add("title."+variant+part+".bytes", "SELECT e.id,octet_length(e."+key+"),octet_length(e.title)"+from+where+end, lengths, args, empty, stmt.Plan{Hot: true, Indexes: []string{index}, Seeks: []string{seek}})
		}
	}
	return out
}
