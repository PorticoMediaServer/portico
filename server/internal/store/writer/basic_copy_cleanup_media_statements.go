package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCleanupBreadthFirst    stmt.ID = "scan.basic_stage.cleanup_media_breadthfirst"
	basicCleanupBreadthAfter    stmt.ID = "scan.basic_stage.cleanup_media_breadthafter"
	basicCleanupBreadthRetained stmt.ID = "scan.basic_stage.cleanup_media_breadthretained"
	basicCleanupBreadthClear    stmt.ID = "scan.basic_stage.cleanup_media_breadthclear"
	basicCleanupChapterFirst    stmt.ID = "scan.basic_stage.cleanup_media_chapterfirst"
	basicCleanupChapterAfter    stmt.ID = "scan.basic_stage.cleanup_media_chapterafter"
	basicCleanupChapterRetained stmt.ID = "scan.basic_stage.cleanup_media_chapterretained"
	basicCleanupChapterDelete   stmt.ID = "scan.basic_stage.cleanup_media_chapterdelete"
)

func basicCopyCleanupMediaDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicCleanupBreadthFirst, Kind: stmt.Read, SQL: `SELECT stream_index,has_asset_stream_breadth FROM asset_streams WHERE asset_id=? ORDER BY stream_index LIMIT 9`, Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "native_key", Bindings: []any{"basic-asset"}}}},
		{ID: basicCleanupBreadthAfter, Kind: stmt.Read, SQL: `SELECT stream_index,has_asset_stream_breadth FROM asset_streams WHERE asset_id=? AND stream_index>? ORDER BY stream_index LIMIT 9`, Params: []stmt.Param{t("asset_token"), i("after_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index>?"}}, Cohorts: []stmt.Cohort{{Name: "native_key", Bindings: []any{"basic-asset", int64(0)}}}},
		{ID: basicCleanupBreadthRetained, Kind: stmt.Read, SQL: `SELECT change_id,asset_id,stream_index FROM catalog_info_new_stream_breadth WHERE change_id=? AND asset_id=? AND stream_index=?`, Params: []stmt.Param{i("change_id"), t("asset_token"), i("stream_index")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_new_stream_breadth"}, Seeks: []string{"change_id=? AND asset_id=? AND stream_index=?"}}, Cohorts: []stmt.Cohort{{Name: "native_key", Bindings: []any{int64(51), "basic-asset", int64(0)}}}},
		{ID: basicCleanupBreadthClear, Kind: stmt.Write, SQL: `UPDATE asset_streams SET has_asset_stream_breadth=0,breadth_audio_description=0,breadth_audio_variant='',breadth_chroma_subsampling='',breadth_fps_numerator=0,breadth_fps_denominator=0,breadth_frame_rate_mode='',breadth_interlace_analysis='',breadth_telecine=0,breadth_dovi_rpu=0,breadth_dovi_base_layer=0,breadth_dovi_metadata_known=0,breadth_dovi_enhancement_type='',breadth_dovi_cm_version='',breadth_playback_frames_revision='',breadth_packet_peak_revision='' WHERE asset_id=? AND stream_index=? AND has_asset_stream_breadth=1 RETURNING asset_id,stream_index,has_asset_stream_breadth,breadth_audio_description,breadth_audio_variant,breadth_chroma_subsampling,breadth_fps_numerator,breadth_fps_denominator,breadth_frame_rate_mode,breadth_interlace_analysis,breadth_telecine,breadth_dovi_rpu,breadth_dovi_base_layer,breadth_dovi_metadata_known,breadth_dovi_enhancement_type,breadth_dovi_cm_version,breadth_playback_frames_revision,breadth_packet_peak_revision`, Params: []stmt.Param{t("asset_token"), i("stream_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index=?"}}, Cohorts: []stmt.Cohort{{Name: "native_key", Bindings: []any{"basic-asset", int64(9)}}}},
		{ID: basicCleanupChapterFirst, Kind: stmt.Read, SQL: `SELECT chapter_index FROM asset_chapters WHERE asset_id=? ORDER BY chapter_index LIMIT 9`, Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapters_1"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "native_key", Bindings: []any{"basic-asset"}}}},
		{ID: basicCleanupChapterAfter, Kind: stmt.Read, SQL: `SELECT chapter_index FROM asset_chapters WHERE asset_id=? AND chapter_index>? ORDER BY chapter_index LIMIT 9`, Params: []stmt.Param{t("asset_token"), i("after_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapters_1"}, Seeks: []string{"asset_id=? AND chapter_index>?"}}, Cohorts: []stmt.Cohort{{Name: "native_key", Bindings: []any{"basic-asset", int64(0)}}}},
		{ID: basicCleanupChapterRetained, Kind: stmt.Read, SQL: `SELECT change_id,asset_id,chapter_index FROM catalog_info_new_chapters WHERE change_id=? AND asset_id=? AND chapter_index=?`, Params: []stmt.Param{i("change_id"), t("asset_token"), i("chapter_index")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_new_chapters"}, Seeks: []string{"change_id=? AND asset_id=? AND chapter_index=?"}}, Cohorts: []stmt.Cohort{{Name: "native_key", Bindings: []any{int64(51), "basic-asset", int64(0)}}}},
		{ID: basicCleanupChapterDelete, Kind: stmt.Write, SQL: `DELETE FROM asset_chapters WHERE asset_id=? AND chapter_index=? RETURNING asset_id,chapter_index`, Params: []stmt.Param{t("asset_token"), i("chapter_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapters_1"}, Seeks: []string{"asset_id=? AND chapter_index=?"}}, Cohorts: []stmt.Cohort{{Name: "native_key", Bindings: []any{"basic-asset", int64(9)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_cleanup_media.go"}
	}
	return defs
}
