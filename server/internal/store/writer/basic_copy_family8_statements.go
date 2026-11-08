package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCopy8StagedFirst stmt.ID = "scan.basic_copy.family8_staged_first"
	basicCopy8StagedAfter stmt.ID = "scan.basic_copy.family8_staged_after"
	basicCopy8PeekStaged  stmt.ID = "scan.basic_copy.family8_peek_staged"
	basicCopy8SidecarPath stmt.ID = "scan.basic_copy.family8_sidecar_path"
	basicCopy8Sidecar     stmt.ID = "scan.basic_copy.family8_sidecar"
	basicCopy8Facts       stmt.ID = "scan.basic_copy.family8_facts"
	basicCopy8Canonical   stmt.ID = "scan.basic_copy.family8_canonical"
	basicCopy8Insert      stmt.ID = "scan.basic_copy.family8_insert"
	basicCopy8Update      stmt.ID = "scan.basic_copy.family8_update"
	basicCopy8Next        stmt.ID = "scan.basic_copy.family8_next"
	basicCopy8PeekNext    stmt.ID = "scan.basic_copy.family8_peek_next"
	basicCopy8StagedKey   stmt.ID = "scan.basic_copy.family8_staged_key"
	basicCopy8Delete      stmt.ID = "scan.basic_copy.family8_delete"
)

var _ = registerBasicCopyFamily(8, basicCopyFamilySpec{copy: copyBasicFamily8, defs: basicCopyFamily8Definitions, maxUses: basicCopyFamily8MaxUses})

func basicCopyFamily8MaxUses(id stmt.ID) int {
	switch id {
	case basicCopy8StagedAfter, basicCopy8SidecarPath, basicCopy8Sidecar, basicCopy8Canonical, basicCopy8Insert, basicCopy8Update,
		basicCopy8Next, basicCopy8StagedKey, basicCopy8Delete:
		return 8
	case basicCopy8StagedFirst, basicCopy8PeekStaged, basicCopy8Facts, basicCopy8PeekNext:
		return 1
	}
	return 0
}

