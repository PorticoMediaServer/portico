package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCleanupSubtitleGapFirst  stmt.ID = "scan.basic_stage.cleanup_subtitle_gap_first"
	basicCleanupSubtitleGapAfter  stmt.ID = "scan.basic_stage.cleanup_subtitle_gap_after"
	basicCleanupSubtitleExact     stmt.ID = "scan.basic_stage.cleanup_subtitle_exact"
	basicCleanupSubtitleRetained  stmt.ID = "scan.basic_stage.cleanup_subtitle_retained"
	basicCleanupSubtitleDelete    stmt.ID = "scan.basic_stage.cleanup_subtitle_delete"
	basicCleanupSubtitleMoreFirst stmt.ID = "scan.basic_stage.cleanup_subtitle_more_first"
	basicCleanupSubtitleMoreAfter stmt.ID = "scan.basic_stage.cleanup_subtitle_more_after"
)

func basicCopySubtitleCleanupDefinitions() []stmt.Definition {
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	defs := []stmt.Definition{
		{ID: basicCleanupSubtitleGapFirst, Kind: stmt.Read, SQL: `SELECT 1 FROM asset_subtitles INDEXED BY asset_subtitles_asset WHERE asset_id=? AND id<? LIMIT 1`, Params: []stmt.Param{t("asset_token"), t("next_id")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}, Seeks: []string{"asset_id=? AND id<?"}}, Cohorts: []stmt.Cohort{{Name: "captured_before", Bindings: []any{"basic-asset", "old"}}}},
		{ID: basicCleanupSubtitleGapAfter, Kind: stmt.Read, SQL: `SELECT 1 FROM asset_subtitles INDEXED BY asset_subtitles_asset WHERE asset_id=? AND id>? AND id<? LIMIT 1`, Params: []stmt.Param{t("asset_token"), t("after_id"), t("next_id")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}, Seeks: []string{"asset_id=? AND id>? AND id<?"}}, Cohorts: []stmt.Cohort{{Name: "captured_before", Bindings: []any{"basic-asset", "a", "old"}}}},
		{ID: basicCleanupSubtitleExact, Kind: stmt.Read, SQL: `SELECT 1 FROM asset_subtitles INDEXED BY asset_subtitles_asset WHERE id=? AND asset_id=? AND origin=? AND locator=? AND stream_index=? AND sidecar_id IS ? AND format=? AND language=? AND title=? AND is_default=? AND is_forced=? AND reason=? AND hearing_impaired=?`, Params: []stmt.Param{t("id"), t("asset_id"), t("origin"), t("locator"), i("stream_index"), {Name: "sidecar_id", Type: stmt.Text, Nullable: true}, t("format"), t("language"), t("title"), i("is_default"), i("is_forced"), t("reason"), i("hearing_impaired")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}, Seeks: []string{"asset_id=? AND id=?"}}, Cohorts: []stmt.Cohort{{Name: "captured_before", Bindings: []any{"old", "basic-asset", "embedded", "2", int64(2), nil, "srt", "en", "old", int64(0), int64(0), "", int64(0)}}}},
		{ID: basicCleanupSubtitleRetained, Kind: stmt.Read, SQL: `SELECT input_ordinal FROM catalog_info_new_subtitles WHERE change_id=? AND id=? AND asset_id=?`, Params: []stmt.Param{i("change_id"), t("id"), t("asset_token")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_new_subtitles"}, Seeks: []string{"change_id=? AND id=?"}}, Cohorts: []stmt.Cohort{{Name: "captured_before", Bindings: []any{int64(51), "old", "basic-asset"}}}},
		{ID: basicCleanupSubtitleDelete, Kind: stmt.Write, SQL: `DELETE FROM asset_subtitles INDEXED BY asset_subtitles_asset WHERE id=? AND asset_id=? AND origin=? AND locator=? AND stream_index=? AND sidecar_id IS ? AND format=? AND language=? AND title=? AND is_default=? AND is_forced=? AND reason=? AND hearing_impaired=? RETURNING id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired`, Params: []stmt.Param{t("id"), t("asset_id"), t("origin"), t("locator"), i("stream_index"), {Name: "sidecar_id", Type: stmt.Text, Nullable: true}, t("format"), t("language"), t("title"), i("is_default"), i("is_forced"), t("reason"), i("hearing_impaired")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}, Seeks: []string{"asset_id=? AND id=?"}}, Cohorts: []stmt.Cohort{{Name: "captured_before", Bindings: []any{"old", "basic-asset", "embedded", "2", int64(2), nil, "srt", "en", "old", int64(0), int64(0), "", int64(0)}}}},
		{ID: basicCleanupSubtitleMoreFirst, Kind: stmt.Read, SQL: `SELECT 1 FROM asset_subtitles INDEXED BY asset_subtitles_asset WHERE asset_id=? LIMIT 1`, Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "captured_before", Bindings: []any{"basic-asset"}}}},
		{ID: basicCleanupSubtitleMoreAfter, Kind: stmt.Read, SQL: `SELECT 1 FROM asset_subtitles INDEXED BY asset_subtitles_asset WHERE asset_id=? AND id>? LIMIT 1`, Params: []stmt.Param{t("asset_token"), t("after_id")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"asset_subtitles_asset"}, Seeks: []string{"asset_id=? AND id>?"}}, Cohorts: []stmt.Cohort{{Name: "captured_before", Bindings: []any{"basic-asset", "old"}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_subtitle_cleanup.go"}
	}
	return defs
}
