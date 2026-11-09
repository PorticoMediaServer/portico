package read

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// The accepted local book-series byte preflights. Each pair is an exact point
// with identical bindings in one immutable snapshot; no length is inferred from
// characters or column maxima. Group C is not embedded yet, so these carry
// EXPLAIN-only evidence and no recorded golden.
const iaBookSeriesEntityOctetsSQL = `SELECT octet_length(sort_key),octet_length(public_id),octet_length(title),octet_length(metadata_incarnation) FROM catalog_entities WHERE id=? AND library_id=?`
const iaBookSeriesHeadOctetsSQL = `SELECT octet_length(source_revision),octet_length(member_digest) FROM catalog_book_series_heads WHERE book_id=? AND library_id=?`
const iaBookSeriesMemberOctetsSQL = `SELECT octet_length(series_name),octet_length(series_position) FROM catalog_book_series_members WHERE book_id=? AND ordinal=?`

func iaBookSeriesOctetDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	entityPoint := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}}
	headPoint := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_book_series_heads"}, Seeks: []string{"rowid=?"}}
	memberPoint := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_book_series_members"}, Seeks: []string{"book_id=? AND ordinal=?"}}
	return []stmt.Definition{
		{ID: readStatementID("foreground.ia.book_series.entity_octets"), SQL: iaBookSeriesEntityOctetsSQL, Kind: stmt.Read,
			Owner: "store/read/ia_book_series", CallSites: []string{"store/read/ia_book_series.go:IABookSeries"},
			Params: []stmt.Param{i("entity"), i("library")},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(1), int64(1)}},
				{Name: "empty", Bindings: []any{int64(9999999), int64(1)}},
			}, Plan: entityPoint},
		{ID: readStatementID("foreground.ia.book_series.head_octets"), SQL: iaBookSeriesHeadOctetsSQL, Kind: stmt.Read,
			Owner: "store/read/ia_book_series", CallSites: []string{"store/read/ia_book_series.go:IABookSeries"},
			Params: []stmt.Param{i("book"), i("library")},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(31), int64(1)}},
				{Name: "empty", Bindings: []any{int64(9999999), int64(1)}},
			}, Plan: headPoint},
		{ID: readStatementID("foreground.ia.book_series.member_octets"), SQL: iaBookSeriesMemberOctetsSQL, Kind: stmt.Read,
			Owner: "store/read/ia_book_series", CallSites: []string{"store/read/ia_book_series.go:IABookSeries"},
			Params: []stmt.Param{i("book"), i("ordinal")},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(31), int64(0)}},
				{Name: "empty", Bindings: []any{int64(9999999), int64(0)}},
			}, Plan: memberPoint},
	}
}
