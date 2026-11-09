package read

import (
	"github.com/PorticoMediaServer/portico/server/internal/store/internal/bookseries"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// The accepted local book-series projection is read as one exact source head
// plus its complete ordered member cohort. The head point is the book PK; the
// member range is that book's PK prefix. Group C is not embedded yet, so these
// are inert harness definitions with EXPLAIN-only evidence, never a recorded
// golden.
const iaBookSeriesEntitySQL = `SELECT id,sort_key,public_id,title,retired,revision,admission_own_age,kind,metadata_incarnation FROM catalog_entities WHERE id=? AND library_id=?`
const iaBookSeriesHeadSQL = `SELECT library_id,book_kind,source_asset_id,source_info_epoch,source_revision,local_policy_revision,revision,member_count,member_bytes,member_digest FROM catalog_book_series_heads WHERE book_id=? AND library_id=?`
const iaBookSeriesMembersSQL = `SELECT ordinal FROM catalog_book_series_members WHERE book_id=? ORDER BY ordinal LIMIT ?`
const iaBookSeriesMemberSQL = `SELECT ordinal,series_name,series_position FROM catalog_book_series_members WHERE book_id=? AND ordinal=?`

// maxIABookSeriesMembers bounds one complete read cohort. The writer publishes
// at most bookseries.MaxPairs; the read refuses a larger head as a torn
// projection rather than truncating or exceeding its raw-row window.
const maxIABookSeriesMembers = bookseries.MaxPairs

// IABookSeriesHarnessDefinitions is inert harness composition, not registration
// or authorization. Capture these exact definitions on the actual reader first;
// group C must be embedded before a golden is recorded.
func IABookSeriesHarnessDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	entityPoint := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_entities"}, Seeks: []string{"rowid=?"}}
	headPoint := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_book_series_heads"}, Seeks: []string{"rowid=?"}}
	memberRange := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_book_series_members"}, Seeks: []string{"book_id=?"}}
	memberPoint := stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_book_series_members"}, Seeks: []string{"book_id=? AND ordinal=?"}}
	defs := []stmt.Definition{
		{ID: readStatementID("foreground.ia.book_series.entity"), SQL: iaBookSeriesEntitySQL, Kind: stmt.Read,
			Owner: "store/read/ia_book_series", CallSites: []string{"store/read/ia_book_series.go:IABookSeries"},
			Params: []stmt.Param{i("entity"), i("library")},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(1), int64(1)}},
				{Name: "empty", Bindings: []any{int64(9999999), int64(1)}},
			}, Plan: entityPoint},
		{ID: readStatementID("foreground.ia.book_series.head"), SQL: iaBookSeriesHeadSQL, Kind: stmt.Read,
			Owner: "store/read/ia_book_series", CallSites: []string{"store/read/ia_book_series.go:IABookSeries"},
			Params: []stmt.Param{i("book"), i("library")},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(31), int64(1)}},
				{Name: "empty", Bindings: []any{int64(9999999), int64(1)}},
			}, Plan: headPoint},
		{ID: readStatementID("foreground.ia.book_series.members"), SQL: iaBookSeriesMembersSQL, Kind: stmt.Read,
			Owner: "store/read/ia_book_series", CallSites: []string{"store/read/ia_book_series.go:IABookSeries"},
			Params: []stmt.Param{i("book"), i("limit")},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(31), int64(17)}},
				{Name: "empty", Bindings: []any{int64(9999999), int64(17)}},
			}, Plan: memberRange},
		{ID: readStatementID("foreground.ia.book_series.member"), SQL: iaBookSeriesMemberSQL, Kind: stmt.Read,
			Owner: "store/read/ia_book_series", CallSites: []string{"store/read/ia_book_series.go:IABookSeries"},
			Params: []stmt.Param{i("book"), i("ordinal")},
			Cohorts: []stmt.Cohort{
				{Name: "present", Bindings: []any{int64(31), int64(0)}},
				{Name: "empty", Bindings: []any{int64(9999999), int64(0)}},
			}, Plan: memberPoint},
	}
	return append(defs, iaBookSeriesOctetDefinitions()...)
}
