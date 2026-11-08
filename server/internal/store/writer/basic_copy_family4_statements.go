package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCopy4Staged    stmt.ID = "scan.basic_copy.family4_staged"
	basicCopy4Canonical stmt.ID = "scan.basic_copy.family4_canonical"
	basicCopy4Child     stmt.ID = "scan.basic_copy.family4_child"
	basicCopy4Delete    stmt.ID = "scan.basic_copy.family4_delete"
	basicCopy4Insert    stmt.ID = "scan.basic_copy.family4_insert"
	basicCopy4Update    stmt.ID = "scan.basic_copy.family4_update"
)

var _ = registerBasicCopyFamily(4, basicCopyFamilySpec{copy: copyBasicFamily4, defs: basicCopyFamily4Definitions, maxUses: basicCopyFamily4MaxUses})

func basicCopyFamily4MaxUses(id stmt.ID) int {
	switch id {
	case basicCopy4Staged, basicCopy4Canonical, basicCopy4Child, basicCopy4Delete, basicCopy4Insert, basicCopy4Update:
		return 1
	}
	return 0
}

// Family 4 (asset_chapter_facts): one row per asset, asset-scoped by token.
// Every statement is a primary key point; the child veto is the chapter key.
func basicCopyFamily4Definitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicCopy4Staged, Kind: stmt.Read,
			SQL:    `SELECT input_ordinal,revision,size,modified_ns,status,fingerprint FROM catalog_info_new_chapter_facts WHERE change_id=? AND asset_id=?`,
			Params: []stmt.Param{i("change_id"), t("asset_token")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_new_chapter_facts"}, Seeks: []string{"change_id=? AND asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "staged_chapter_fact", Bindings: []any{int64(51), "basic-asset"}}}},
		{ID: basicCopy4Canonical, Kind: stmt.Read,
			SQL:    `SELECT revision,size,modified_ns,status,fingerprint FROM asset_chapter_facts WHERE asset_id=?`,
			Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapter_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_chapter_fact", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopy4Child, Kind: stmt.Read,
			SQL:    `SELECT chapter_index FROM asset_chapters WHERE asset_id=? ORDER BY chapter_index LIMIT 1`,
			Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapters_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_chapter_veto", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopy4Delete, Kind: stmt.Write,
			SQL:    `DELETE FROM asset_chapter_facts WHERE asset_id=? AND revision=? RETURNING asset_id`,
			Params: []stmt.Param{t("asset_token"), i("revision")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapter_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "obsolete_chapter_fact", Bindings: []any{"basic-asset", int64(1)}}}},
		{ID: basicCopy4Insert, Kind: stmt.Write,
			SQL:    `INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) VALUES(?,?,?,?,?,?) RETURNING asset_id,revision,size,modified_ns,status,fingerprint`,
			Params: []stmt.Param{t("asset_token"), i("revision"), i("size"), i("modified_ns"), t("status"), t("fingerprint")}, Plan: stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "install_staged_chapter_fact", Bindings: []any{"basic-asset", int64(37), int64(42), int64(-11), "known", "chapters:37"}}}},
		{ID: basicCopy4Update, Kind: stmt.Write,
			SQL:     `UPDATE asset_chapter_facts SET revision=?,size=?,modified_ns=?,status=?,fingerprint=? WHERE asset_id=? AND revision=? RETURNING asset_id,revision,size,modified_ns,status,fingerprint`,
			Params:  []stmt.Param{i("revision"), i("size"), i("modified_ns"), t("status"), t("fingerprint"), t("asset_token"), i("expected_revision")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapter_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "replace_canonical_chapter_fact", Bindings: []any{int64(37), int64(42), int64(-11), "known", "chapters:37", "basic-asset", int64(31)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_family4.go"}
	}
	return defs
}
