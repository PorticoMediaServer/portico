package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCopy3Staged    stmt.ID = "scan.basic_copy.family3_staged"
	basicCopy3Canonical stmt.ID = "scan.basic_copy.family3_canonical"
	basicCopy3Update    stmt.ID = "scan.basic_copy.family3_update"
	basicCopy3WalkFirst stmt.ID = "scan.basic_copy.family3_walk_first"
	basicCopy3WalkAfter stmt.ID = "scan.basic_copy.family3_walk_after"
	basicCopy3Retained  stmt.ID = "scan.basic_copy.family3_retained"
	basicCopy3Clear     stmt.ID = "scan.basic_copy.family3_clear"
)

var _ = registerBasicCopyFamily(3, basicCopyFamilySpec{copy: copyBasicFamily3, defs: basicCopyFamily3Definitions, maxUses: basicCopyFamily3MaxUses})

func basicCopyFamily3MaxUses(id stmt.ID) int {
	switch id {
	case basicCopy3Staged, basicCopy3WalkFirst, basicCopy3WalkAfter:
		return 1
	case basicCopy3Canonical, basicCopy3Update, basicCopy3Retained, basicCopy3Clear:
		return basicCopy3Install
	}
	return 0
}

// Family 3 (folded asset_streams breadth): every column lives on the family-2
// stream row, so install and cleanup are one row CAS each on that exact key.
func basicCopyFamily3Definitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicCopy3Staged, Kind: stmt.Read,
			SQL:    `SELECT input_ordinal,asset_id,stream_index,audio_description,audio_variant,chroma_subsampling,fps_numerator,fps_denominator,frame_rate_mode,interlace_analysis,telecine,dovi_rpu,dovi_base_layer,dovi_metadata_known,dovi_enhancement_type,dovi_cm_version,playback_frames_revision,packet_peak_revision FROM catalog_info_new_stream_breadth WHERE change_id=? AND asset_id=? AND input_ordinal>=? ORDER BY input_ordinal LIMIT 10`,
			Params: []stmt.Param{i("change_id"), t("asset_token"), i("after_ordinal")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_stream_breadth_2"}, Seeks: []string{"change_id=? AND input_ordinal>?"}},
			Cohorts: []stmt.Cohort{{Name: "staged_breadth_keyset", Bindings: []any{int64(51), "basic-asset", int64(0)}}}},
		{ID: basicCopy3Canonical, Kind: stmt.Read,
			SQL:    `SELECT has_asset_stream_breadth,breadth_audio_description,breadth_audio_variant,breadth_chroma_subsampling,breadth_fps_numerator,breadth_fps_denominator,breadth_frame_rate_mode,breadth_interlace_analysis,breadth_telecine,breadth_dovi_rpu,breadth_dovi_base_layer,breadth_dovi_metadata_known,breadth_dovi_enhancement_type,breadth_dovi_cm_version,breadth_playback_frames_revision,breadth_packet_peak_revision FROM asset_streams WHERE asset_id=? AND stream_index=?`,
			Params: []stmt.Param{t("asset_token"), i("stream_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index=?"}},
			Cohorts: []stmt.Cohort{{Name: "canonical_breadth", Bindings: []any{"basic-asset", int64(0)}}}},
		{ID: basicCopy3Update, Kind: stmt.Write,
			SQL:     `UPDATE asset_streams SET has_asset_stream_breadth=1,breadth_audio_description=?,breadth_audio_variant=?,breadth_chroma_subsampling=?,breadth_fps_numerator=?,breadth_fps_denominator=?,breadth_frame_rate_mode=?,breadth_interlace_analysis=?,breadth_telecine=?,breadth_dovi_rpu=?,breadth_dovi_base_layer=?,breadth_dovi_metadata_known=?,breadth_dovi_enhancement_type=?,breadth_dovi_cm_version=?,breadth_playback_frames_revision=?,breadth_packet_peak_revision=? WHERE asset_id=? AND stream_index=? AND has_asset_stream_breadth=? AND breadth_audio_description=? AND breadth_audio_variant=? AND breadth_chroma_subsampling=? AND breadth_fps_numerator=? AND breadth_fps_denominator=? AND breadth_frame_rate_mode=? AND breadth_interlace_analysis=? AND breadth_telecine=? AND breadth_dovi_rpu=? AND breadth_dovi_base_layer=? AND breadth_dovi_metadata_known=? AND breadth_dovi_enhancement_type=? AND breadth_dovi_cm_version=? AND breadth_playback_frames_revision=? AND breadth_packet_peak_revision=? RETURNING asset_id,stream_index,has_asset_stream_breadth,breadth_audio_description,breadth_audio_variant,breadth_chroma_subsampling,breadth_fps_numerator,breadth_fps_denominator,breadth_frame_rate_mode,breadth_interlace_analysis,breadth_telecine,breadth_dovi_rpu,breadth_dovi_base_layer,breadth_dovi_metadata_known,breadth_dovi_enhancement_type,breadth_dovi_cm_version,breadth_playback_frames_revision,breadth_packet_peak_revision`,
			Params:  []stmt.Param{i("audio_description"), t("audio_variant"), t("chroma_subsampling"), i("fps_numerator"), i("fps_denominator"), t("frame_rate_mode"), t("interlace_analysis"), i("telecine"), i("dovi_rpu"), i("dovi_base_layer"), i("dovi_metadata_known"), t("dovi_enhancement_type"), t("dovi_cm_version"), t("playback_frames_revision"), t("packet_peak_revision"), t("asset_token"), i("stream_index"), i("expected_has"), i("expected_audio_description"), t("expected_audio_variant"), t("expected_chroma_subsampling"), i("expected_fps_numerator"), i("expected_fps_denominator"), t("expected_frame_rate_mode"), t("expected_interlace_analysis"), i("expected_telecine"), i("expected_dovi_rpu"), i("expected_dovi_base_layer"), i("expected_dovi_metadata_known"), t("expected_dovi_enhancement_type"), t("expected_dovi_cm_version"), t("expected_playback_frames_revision"), t("expected_packet_peak_revision")},
			Plan:    stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index=?"}},
			Cohorts: []stmt.Cohort{{Name: "install_staged_breadth", Bindings: []any{int64(0), "", "4:2:0", int64(24000), int64(1001), "constant", "progressive", int64(0), int64(0), int64(0), int64(1), "", "", "", "", "basic-asset", int64(0), int64(0), int64(0), "", "", int64(0), int64(0), "", "", int64(0), int64(0), int64(0), int64(0), "", "", "", ""}}}},
		{ID: basicCopy3WalkFirst, Kind: stmt.Read,
			SQL:    `SELECT stream_index FROM asset_streams WHERE asset_id=? AND has_asset_stream_breadth=1 ORDER BY stream_index LIMIT 9`,
			Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "breadth_walk_first", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopy3WalkAfter, Kind: stmt.Read,
			SQL:    `SELECT stream_index FROM asset_streams WHERE asset_id=? AND stream_index>? AND has_asset_stream_breadth=1 ORDER BY stream_index LIMIT 9`,
			Params: []stmt.Param{t("asset_token"), i("after_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index>?"}},
			Cohorts: []stmt.Cohort{{Name: "breadth_walk_after", Bindings: []any{"basic-asset", int64(0)}}}},
		{ID: basicCopy3Retained, Kind: stmt.Read,
			SQL:    `SELECT change_id,asset_id,stream_index FROM catalog_info_new_stream_breadth WHERE change_id=? AND asset_id=? AND stream_index=?`,
			Params: []stmt.Param{i("change_id"), t("asset_token"), i("stream_index")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_new_stream_breadth"}, Seeks: []string{"change_id=? AND asset_id=? AND stream_index=?"}},
			Cohorts: []stmt.Cohort{{Name: "staged_breadth_key", Bindings: []any{int64(51), "basic-asset", int64(0)}}}},
		{ID: basicCopy3Clear, Kind: stmt.Write,
			SQL:    `UPDATE asset_streams SET has_asset_stream_breadth=0,breadth_audio_description=0,breadth_audio_variant='',breadth_chroma_subsampling='',breadth_fps_numerator=0,breadth_fps_denominator=0,breadth_frame_rate_mode='',breadth_interlace_analysis='',breadth_telecine=0,breadth_dovi_rpu=0,breadth_dovi_base_layer=0,breadth_dovi_metadata_known=0,breadth_dovi_enhancement_type='',breadth_dovi_cm_version='',breadth_playback_frames_revision='',breadth_packet_peak_revision='' WHERE asset_id=? AND stream_index=? AND has_asset_stream_breadth=1 RETURNING asset_id,stream_index,has_asset_stream_breadth,breadth_audio_description,breadth_audio_variant,breadth_chroma_subsampling,breadth_fps_numerator,breadth_fps_denominator,breadth_frame_rate_mode,breadth_interlace_analysis,breadth_telecine,breadth_dovi_rpu,breadth_dovi_base_layer,breadth_dovi_metadata_known,breadth_dovi_enhancement_type,breadth_dovi_cm_version,breadth_playback_frames_revision,breadth_packet_peak_revision`,
			Params: []stmt.Param{t("asset_token"), i("stream_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index=?"}},
			Cohorts: []stmt.Cohort{{Name: "obsolete_canonical_breadth", Bindings: []any{"basic-asset", int64(0)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_family3.go"}
	}
	return defs
}