// Family 8 (asset_subtitles): canonical identity (asset_id,origin,locator).
// Install reads staged rows by input_ordinal, resolves sidecars by path and
// keeps a canonical id; cleanup walks the asset's canonical ids through the
// adopted asset_subtitles_asset (asset_id,id) index.
func basicCopyFamily8Definitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	defs := []stmt.Definition{
		{ID: basicCopy8StagedFirst, Kind: stmt.Read,
			SQL:    `SELECT input_ordinal,id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired FROM catalog_info_new_subtitles WHERE change_id=? AND input_ordinal>=? AND asset_id=? ORDER BY input_ordinal LIMIT 1`,
			Params: []stmt.Param{i("change_id"), i("from_ordinal"), t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitles_3"}, Seeks: []string{"change_id=? AND input_ordinal>?"}},
			Cohorts: []stmt.Cohort{{Name: "first_staged_subtitle", Bindings: []any{int64(51), int64(0), "basic-asset"}}}},
		{ID: basicCopy8StagedAfter, Kind: stmt.Read,
			SQL:    `SELECT input_ordinal,id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired FROM catalog_info_new_subtitles WHERE change_id=? AND input_ordinal>? AND asset_id=? ORDER BY input_ordinal LIMIT 1`,
			Params: []stmt.Param{i("change_id"), i("after_ordinal"), t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitles_3"}, Seeks: []string{"change_id=? AND input_ordinal>?"}},
			Cohorts: []stmt.Cohort{{Name: "next_staged_subtitle", Bindings: []any{int64(51), int64(0), "basic-asset"}}}},
		{ID: basicCopy8PeekStaged, Kind: stmt.Read,
			SQL:    `SELECT input_ordinal FROM catalog_info_new_subtitles WHERE change_id=? AND input_ordinal>? AND asset_id=? ORDER BY input_ordinal LIMIT 1`,
			Params: []stmt.Param{i("change_id"), i("after_ordinal"), t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitles_3"}, Seeks: []string{"change_id=? AND input_ordinal>?"}},
			Cohorts: []stmt.Cohort{{Name: "more_staged_subtitles", Bindings: []any{int64(51), int64(8), "basic-asset"}}}},
		{ID: basicCopy8SidecarPath, Kind: stmt.Read,
			SQL:    `SELECT path FROM catalog_info_new_subtitle_sidecars WHERE change_id=? AND id=?`,
			Params: []stmt.Param{i("change_id"), t("sidecar_id")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_new_subtitle_sidecars"}, Seeks: []string{"change_id=? AND id=?"}},
			Cohorts: []stmt.Cohort{{Name: "staged_sidecar_path", Bindings: []any{int64(51), "sidecar-1"}}}},
		{ID: basicCopy8Sidecar, Kind: stmt.Read,
			SQL:    `SELECT id FROM subtitle_sidecars WHERE path=?`,
			Params: []stmt.Param{t("path")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_subtitle_sidecars_2"}, Seeks: []string{"path=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_sidecar_id", Bindings: []any{"/media/Arrival.srt"}}}},
		{ID: basicCopy8Facts, Kind: stmt.Read,
			SQL:    `SELECT asset_id FROM asset_subtitle_facts WHERE asset_id=?`,
			Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitle_facts_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "required_subtitle_fact", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopy8Canonical, Kind: stmt.Read,
			SQL:    `SELECT id,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired FROM asset_subtitles WHERE asset_id=? AND origin=? AND locator=?`,
			Params: []stmt.Param{t("asset_token"), t("origin"), t("locator")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitles_2"}, Seeks: []string{"asset_id=? AND origin=? AND locator=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_track_by_identity", Bindings: []any{"basic-asset", "embedded", "2"}}}},
		{ID: basicCopy8Insert, Kind: stmt.Write,
			SQL:    `INSERT INTO asset_subtitles(id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired`,
			Params: []stmt.Param{t("id"), t("asset_token"), t("origin"), t("locator"), i("stream_index"), nt("sidecar_id"), t("format"), t("language"), t("title"), i("is_default"), i("is_forced"), t("reason"), i("hearing_impaired")}, Plan: stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "install_new_track", Bindings: []any{"subtitle-1", "basic-asset", "embedded", "2", int64(2), nil, "subrip", "en", "English", int64(0), int64(0), "", int64(0)}}}},
		{ID: basicCopy8Update, Kind: stmt.Write,
			SQL:     `UPDATE asset_subtitles SET stream_index=?,sidecar_id=?,format=?,language=?,title=?,is_default=?,is_forced=?,reason=?,hearing_impaired=? WHERE id=? AND asset_id=? AND origin=? AND locator=? RETURNING id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired`,
			Params:  []stmt.Param{i("stream_index"), nt("sidecar_id"), t("format"), t("language"), t("title"), i("is_default"), i("is_forced"), t("reason"), i("hearing_impaired"), t("id"), t("asset_token"), t("origin"), t("locator")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitles_2"}, Seeks: []string{"asset_id=? AND origin=? AND locator=?"}},
			Cohorts: []stmt.Cohort{{Name: "replace_canonical_track", Bindings: []any{int64(2), nil, "subrip", "en", "English", int64(0), int64(0), "", int64(0), "subtitle-1", "basic-asset", "embedded", "2"}}}},
		{ID: basicCopy8Next, Kind: stmt.Read,
			SQL:    `SELECT id,origin,locator FROM asset_subtitles INDEXED BY asset_subtitles_asset WHERE asset_id=? AND id>? ORDER BY id LIMIT 1`,
			Params: []stmt.Param{t("asset_token"), t("after_id")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}, Seeks: []string{"asset_id=? AND id>?"}},
			Cohorts: []stmt.Cohort{{Name: "next_canonical_track", Bindings: []any{"basic-asset", ""}}}},
		{ID: basicCopy8PeekNext, Kind: stmt.Read,
			SQL:    `SELECT id FROM asset_subtitles INDEXED BY asset_subtitles_asset WHERE asset_id=? AND id>? ORDER BY id LIMIT 1`,
			Params: []stmt.Param{t("asset_token"), t("after_id")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}, Seeks: []string{"asset_id=? AND id>?"}},
			Cohorts: []stmt.Cohort{{Name: "more_canonical_tracks", Bindings: []any{"basic-asset", "keep-1"}}}},
		{ID: basicCopy8StagedKey, Kind: stmt.Read,
			SQL:    `SELECT id FROM catalog_info_new_subtitles WHERE change_id=? AND asset_id=? AND origin=? AND locator=?`,
			Params: []stmt.Param{i("change_id"), t("asset_token"), t("origin"), t("locator")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitles_1"}, Seeks: []string{"change_id=? AND asset_id=? AND origin=? AND locator=?"}},
			Cohorts: []stmt.Cohort{{Name: "staged_track_identity", Bindings: []any{int64(51), "basic-asset", "embedded", "2"}}}},
		{ID: basicCopy8Delete, Kind: stmt.Write,
			SQL:    `DELETE FROM asset_subtitles WHERE id=? AND asset_id=? RETURNING id`,
			Params: []stmt.Param{t("id"), t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitles_1"}, Seeks: []string{"id=?"}},
			Cohorts: []stmt.Cohort{{Name: "obsolete_canonical_track", Bindings: []any{"stale-1", "basic-asset"}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_family8.go"}
	}
	return defs
}
