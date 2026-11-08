package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCopy6Staged    stmt.ID = "scan.basic_copy.family6_staged"
	basicCopy6Canonical stmt.ID = "scan.basic_copy.family6_canonical"
	basicCopy6Children  stmt.ID = "scan.basic_copy.family6_children"
	basicCopy6Delete    stmt.ID = "scan.basic_copy.family6_delete"
	basicCopy6Insert    stmt.ID = "scan.basic_copy.family6_insert"
	basicCopy6Update    stmt.ID = "scan.basic_copy.family6_update"
)

var _ = registerBasicCopyFamily(6, basicCopyFamilySpec{copy: copyBasicFamily6, defs: basicCopyFamily6Definitions, maxUses: basicCopyFamily6MaxUses})

func basicCopyFamily6MaxUses(id stmt.ID) int {
	switch id {
	case basicCopy6Staged, basicCopy6Canonical, basicCopy6Children, basicCopy6Delete, basicCopy6Insert, basicCopy6Update:
		return 1
	}
	return 0
}

// Family 6 (asset_subtitle_facts): one row per asset, asset-scoped by token.
// Every statement is a PRIMARY KEY/index point access; nothing scans.
func basicCopyFamily6Definitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicCopy6Staged, Kind: stmt.Read,
			SQL:    `SELECT input_ordinal,revision,size,modified_ns,origin_us,timing_known,status,fingerprint FROM catalog_info_new_subtitle_facts WHERE change_id=? AND asset_id=?`,
			Params: []stmt.Param{i("change_id"), t("asset_token")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_new_subtitle_facts"}, Seeks: []string{"change_id=? AND asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "staged_asset_subtitle_fact", Bindings: []any{int64(51), "basic-asset"}}}},
		{ID: basicCopy6Canonical, Kind: stmt.Read,
			SQL:    `SELECT revision,size,modified_ns,origin_us,timing_known,status,fingerprint FROM asset_subtitle_facts WHERE asset_id=?`,
			Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitle_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_asset_subtitle_fact", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopy6Children, Kind: stmt.Read,
			SQL:    `SELECT id FROM asset_subtitles INDEXED BY asset_subtitles_asset WHERE asset_id=? ORDER BY id LIMIT 1`,
			Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_child_veto", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopy6Delete, Kind: stmt.Write,
			SQL:    `DELETE FROM asset_subtitle_facts WHERE asset_id=? AND revision=? RETURNING asset_id`,
			Params: []stmt.Param{t("asset_token"), i("revision")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitle_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "obsolete_canonical_subtitle_fact", Bindings: []any{"basic-asset", int64(1)}}}},
		{ID: basicCopy6Insert, Kind: stmt.Write,
			SQL:    `INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(?,?,?,?,?,?,?,?) RETURNING asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint`,
			Params: []stmt.Param{t("asset_token"), i("revision"), i("size"), i("modified_ns"), i("origin_us"), i("timing_known"), t("status"), t("fingerprint")}, Plan: stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "install_new_staged_subtitle_fact", Bindings: []any{"basic-asset", int64(31), int64(42), int64(-11), int64(0), int64(1), "known", "subtitles:31"}}}},
		{ID: basicCopy6Update, Kind: stmt.Write,
			SQL:     `UPDATE asset_subtitle_facts SET revision=?,size=?,modified_ns=?,origin_us=?,timing_known=?,status=?,fingerprint=? WHERE asset_id=? AND revision=? RETURNING asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint`,
			Params:  []stmt.Param{i("revision"), i("size"), i("modified_ns"), i("origin_us"), i("timing_known"), t("status"), t("fingerprint"), t("asset_token"), i("expected_revision")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitle_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "replace_canonical_subtitle_fact", Bindings: []any{int64(31), int64(42), int64(-11), int64(0), int64(1), "known", "subtitles:31", "basic-asset", int64(30)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_family6.go"}
	}
	return defs
}
