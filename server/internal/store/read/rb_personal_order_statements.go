package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// Compose these inert definitions with HarnessDefinitions. There is no durable
// profile catalogue or copied presentation order, just current personal facts.
func RBPersonalOrderDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	return []stmt.Definition{{
		ID: readStatementID("rb.personal.order.source"), Owner: "store/read", Kind: stmt.Read,
		CallSites: []string{"store/read/rb_personal_order.go:BrowseReader.PersonalOrderExact"},
		SQL: `SELECT e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,p.rating,p.play_count,p.last_played_at
FROM catalog_entities AS e INDEXED BY catalog_entities_library
LEFT JOIN personal_items AS p INDEXED BY personal_current_live ON p.profile_id=? AND p.item_id=e.id AND p.item_public_id=e.public_id
WHERE e.library_id=? AND e.kind=? ORDER BY e.id LIMIT ?`,
		Params:  []stmt.Param{{Name: "profile", Type: stmt.Text}, i("library"), i("kind"), i("limit")},
		Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{"p", int64(1), int64(1), int64(257)}}, {Name: "empty", Bindings: []any{"p", int64(999999), int64(1), int64(257)}}},
		Plan:    stmt.Plan{Hot: true, Indexes: []string{"catalog_entities_library", "personal_current_live"}, Seeks: []string{"library_id=? AND kind=?", "profile_id=? AND item_id=?"}},
	}}
}
