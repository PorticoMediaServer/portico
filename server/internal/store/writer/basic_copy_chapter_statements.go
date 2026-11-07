package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCopyChapterScope   stmt.ID = "scan.basic_stage.copy_chapter_scope"
	basicCopyChapterStaged  stmt.ID = "scan.basic_stage.copy_chapter_staged"
	basicCopyChapterPresent stmt.ID = "scan.basic_stage.copy_chapter_present"
	basicCopyChapterInsert  stmt.ID = "scan.basic_stage.copy_chapter_insert"
	basicCopyChapterUpdate  stmt.ID = "scan.basic_stage.copy_chapter_update"
)

func basicCopyChapterDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	r := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Real} }
	defs := []stmt.Definition{
		{ID: basicCopyChapterScope, Kind: stmt.Read, SQL: `SELECT a.info_publication_pending,l.info_pending_change,a.file_size_revision,a.info_invalidation_revision,l.library_id,e.library_id,e.kind,e.retired,a.info_owner_object,a.info_owner_revision FROM catalog_assets AS a INNER JOIN catalog_asset_links AS l ON l.asset_id=a.id INNER JOIN catalog_entities AS e ON e.id=l.entity_id WHERE a.id=? AND l.entity_id=? AND l.asset_id=?`, Params: []stmt.Param{i("asset_id"), i("entity_id"), i("linked_asset_id")},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"a", "l", "e"}, Seeks: []string{"rowid=?", "entity_id=? AND asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "fenced_current_ordinary_owner", Bindings: []any{int64(41), int64(31), int64(41)}}}},
		{ID: basicCopyChapterStaged, Kind: stmt.Read, SQL: `SELECT input_ordinal,asset_id,chapter_index,title,start_seconds,end_seconds FROM catalog_info_new_chapters WHERE change_id=? AND input_ordinal=?`, Params: []stmt.Param{i("change_id"), i("input_ordinal")},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_chapters_2"}, Seeks: []string{"change_id=? AND input_ordinal=?"}}, Cohorts: []stmt.Cohort{{Name: "exact_sealed_chapter", Bindings: []any{int64(51), int64(0)}}}},
		{ID: basicCopyChapterPresent, Kind: stmt.Read, SQL: `SELECT 1 FROM asset_chapters WHERE asset_id=? AND chapter_index=?`, Params: []stmt.Param{t("asset_token"), i("chapter_index")},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapters_1"}, Seeks: []string{"asset_id=? AND chapter_index=?"}}, Cohorts: []stmt.Cohort{{Name: "canonical_chapter_key", Bindings: []any{"basic-asset", int64(1)}}}},
		{ID: basicCopyChapterInsert, Kind: stmt.Write, SQL: `INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds) VALUES(?,?,?,?,?) RETURNING asset_id,chapter_index,title,start_seconds,end_seconds`, Params: []stmt.Param{t("asset_token"), i("chapter_index"), t("title"), r("start_seconds"), r("end_seconds")},
			Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "first_canonical_chapter", Bindings: []any{"basic-asset", int64(1), "Opening", float64(0), float64(3)}}}},
		{ID: basicCopyChapterUpdate, Kind: stmt.Write, SQL: `UPDATE asset_chapters SET title=?,start_seconds=?,end_seconds=? WHERE asset_id=? AND chapter_index=? AND title=? AND start_seconds=? AND end_seconds=? RETURNING asset_id,chapter_index,title,start_seconds,end_seconds`, Params: []stmt.Param{t("next_title"), r("next_start_seconds"), r("next_end_seconds"), t("asset_token"), i("chapter_index"), t("title"), r("start_seconds"), r("end_seconds")},
			Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapters_1"}, Seeks: []string{"asset_id=? AND chapter_index=?"}}, Cohorts: []stmt.Cohort{{Name: "replace_exact_captured_chapter", Bindings: []any{"Opening", float64(0), float64(3), "basic-asset", int64(1), "Old opening", float64(0.25), float64(2.75)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_chapter.go"}
	}
	return defs
}
