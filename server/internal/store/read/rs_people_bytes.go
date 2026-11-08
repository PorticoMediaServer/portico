package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const rsPersonSQL = `SELECT id,token,name,sort_name,biography,birth_place,biography_source,birth_date,death_date,revision FROM catalog_people WHERE id=?`
const rsPersonOctetsSQL = `SELECT octet_length(token),octet_length(name),octet_length(sort_name),octet_length(biography),octet_length(birth_place),octet_length(biography_source),octet_length(birth_date),octet_length(death_date) FROM catalog_people WHERE id=?`
const rsCreditSQL = `SELECT e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,c.credited_name,r.label,d.label FROM catalog_credits AS c INDEXED BY sqlite_autoindex_catalog_credits_1
CROSS JOIN catalog_entities AS e ON e.id=c.entity_id
CROSS JOIN catalog_credit_images AS h ON h.id=c.image_id AND h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision
LEFT JOIN catalog_credit_labels AS r ON r.id=c.role_id
LEFT JOIN catalog_credit_labels AS d ON d.id=c.department_id
WHERE c.image_id=? AND c.ord=? AND c.person_id=? AND e.library_id=? AND e.kind=? AND e.retired=0 AND e.credit_image_id=c.image_id AND h.sealed=1`
const rsCreditOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(c.credited_name),octet_length(r.label),octet_length(d.label) FROM catalog_credits AS c INDEXED BY sqlite_autoindex_catalog_credits_1
CROSS JOIN catalog_entities AS e ON e.id=c.entity_id
CROSS JOIN catalog_credit_images AS h ON h.id=c.image_id AND h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision
LEFT JOIN catalog_credit_labels AS r ON r.id=c.role_id
LEFT JOIN catalog_credit_labels AS d ON d.id=c.department_id
WHERE c.image_id=? AND c.ord=? AND c.person_id=? AND e.library_id=? AND e.kind=? AND e.retired=0 AND e.credit_image_id=c.image_id AND h.sealed=1`

func init() {
	registerRSBytePoint("rs.people.person", rsBytePoint{lengths: "rs.people.person_octets", columns: []int{1, 2, 3, 4, 5, 6, 7, 8}})
	registerRSBytePoint("rs.people.credit", rsBytePoint{lengths: "rs.people.credit_octets", columns: []int{1, 2, 3, 7, 8, 9}})
}
func rsPeopleByteDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	var out []stmt.Definition
	add := func(name, sql string, params []stmt.Param, bindings []any, p stmt.Plan) {
		empty := append([]any(nil), bindings...)
		empty[0] = int64(999999)
		out = append(out, stmt.Definition{ID: readStatementID("rs.people." + name), SQL: sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_people.go:PersonReader"}, Params: params, Cohorts: []stmt.Cohort{{Name: "present", Bindings: bindings}, {Name: "empty", Bindings: empty}}, Plan: p})
	}
	for _, point := range []struct{ name, sql string }{{"person", rsPersonSQL}, {"person_octets", rsPersonOctetsSQL}} {
		add(point.name, point.sql, []stmt.Param{i("person")}, []any{int64(1)}, stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_people"}})
	}
	for _, point := range []struct{ name, sql string }{{"credit", rsCreditSQL}, {"credit_octets", rsCreditOctetsSQL}} {
		add(point.name, point.sql, []stmt.Param{i("image"), i("ordinal"), i("person"), i("library"), i("kind")}, []any{int64(2), int64(1), int64(1), int64(1), int64(1)}, stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_credits_1"}, PrimaryKeys: []string{"e", "h", "r", "d"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "catalog_credits", Name: "sqlite_autoindex_catalog_credits_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "image_id", Collation: "BINARY"}, {Name: "ord", Collation: "BINARY"}}}}})
	}
	return out
}
