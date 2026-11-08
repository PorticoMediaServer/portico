package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCopy7StagedFirst stmt.ID = "scan.basic_copy.family7_staged_first"
	basicCopy7StagedAfter stmt.ID = "scan.basic_copy.family7_staged_after"
	basicCopy7Peek        stmt.ID = "scan.basic_copy.family7_peek"
	basicCopy7Canonical   stmt.ID = "scan.basic_copy.family7_canonical"
	basicCopy7Insert      stmt.ID = "scan.basic_copy.family7_insert"
	basicCopy7Update      stmt.ID = "scan.basic_copy.family7_update"
)

var _ = registerBasicCopyFamily(7, basicCopyFamilySpec{copy: copyBasicFamily7, defs: basicCopyFamily7Definitions, maxUses: basicCopyFamily7MaxUses})

func basicCopyFamily7MaxUses(id stmt.ID) int {
	switch id {
	case basicCopy7StagedAfter, basicCopy7Canonical, basicCopy7Insert, basicCopy7Update:
		return 8
	case basicCopy7StagedFirst, basicCopy7Peek:
		return 1
	}
	return 0
}

// Family 7 (subtitle_sidecars): shared by path across assets, so this family is
// upsert only, never delete. The staged rows are read in input_ordinal order by
// the staged table's UNIQUE(change_id,input_ordinal) keyset.
func basicCopyFamily7Definitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicCopy7StagedFirst, Kind: stmt.Read,
			SQL:    `SELECT input_ordinal,id,path,size,modified_ns,digest FROM catalog_info_new_subtitle_sidecars WHERE change_id=? AND input_ordinal>=? ORDER BY input_ordinal LIMIT 1`,
			Params: []stmt.Param{i("change_id"), i("from_ordinal")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitle_sidecars_3"}, Seeks: []string{"change_id=? AND input_ordinal>?"}},
			Cohorts: []stmt.Cohort{{Name: "first_staged_sidecar", Bindings: []any{int64(51), int64(0)}}}},
		{ID: basicCopy7StagedAfter, Kind: stmt.Read,
			SQL:    `SELECT input_ordinal,id,path,size,modified_ns,digest FROM catalog_info_new_subtitle_sidecars WHERE change_id=? AND input_ordinal>? ORDER BY input_ordinal LIMIT 1`,
			Params: []stmt.Param{i("change_id"), i("after_ordinal")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitle_sidecars_3"}, Seeks: []string{"change_id=? AND input_ordinal>?"}},
			Cohorts: []stmt.Cohort{{Name: "next_staged_sidecar", Bindings: []any{int64(51), int64(0)}}}},
		{ID: basicCopy7Peek, Kind: stmt.Read,
			SQL:    `SELECT input_ordinal FROM catalog_info_new_subtitle_sidecars WHERE change_id=? AND input_ordinal>? ORDER BY input_ordinal LIMIT 1`,
			Params: []stmt.Param{i("change_id"), i("after_ordinal")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitle_sidecars_3"}, Seeks: []string{"change_id=? AND input_ordinal>?"}},
			Cohorts: []stmt.Cohort{{Name: "more_staged_sidecars", Bindings: []any{int64(51), int64(8)}}}},
		{ID: basicCopy7Canonical, Kind: stmt.Read,
			SQL:    `SELECT id,size,modified_ns,digest FROM subtitle_sidecars WHERE path=?`,
			Params: []stmt.Param{t("path")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_subtitle_sidecars_2"}, Seeks: []string{"path=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_sidecar_by_path", Bindings: []any{"/media/Arrival.srt"}}}},
		{ID: basicCopy7Insert, Kind: stmt.Write,
			SQL:    `INSERT INTO subtitle_sidecars(id,path,size,modified_ns,digest) VALUES(?,?,?,?,?) RETURNING id,path,size,modified_ns,digest`,
			Params: []stmt.Param{t("id"), t("path"), i("size"), i("modified_ns"), t("digest")}, Plan: stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "install_new_sidecar", Bindings: []any{"sidecar-1", "/media/Arrival.srt", int64(30), int64(-7), "sidecar:30"}}}},
		{ID: basicCopy7Update, Kind: stmt.Write,
			SQL:     `UPDATE subtitle_sidecars SET size=?,modified_ns=?,digest=? WHERE path=? AND size=? AND modified_ns=? AND digest=? RETURNING id,path,size,modified_ns,digest`,
			Params:  []stmt.Param{i("size"), i("modified_ns"), t("digest"), t("path"), i("expected_size"), i("expected_modified_ns"), t("expected_digest")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_subtitle_sidecars_2"}, Seeks: []string{"path=?"}},
			Cohorts: []stmt.Cohort{{Name: "replace_canonical_sidecar", Bindings: []any{int64(31), int64(-7), "sidecar:31", "/media/Arrival.srt", int64(30), int64(-6), "sidecar:30"}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_family7.go"}
	}
	return defs
}
