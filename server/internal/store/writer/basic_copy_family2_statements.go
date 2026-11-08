package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCopy2Staged    stmt.ID = "scan.basic_copy.family2_staged"
	basicCopy2Canonical stmt.ID = "scan.basic_copy.family2_canonical"
	basicCopy2Insert    stmt.ID = "scan.basic_copy.family2_insert"
	basicCopy2Update    stmt.ID = "scan.basic_copy.family2_update"
	basicCopy2WalkFirst stmt.ID = "scan.basic_copy.family2_walk_first"
	basicCopy2WalkAfter stmt.ID = "scan.basic_copy.family2_walk_after"
	basicCopy2Retained  stmt.ID = "scan.basic_copy.family2_retained"
	basicCopy2Delete    stmt.ID = "scan.basic_copy.family2_delete"
)

var _ = registerBasicCopyFamily(2, basicCopyFamilySpec{copy: copyBasicFamily2, defs: basicCopyFamily2Definitions, maxUses: basicCopyFamily2MaxUses})

func basicCopyFamily2MaxUses(id stmt.ID) int {
	switch id {
	case basicCopy2Staged, basicCopy2WalkFirst, basicCopy2WalkAfter:
		return 1
	case basicCopy2Canonical, basicCopy2Insert, basicCopy2Update, basicCopy2Retained, basicCopy2Delete:
		return basicCopy2Install
	}
	return 0
}

// Family 2 (asset_streams): the stream PK keyset, its per-ordinal staged rows
// and the canonical key walk. Every access is an index seek, never a scan.
func basicCopyFamily2Definitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicCopy2Staged, Kind: stmt.Read,
			SQL:    `SELECT input_ordinal,asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json FROM catalog_info_new_streams WHERE change_id=? AND asset_id=? AND input_ordinal>=? ORDER BY input_ordinal LIMIT 10`,
			Params: []stmt.Param{i("change_id"), t("asset_token"), i("after_ordinal")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_streams_2"}, Seeks: []string{"change_id=? AND input_ordinal>?"}},
			Cohorts: []stmt.Cohort{{Name: "staged_stream_keyset", Bindings: []any{int64(51), "basic-asset", int64(0)}}}},
		{ID: basicCopy2Canonical, Kind: stmt.Read,
			SQL:    `SELECT stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json FROM asset_streams WHERE asset_id=? AND stream_index=?`,
			Params: []stmt.Param{t("asset_token"), i("stream_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_stream", Bindings: []any{"basic-asset", int64(0)}}}},
		{ID: basicCopy2Insert, Kind: stmt.Write,
			SQL:    `INSERT INTO asset_streams(asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json`,
			Params: []stmt.Param{t("asset_token"), i("stream_index"), t("type"), t("codec"), t("language"), t("title"), i("channels"), t("channel_layout"), i("is_default"), i("is_forced"), t("color_transfer"), t("color_primaries"), t("detail_json")}, Plan: stmt.Plan{Hot: true},
			Cohorts: []stmt.Cohort{{Name: "install_staged_stream", Bindings: []any{"basic-asset", int64(0), "video", "h264", "", "", int64(0), "", int64(1), int64(0), "bt709", "bt709", `{"width":1920}`}}}},
		{ID: basicCopy2Update, Kind: stmt.Write,
			SQL:     `UPDATE asset_streams SET type=?,codec=?,language=?,title=?,channels=?,channel_layout=?,is_default=?,is_forced=?,color_transfer=?,color_primaries=?,detail_json=? WHERE asset_id=? AND stream_index=? AND type=? AND codec=? AND language=? AND title=? AND channels=? AND channel_layout=? AND is_default=? AND is_forced=? AND color_transfer=? AND color_primaries=? AND detail_json=? RETURNING asset_id,stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,detail_json`,
			Params:  []stmt.Param{t("type"), t("codec"), t("language"), t("title"), i("channels"), t("channel_layout"), i("is_default"), i("is_forced"), t("color_transfer"), t("color_primaries"), t("detail_json"), t("asset_token"), i("stream_index"), t("expected_type"), t("expected_codec"), t("expected_language"), t("expected_title"), i("expected_channels"), t("expected_channel_layout"), i("expected_is_default"), i("expected_is_forced"), t("expected_color_transfer"), t("expected_color_primaries"), t("expected_detail_json")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index=?"}},
			Cohorts: []stmt.Cohort{{Name: "replace_canonical_stream", Bindings: []any{"video", "h264", "", "", int64(0), "", int64(1), int64(0), "bt709", "bt709", `{"width":1920}`, "basic-asset", int64(0), "video", "hevc", "", "", int64(0), "", int64(0), int64(0), "", "", ""}}}},
		{ID: basicCopy2WalkFirst, Kind: stmt.Read,
			SQL:    `SELECT stream_index FROM asset_streams WHERE asset_id=? ORDER BY stream_index LIMIT 9`,
			Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_walk_first", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopy2WalkAfter, Kind: stmt.Read,
			SQL:    `SELECT stream_index FROM asset_streams WHERE asset_id=? AND stream_index>? ORDER BY stream_index LIMIT 9`,
			Params: []stmt.Param{t("asset_token"), i("after_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index>?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_walk_after", Bindings: []any{"basic-asset", int64(0)}}}},
		{ID: basicCopy2Retained, Kind: stmt.Read,
			SQL:    `SELECT change_id,asset_id,stream_index FROM catalog_info_new_streams WHERE change_id=? AND asset_id=? AND stream_index=?`,
			Params: []stmt.Param{i("change_id"), t("asset_token"), i("stream_index")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_new_streams"}, Seeks: []string{"change_id=? AND asset_id=? AND stream_index=?"}},
			Cohorts: []stmt.Cohort{{Name: "staged_stream_key", Bindings: []any{int64(51), "basic-asset", int64(0)}}}},
		{ID: basicCopy2Delete, Kind: stmt.Write,
			SQL:    `DELETE FROM asset_streams WHERE asset_id=? AND stream_index=? RETURNING asset_id,stream_index`,
			Params: []stmt.Param{t("asset_token"), i("stream_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index=?"}},
			Cohorts: []stmt.Cohort{{Name: "obsolete_canonical_stream", Bindings: []any{"basic-asset", int64(0)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_family2.go"}
	}
	return defs
}
