package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const rsDetailFactsOctetsSQL = `SELECT octet_length(e.sort_key),octet_length(e.public_id),octet_length(e.title),octet_length(e.overview),octet_length(e.original_title),octet_length(e.tagline),octet_length(e.edition),octet_length(e.release_date),octet_length(e.content_rating),octet_length(e.studio),octet_length(e.network),octet_length(e.country),octet_length(e.added_at),octet_length(e.last_content_added_at),octet_length(e.show_ranges),octet_length(e.numbering),octet_length(e.local_identity_status),octet_length(e.ordering_basis),octet_length(e.record_label),octet_length(e.release_country),octet_length(e.edition_key),octet_length(e.author),octet_length(e.narrator),octet_length(e.series),octet_length(e.series_position),octet_length(e.collection_name_key) FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`

func init() {
	registerRSBytePoint("rs.detail.facts", rsBytePoint{lengths: "rs.detail.facts_octets", columns: []int{1, 2, 3, 7, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 23, 28, 30, 31, 33, 34, 35, 39, 40, 41, 42, 45}})
}
func rsDetailByteDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	params := []stmt.Param{i("item"), i("library"), i("kind")}
	cohorts := []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1), int64(1)}}, {Name: "empty", Bindings: []any{int64(999999), int64(1), int64(1)}}}
	return []stmt.Definition{
		{ID: readStatementID("rs.detail.source"), Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_detail.go:ForegroundReader"}, SQL: `SELECT e.id,e.revision,e.retired,e.admission_own_age,e.metadata_incarnation,e.source_revision,e.identity_revision,e.metadata_revision,e.scalar_source_revision,e.aggregate_revision,e.admission_revision FROM catalog_entities AS e WHERE e.id=? AND e.library_id=? AND e.kind=? AND e.retired=0`, Params: params, Cohorts: cohorts, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}},
		{ID: readStatementID("rs.detail.facts_octets"), Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_detail.go:ForegroundReader"}, SQL: rsDetailFactsOctetsSQL, Params: params, Cohorts: cohorts, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"e"}}},
	}
}
