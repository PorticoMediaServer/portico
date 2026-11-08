package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Inert definitions; the caller must Capture the actual Reader before use.
// The complete entity ID window is a covering library/kind/id seek. The point
// pair hydrates one typed key through the complete entity primary key and
// preflights its exact variable bytes in the same snapshot. Actual title-key
// streams reuse the dual title points instead of re-sorting these IDs.
func RBOrderPrepareDefinitions() []stmt.Definition {
	return append(HarnessDefinitions(), rbOrderPrepareDefinitions()...)
}

func rbOrderPrepareDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	base := "SELECT e.id,e.retired,e.revision,e.admission_own_age FROM catalog_entities AS e INDEXED BY catalog_entities_library WHERE e.library_id=? AND e.kind=?"
	end := " ORDER BY e.id ASC LIMIT ?"
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, args, empty []any, seek string) {
		out = append(out, stmt.Definition{ID: statementID("rb.order_prepare." + name), SQL: sql, Owner: "store/read", CallSites: []string{"store/read/rb_order_prepare.go:RBOrderPreparation.Advance"}, Kind: stmt.Read, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: args}, {Name: "empty", Bindings: empty}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library"}, Seeks: []string{seek}}})
	}
	add("first", base+end, []stmt.Param{i("library"), i("kind"), i("limit")}, []any{int64(1), int64(1), int64(rbOrderRaw)}, []any{int64(999999), int64(1), int64(rbOrderRaw)}, "library_id=? AND kind=?")
	add("tail", base+" AND e.id>?"+end, []stmt.Param{i("library"), i("kind"), i("after"), i("limit")}, []any{int64(1), int64(1), int64(0), int64(rbOrderRaw)}, []any{int64(999999), int64(1), int64(0), int64(rbOrderRaw)}, "library_id=? AND kind=? AND id>?")

	point := stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}, Seeks: []string{"rowid=?"}}
	out = append(out,
		stmt.Definition{ID: statementID("rb.order_prepare.point"), SQL: "SELECT e.id,e.sort_key,e.sort_key_keep_articles,e.title,e.retired,e.revision,e.admission_own_age FROM catalog_entities AS e WHERE e.id=?", Owner: "store/read", CallSites: []string{"store/read/rb_order_prepare.go:RBOrderPreparation.anchorTitleKey"}, Kind: stmt.Read, Params: []stmt.Param{i("entity")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "empty", Bindings: []any{int64(999999)}}}, Plan: point},
		stmt.Definition{ID: statementID("rb.order_prepare.point_octets"), SQL: "SELECT octet_length(e.sort_key),octet_length(e.sort_key_keep_articles),octet_length(e.title) FROM catalog_entities AS e WHERE e.id=?", Owner: "store/read", CallSites: []string{"store/read/rb_order_prepare.go:RBOrderPreparation.anchorTitleKey"}, Kind: stmt.Read, Params: []stmt.Param{i("entity")}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "empty", Bindings: []any{int64(999999)}}}, Plan: point},
	)
	return out
}
