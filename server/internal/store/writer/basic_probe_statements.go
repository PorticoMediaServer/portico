package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicProbeHeader      stmt.ID = "scan.basic_stage.probe_header"
	basicProbeAsset       stmt.ID = "scan.basic_stage.probe_asset"
	basicProbeOldPresence stmt.ID = "scan.basic_stage.probe_old_presence"
	basicProbeOldInsert   stmt.ID = "scan.basic_stage.probe_old_insert"
	basicProbeAssetCAS    stmt.ID = "scan.basic_stage.probe_asset_cas"
)

func basicProbeDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	r := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Real} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	ni := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer, Nullable: true} }
	nr := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Real, Nullable: true} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	defs := []stmt.Definition{
		{ID: basicProbeHeader, Kind: stmt.Read, SQL: `SELECT id FROM catalog_info_changes WHERE id=? AND asset_id=? AND asset_token=? AND phase=? AND family_mask=? AND input_revision=? AND job_revision=? AND copy_aux=? AND copy_family=? AND job_lease_owner=? AND job_lease_generation=? AND job_lease_expires_ms=? AND target_shape_id IS NULL AND target_owner_object IS NULL AND target_owner_revision IS NULL`, Params: []stmt.Param{i("change_id"), i("asset_id"), t("asset_token"), i("phase"), i("family_mask"), i("input_revision"), i("job_revision"), i("copy_aux"), i("copy_family"), i("lease_owner"), i("lease_generation"), i("lease_expires_ms")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "first_unfrozen_input", Bindings: []any{int64(51), int64(41), "basic-asset", int64(0), int64(511), int64(7), int64(1), int64(0), int64(0), int64(0), int64(0), int64(0)}}}},
		{ID: basicProbeAsset, Kind: stmt.Read, SQL: `SELECT id FROM catalog_assets WHERE id=? AND token=? AND library_id=? AND path=? AND size=? AND modified_ns=? AND available=? AND container=? AND video_codec=? AND audio_codec=? AND width=? AND height=? AND duration=? AND playback_origin_incarnation=? AND playback_origin_revision=? AND prepared_source_revision=? AND file_size_revision=? AND info_invalidation_revision=? AND info_epoch=? AND info_publication_pending=? AND info_owner_object IS NULL AND info_owner_revision IS NULL`, Params: []stmt.Param{i("asset_id"), t("asset_token"), i("library_id"), t("path"), i("size"), i("modified_ns"), i("available"), t("old_container"), t("old_video_codec"), t("old_audio_codec"), i("old_width"), i("old_height"), r("old_duration"), b("playback_incarnation"), i("old_playback_revision"), i("old_prepared_revision"), i("file_size_revision"), i("invalidation_revision"), i("info_epoch"), i("pending")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "exact_original_scalar_vector", Bindings: []any{int64(41), "basic-asset", int64(7), "/media/Arrival.mkv", int64(42), int64(-11), int64(1), "old", "vp9", "aac", int64(1920), int64(1080), float64(90), make([]byte, 16), int64(101), int64(103), int64(113), int64(109), int64(17), int64(0)}}}},
		{ID: basicProbeOldPresence, Kind: stmt.Read, SQL: `SELECT field FROM catalog_info_scalar_old WHERE change_id=? ORDER BY field LIMIT ?`, Params: []stmt.Param{i("change_id"), i("row_limit")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_scalar_old"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "prior_old_must_be_absent", Bindings: []any{int64(51), int64(1)}}}},
		{ID: basicProbeOldInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_info_scalar_old(change_id,field,kind,old_integer,old_real,old_text) VALUES(?,?,?,?,?,?) RETURNING change_id,field,kind,old_integer,old_real,old_text`, Params: []stmt.Param{i("change_id"), i("field"), i("kind"), ni("old_integer"), nr("old_real"), nt("old_text")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "first_old_text", Bindings: []any{int64(51), int64(4), int64(3), nil, nil, "old"}}}},
		{ID: basicProbeAssetCAS, Kind: stmt.Write, SQL: `UPDATE catalog_assets SET container=?,video_codec=?,audio_codec=?,width=?,height=?,duration=?,playback_origin_revision=?,prepared_source_revision=? WHERE id=? AND token=? AND library_id=? AND path=? AND size=? AND modified_ns=? AND available=? AND container=? AND video_codec=? AND audio_codec=? AND width=? AND height=? AND duration=? AND playback_origin_incarnation=? AND playback_origin_revision=? AND prepared_source_revision=? AND file_size_revision=? AND info_invalidation_revision=? AND info_epoch=? AND info_publication_pending=? AND info_owner_object IS NULL AND info_owner_revision IS NULL RETURNING id,container,video_codec,audio_codec,width,height,duration,playback_origin_revision,prepared_source_revision,file_size_revision,info_invalidation_revision,info_epoch,playback_origin_incarnation`, Params: []stmt.Param{t("new_container"), t("new_video_codec"), t("new_audio_codec"), i("new_width"), i("new_height"), r("new_duration"), i("next_playback_revision"), i("next_prepared_revision"), i("asset_id"), t("asset_token"), i("library_id"), t("path"), i("size"), i("modified_ns"), i("available"), t("old_container"), t("old_video_codec"), t("old_audio_codec"), i("old_width"), i("old_height"), r("old_duration"), b("playback_incarnation"), i("old_playback_revision"), i("old_prepared_revision"), i("file_size_revision"), i("invalidation_revision"), i("info_epoch"), i("pending")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "coherent_six_scalar_replacement", Bindings: []any{"mkv", "h264", "", int64(0), int64(0), float64(100), int64(102), int64(104), int64(41), "basic-asset", int64(7), "/media/Arrival.mkv", int64(42), int64(-11), int64(1), "old", "vp9", "aac", int64(1920), int64(1080), float64(90), make([]byte, 16), int64(101), int64(103), int64(113), int64(109), int64(17), int64(0)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_probe.go"}
	}
	return defs
}
func basicProbeMaxUses(id stmt.ID) int {
	switch id {
	case basicProbeOldInsert:
		return 6
	case basicProbeHeader, basicProbeAsset, basicProbeOldPresence, basicProbeAssetCAS:
		return 1
	default:
		return 0
	}
}
