package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCopy5Staged    stmt.ID = "scan.basic_copy.family5_staged"
	basicCopy5Parent    stmt.ID = "scan.basic_copy.family5_parent"
	basicCopy5Canonical stmt.ID = "scan.basic_copy.family5_canonical"
	basicCopy5Insert    stmt.ID = "scan.basic_copy.family5_insert"
	basicCopy5Update    stmt.ID = "scan.basic_copy.family5_update"
	basicCopy5WalkFirst stmt.ID = "scan.basic_copy.family5_walk_first"
	basicCopy5WalkAfter stmt.ID = "scan.basic_copy.family5_walk_after"
	basicCopy5Retained  stmt.ID = "scan.basic_copy.family5_retained"
	basicCopy5Delete    stmt.ID = "scan.basic_copy.family5_delete"
)

var _ = registerBasicCopyFamily(5, basicCopyFamilySpec{copy: copyBasicFamily5, defs: basicCopyFamily5Definitions, maxUses: basicCopyFamily5MaxUses})

func basicCopyFamily5MaxUses(id stmt.ID) int {
	switch id {
	case basicCopy5Staged, basicCopy5Parent, basicCopy5WalkFirst, basicCopy5WalkAfter:
		return 1
	case basicCopy5Canonical, basicCopy5Insert, basicCopy5Update, basicCopy5Retained, basicCopy5Delete:
		return basicCopy5Install
	}
	return 0
}

// Family 5 (asset_chapters): the chapter PK keyset, its per-ordinal staged rows
// and the canonical key walk. Every access is an index seek, never a scan.
func basicCopyFamily5Definitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	r := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Real} }
	defs := []stmt.Definition{
		{ID: basicCopy5Staged, Kind: stmt.Read,
			SQL:    `SELECT input_ordinal,asset_id,chapter_index,title,start_seconds,end_seconds FROM catalog_info_new_chapters WHERE change_id=? AND asset_id=? AND input_ordinal>=? ORDER BY input_ordinal LIMIT 10`,
			Params: []stmt.Param{i("change_id"), t("asset_token"), i("after_ordinal")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_chapters_2"}, Seeks: []string{"change_id=? AND input_ordinal>?"}},
			Cohorts: []stmt.Cohort{{Name: "staged_chapter_keyset", Bindings: []any{int64(51), "basic-asset", int64(0)}}}},
		{ID: basicCopy5Parent, Kind: stmt.Read,
			SQL:    `SELECT asset_id FROM asset_chapter_facts WHERE asset_id=?`,
			Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapter_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_chapter_parent", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopy5Canonical, Kind: stmt.Read,
			SQL:    `SELECT chapter_index,title,start_seconds,end_seconds FROM asset_chapters WHERE asset_id=? AND chapter_index=?`,
			Params: []stmt.Param{t("asset_token"), i("chapter_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapters_1"}, Seeks: []string{"asset_id=? AND chapter_index=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_chapter", Bindings: []any{"basic-asset", int64(0)}}}},
		{ID: basicCopy5Insert, Kind: stmt.Write,
			SQL:    `INSERT INTO asset_chapters(asset_id,chapter_index,title,start_seconds,end_seconds) VALUES(?,?,?,?,?) RETURNING asset_id,chapter_index,title,start_seconds,end_seconds`,
			Params: []stmt.Param{t("asset_token"), i("chapter_index"), t("title"), r("start_seconds"), r("end_seconds")}, Plan: stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "install_staged_chapter", Bindings: []any{"basic-asset", int64(0), "Opening", float64(0), float64(3)}}}},
		{ID: basicCopy5Update, Kind: stmt.Write,
			SQL:     `UPDATE asset_chapters SET title=?,start_seconds=?,end_seconds=? WHERE asset_id=? AND chapter_index=? AND title=? AND start_seconds=? AND end_seconds=? RETURNING asset_id,chapter_index,title,start_seconds,end_seconds`,
			Params:  []stmt.Param{t("title"), r("start_seconds"), r("end_seconds"), t("asset_token"), i("chapter_index"), t("expected_title"), r("expected_start_seconds"), r("expected_end_seconds")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapters_1"}, Seeks: []string{"asset_id=? AND chapter_index=?"}},
			Cohorts: []stmt.Cohort{{Name: "replace_canonical_chapter", Bindings: []any{"Opening", float64(0), float64(3), "basic-asset", int64(0), "Opening", float64(0), float64(2)}}}},
		{ID: basicCopy5WalkFirst, Kind: stmt.Read,
			SQL:    `SELECT chapter_index FROM asset_chapters WHERE asset_id=? ORDER BY chapter_index LIMIT 9`,
			Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapters_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_chapter_walk_first", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopy5WalkAfter, Kind: stmt.Read,
			SQL:    `SELECT chapter_index FROM asset_chapters WHERE asset_id=? AND chapter_index>? ORDER BY chapter_index LIMIT 9`,
			Params: []stmt.Param{t("asset_token"), i("after_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapters_1"}, Seeks: []string{"asset_id=? AND chapter_index>?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_chapter_walk_after", Bindings: []any{"basic-asset", int64(0)}}}},
		{ID: basicCopy5Retained, Kind: stmt.Read,
			SQL:    `SELECT change_id,asset_id,chapter_index FROM catalog_info_new_chapters WHERE change_id=? AND asset_id=? AND chapter_index=?`,
			Params: []stmt.Param{i("change_id"), t("asset_token"), i("chapter_index")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_new_chapters"}, Seeks: []string{"change_id=? AND asset_id=? AND chapter_index=?"}},
			Cohorts: []stmt.Cohort{{Name: "staged_chapter_key", Bindings: []any{int64(51), "basic-asset", int64(0)}}}},
		{ID: basicCopy5Delete, Kind: stmt.Write,
			SQL:    `DELETE FROM asset_chapters WHERE asset_id=? AND chapter_index=? RETURNING asset_id,chapter_index`,
			Params: []stmt.Param{t("asset_token"), i("chapter_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapters_1"}, Seeks: []string{"asset_id=? AND chapter_index=?"}},
			Cohorts: []stmt.Cohort{{Name: "obsolete_canonical_chapter", Bindings: []any{"basic-asset", int64(0)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_family5.go"}
	}
	return defs
}
