package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const rsPageCreditSQL = `SELECT e.id,e.sort_key,e.public_id,e.title,e.retired,e.revision,e.admission_own_age,c.credited_name,r.label,d.label,e.added_at,e.kind FROM catalog_credits AS c INDEXED BY sqlite_autoindex_catalog_credits_1
CROSS JOIN catalog_entities AS e ON e.id=c.entity_id
CROSS JOIN catalog_credit_images AS h ON h.id=c.image_id AND h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision
LEFT JOIN catalog_credit_labels AS r ON r.id=c.role_id
LEFT JOIN catalog_credit_labels AS d ON d.id=c.department_id
WHERE c.image_id=? AND c.ord=? AND c.person_id=? AND e.library_id=? AND e.kind=? AND e.retired=0 AND e.credit_image_id=c.image_id AND h.sealed=1`

const rsPageCreditOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(c.credited_name),octet_length(r.label),octet_length(d.label),octet_length(e.added_at) FROM catalog_credits AS c INDEXED BY sqlite_autoindex_catalog_credits_1
CROSS JOIN catalog_entities AS e ON e.id=c.entity_id
CROSS JOIN catalog_credit_images AS h ON h.id=c.image_id AND h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision
LEFT JOIN catalog_credit_labels AS r ON r.id=c.role_id
LEFT JOIN catalog_credit_labels AS d ON d.id=c.department_id
WHERE c.image_id=? AND c.ord=? AND c.person_id=? AND e.library_id=? AND e.kind=? AND e.retired=0 AND e.credit_image_id=c.image_id AND h.sealed=1`

const rsPagePortraitSQL = `SELECT s.small_blob,s.large_blob,s.ambient_blob,s.accepted_mime,s.accepted_width,s.accepted_height,a.revision FROM artwork_selections AS a LEFT JOIN artwork_sources AS s INDEXED BY sqlite_autoindex_artwork_sources_1 ON s.digest=a.digest WHERE a.kind='person' AND a.entity_id=? AND a.role='portrait' AND a.choice=?`
const rsPagePortraitOctetsSQL = `SELECT octet_length(s.small_blob),octet_length(s.large_blob),octet_length(s.ambient_blob),octet_length(s.accepted_mime) FROM artwork_selections AS a LEFT JOIN artwork_sources AS s INDEXED BY sqlite_autoindex_artwork_sources_1 ON s.digest=a.digest WHERE a.kind='person' AND a.entity_id=? AND a.role='portrait' AND a.choice=?`

func init() {
	registerRSBytePoint("rs.people.page.credit", rsBytePoint{lengths: "rs.people.page.credit_octets", columns: []int{1, 2, 3, 7, 8, 9, 10}})
	registerRSBytePoint("rs.people.page.portrait", rsBytePoint{lengths: "rs.people.page.portrait_octets", columns: []int{0, 1, 2, 3}})
}

// RSPeoplePageDefinitions is inert. Composition must Capture on its actual
// reader and include BrowseDefinitions and PersonCreditsDefinitions as well.
func RSPeoplePageDefinitions() []stmt.Definition {
	base := rsPeopleByteDefinitions()
	var out []stmt.Definition
	for _, pair := range []struct{ name, sql string }{{"credit", rsPageCreditSQL}, {"credit_octets", rsPageCreditOctetsSQL}} {
		d := base[2]
		d.ID, d.SQL = readStatementID("rs.people.page."+pair.name), pair.sql
		d.CallSites = []string{"store/read/rs_people_page.go:RSPeoplePageReader"}
		out = append(out, d)
	}
	out = append(out, stmt.Definition{ID: readStatementID("rs.people.page.work_source"), SQL: `SELECT e.id,e.revision,e.metadata_incarnation,e.credit_owner_revision,e.credit_image_id,e.retired,e.admission_own_age,h.sealed FROM catalog_entities AS e LEFT JOIN catalog_credit_images AS h ON h.id=e.credit_image_id AND h.entity_id=e.id AND h.entity_incarnation=e.metadata_incarnation AND h.owner_revision=e.credit_owner_revision WHERE e.id=? AND e.library_id=? AND e.kind=?`, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_people_page.go:RSPeoplePageReader"}, Params: []stmt.Param{{Name: "work", Type: stmt.Integer}, {Name: "library", Type: stmt.Integer}, {Name: "kind", Type: stmt.Integer}}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1), int64(1)}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e", "h"}}})
	for _, pair := range []struct{ name, sql string }{{"portrait", rsPagePortraitSQL}, {"portrait_octets", rsPagePortraitOctetsSQL}} {
		out = append(out, stmt.Definition{ID: readStatementID("rs.people.page." + pair.name), SQL: pair.sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_people_page.go:RSPeoplePageReader"}, Params: []stmt.Param{{Name: "person", Type: stmt.Integer}, {Name: "choice", Type: stmt.Text}}, Cohorts: []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), "matched"}}, {Name: "empty", Bindings: []any{int64(999999), "matched"}}}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"a"}, Indexes: []string{"sqlite_autoindex_artwork_sources_1"}, AutoIndexes: []stmt.AutoIndexBinding{{Table: "artwork_sources", Name: "sqlite_autoindex_artwork_sources_1", Origin: "pk", Columns: []stmt.IndexColumn{{Name: "digest", Collation: "BINARY"}}}}}})
	}
	return out
}
