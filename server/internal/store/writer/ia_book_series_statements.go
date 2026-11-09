package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

// IA-14 statement family: the exact book-series head point, its finite
// insert/CAS and the complete ordered member replacement on the group C C2
// relations. There is no OFFSET, temporary sort, library-wide scan or
// caller-selected SQL. The head is one row per book; members are read and
// replaced by that book's PK prefix only.
const (
	iaBookSeriesHead          stmt.ID = "ia.book_series.head"
	iaBookSeriesHeadInsert    stmt.ID = "ia.book_series.head_insert"
	iaBookSeriesHeadCAS       stmt.ID = "ia.book_series.head_cas"
	iaBookSeriesMembersDelete stmt.ID = "ia.book_series.members_delete"
	iaBookSeriesMemberInsert  stmt.ID = "ia.book_series.member_insert"
)

func iaBookSeriesStatements() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	digest := make([]byte, 32)
	defs := []stmt.Definition{
		{ID: iaBookSeriesHead, Kind: stmt.Read,
			SQL: `SELECT library_id,source_asset_id,source_info_epoch,source_revision,local_policy_revision,revision,member_count,member_bytes,member_digest
FROM catalog_book_series_heads WHERE book_id=?`,
			Params: []stmt.Param{i("book_id")},
			Cohorts: []stmt.Cohort{
				{Name: "published_head", Bindings: []any{int64(31)}},
				{Name: "missing_head", Bindings: []any{int64(32)}},
			}},
		{ID: iaBookSeriesHeadInsert, Kind: stmt.Write,
			SQL: `INSERT INTO catalog_book_series_heads(
book_id,library_id,source_asset_id,source_info_epoch,source_revision,local_policy_revision,revision,member_count,member_bytes,member_digest)
VALUES(?,?,?,?,?,?,?,?,?,?) RETURNING revision`,
			Params: []stmt.Param{i("book_id"), i("library_id"), i("source_asset_id"), i("source_info_epoch"), t("source_revision"),
				i("local_policy_revision"), i("revision"), i("member_count"), i("member_bytes"), b("member_digest")},
			Cohorts: []stmt.Cohort{
				{Name: "first_published_head", Bindings: []any{int64(31), int64(7), int64(41), int64(1), "book-token", int64(4), int64(1), int64(1), int64(32), digest}},
			}},
		{ID: iaBookSeriesHeadCAS, Kind: stmt.Write,
			SQL: `UPDATE catalog_book_series_heads
SET source_asset_id=?,source_info_epoch=?,source_revision=?,local_policy_revision=?,revision=?,member_count=?,member_bytes=?,member_digest=?
WHERE book_id=? AND library_id=? AND source_asset_id=? AND source_info_epoch=? AND source_revision=? AND local_policy_revision=?
AND revision=? AND member_count=? AND member_bytes=? AND member_digest=?
RETURNING revision`,
			Params: []stmt.Param{i("source_asset_id"), i("source_info_epoch"), t("source_revision"), i("local_policy_revision"),
				i("revision"), i("member_count"), i("member_bytes"), b("member_digest"),
				i("book_id"), i("library_id"), i("expected_asset_id"), i("expected_info_epoch"), t("expected_source_revision"),
				i("expected_policy_revision"), i("expected_revision"), i("expected_member_count"), i("expected_member_bytes"), b("expected_member_digest")},
			Cohorts: []stmt.Cohort{
				{Name: "replaced_head", Bindings: []any{int64(41), int64(2), "book-token", int64(4), int64(2), int64(2), int64(64), digest,
					int64(31), int64(7), int64(41), int64(1), "book-token", int64(4), int64(1), int64(1), int64(32), digest}},
				{Name: "stale_head_cas", Bindings: []any{int64(41), int64(2), "book-token", int64(4), int64(2), int64(2), int64(64), digest,
					int64(31), int64(7), int64(41), int64(1), "book-token", int64(4), int64(2), int64(1), int64(32), digest}},
			}},
		{ID: iaBookSeriesMembersDelete, Kind: stmt.Write,
			SQL:    `DELETE FROM catalog_book_series_members WHERE book_id=?`,
			Params: []stmt.Param{i("book_id")},
			Cohorts: []stmt.Cohort{
				{Name: "published_members", Bindings: []any{int64(31)}},
			}},
		{ID: iaBookSeriesMemberInsert, Kind: stmt.Write,
			SQL:    `INSERT INTO catalog_book_series_members(book_id,ordinal,series_name,series_position) VALUES(?,?,?,?) RETURNING ordinal`,
			Params: []stmt.Param{i("book_id"), i("ordinal"), t("series_name"), t("series_position")},
			Cohorts: []stmt.Cohort{
				{Name: "first_member", Bindings: []any{int64(31), int64(0), "Saga", "1"}},
				{Name: "empty_position_member", Bindings: []any{int64(31), int64(1), "Émile", ""}},
			}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer IA book series accept"
		defs[n].CallSites = []string{"store/writer/ia_book_series_accept.go"}
		defs[n].Plan = stmt.Plan{Hot: true}
		switch defs[n].ID {
		case iaBookSeriesHead:
			defs[n].Plan.PrimaryKeys = []string{"catalog_book_series_heads"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaBookSeriesHeadCAS:
			defs[n].Plan.PrimaryKeys = []string{"catalog_book_series_heads"}
			defs[n].Plan.Seeks = []string{"rowid=?"}
		case iaBookSeriesMembersDelete:
			defs[n].Plan.PrimaryKeys = []string{"catalog_book_series_members"}
			defs[n].Plan.Seeks = []string{"book_id=?"}
		}
	}
	return defs
}
