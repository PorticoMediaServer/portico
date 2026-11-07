package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// PersonCreditsDefinitions supplies inert SQL metadata. Harness composition
// also supplies browseDefinitions through HarnessDefinitions and actual Capture.
func PersonCreditsDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, bindings []any, p stmt.Plan) {
		empty := append([]any(nil), bindings...)
		empty[0] = int64(999999)
		out = append(out, stmt.Definition{ID: readStatementID("rs.people." + name), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_people.go:PersonReader"}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	add("source", `SELECT id,token,name,sort_name,biography,birth_place,biography_source,birth_date,death_date,revision,artwork_incarnation,artwork_revision FROM catalog_people WHERE id=?`, []stmt.Param{i("person")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_people"}})
	base := `SELECT c.entity_id,c.ord,c.image_id,e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,e.library_id,e.kind,e.credit_image_id,h.sealed,c.credited_name,r.label,d.label
FROM catalog_credits AS c INDEXED BY catalog_credits_person
LEFT JOIN catalog_entities AS e ON e.id=c.entity_id
LEFT JOIN catalog_credit_images AS h ON h.id=c.image_id AND h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision
LEFT JOIN catalog_credit_labels AS r ON r.id=c.role_id
LEFT JOIN catalog_credit_labels AS d ON d.id=c.department_id
WHERE c.person_id=?`
	planFor := func(seek string) stmt.Plan {
		return stmt.Plan{Hot: true, Indexes: []string{"catalog_credits_person"}, PrimaryKeys: []string{"e", "h", "r", "d"}, Seeks: []string{seek}}
	}
	add("first", base+" ORDER BY c.entity_id DESC,c.ord DESC,c.image_id DESC LIMIT ?", []stmt.Param{i("person"), i("limit")}, []any{int64(1), int64(64)}, planFor("person_id=?"))
	add("image_tail", base+" AND c.entity_id=? AND c.ord=? AND c.image_id<? ORDER BY c.image_id DESC LIMIT ?", []stmt.Param{i("person"), i("work"), i("ordinal"), i("image"), i("limit")}, []any{int64(1), int64(2), int64(1), int64(10003), int64(64)}, planFor("person_id=? AND entity_id=? AND ord=? AND image_id<?"))
	add("ordinal_tail", base+" AND c.entity_id=? AND c.ord<? ORDER BY c.ord DESC,c.image_id DESC LIMIT ?", []stmt.Param{i("person"), i("work"), i("ordinal"), i("limit")}, []any{int64(1), int64(2), int64(2), int64(64)}, planFor("person_id=? AND entity_id=? AND ord<?"))
	add("next", base+" AND c.entity_id<? ORDER BY c.entity_id DESC,c.ord DESC,c.image_id DESC LIMIT ?", []stmt.Param{i("person"), i("work"), i("limit")}, []any{int64(1), int64(2), int64(64)}, planFor("person_id=? AND entity_id<?"))
	return out
}
