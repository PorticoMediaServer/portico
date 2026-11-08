package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const rsReadingSourceSQL = `SELECT e.id,e.public_id,e.library_id,e.kind,e.retired,e.revision,e.admission_own_age,e.has_reading_attribution_members,e.metadata_incarnation,e.source_revision,e.identity_revision,e.metadata_revision,e.scalar_source_revision,e.aggregate_revision,e.admission_revision FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=?`
const rsReadingFactsSQL = `SELECT e.id,e.public_id,e.title,e.year,e.added_at,e.reading_publication_type,e.reading_publication_type_source,e.reading_issue_date,e.reading_issue_date_source,e.reading_author_value,e.reading_author_key,e.reading_author_source,e.reading_series_value,e.reading_series_key,e.reading_series_id,e.reading_series_source,e.reading_numbered_order,e.reading_number_value FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0 AND e.has_reading_attribution_members=1`
const rsReadingOctetsSQL = `SELECT octet_length(e.public_id),octet_length(e.title),octet_length(e.added_at),octet_length(e.reading_publication_type),octet_length(e.reading_publication_type_source),octet_length(e.reading_issue_date),octet_length(e.reading_issue_date_source),octet_length(e.reading_author_value),octet_length(e.reading_author_key),octet_length(e.reading_author_source),octet_length(e.reading_series_value),octet_length(e.reading_series_key),octet_length(e.reading_series_id),octet_length(e.reading_series_source) FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0 AND e.has_reading_attribution_members=1`
const rsReadingPublicationsSQL = `SELECT asset_id,format FROM reading_publications WHERE item_id=? ORDER BY asset_id LIMIT ?`
const rsReadingLocalSQL = `SELECT 1 FROM inventory_objects AS o INDEXED BY inventory_objects_reading_asset_object CROSS JOIN library_sources AS s INDEXED BY library_sources_library ON s.id=o.source_id WHERE o.asset_id=? AND s.library_id=? AND o.retired=0 AND o.root_incarnation=s.incarnation AND o.size=? AND o.modified_ns=? AND s.kind='local' LIMIT 1`

func init() {
	registerRSBytePoint("rs.reading.facts", rsBytePoint{lengths: "rs.reading.octets", columns: []int{1, 2, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}})
}
func ReadingFactsDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	params := []stmt.Param{i("item"), i("library"), i("kind")}
	cohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(1101), int64(1), int64(20)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1), int64(20)}}}
	var out []stmt.Definition
	for _, p := range []struct {
		name, sql string
		plan      stmt.Plan
	}{{"source", rsReadingSourceSQL, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}}, {"facts", rsReadingFactsSQL, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}}, {"octets", rsReadingOctetsSQL, stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}}} {
		out = append(out, stmt.Definition{ID: readStatementID("rs.reading." + p.name), SQL: p.sql, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_reading.go:RSReadingReader.Facts"}, Params: params, Cohorts: cohorts, Plan: p.plan})
	}
	out = append(out, rsReadingInventoryDefinitions()...)
	return out
}
