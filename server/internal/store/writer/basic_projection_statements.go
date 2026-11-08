package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicProjectionLengths stmt.ID = "scan.basic_projection.asset_lengths"
	basicProjectionAsset   stmt.ID = "scan.basic_projection.asset"
	basicProjectionObject  stmt.ID = "scan.basic_projection.object"
)

func basicProjectionDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicProjectionLengths, Kind: stmt.Read, SQL: `SELECT octet_length(token),octet_length(container),octet_length(video_codec),octet_length(audio_codec),octet_length(playback_origin_incarnation) FROM catalog_assets WHERE id=? AND library_id=?`, Params: []stmt.Param{i("asset_id"), i("library_id")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "native_asset_bytes", Bindings: []any{int64(41), int64(7)}}}},
		{ID: basicProjectionAsset, Kind: stmt.Read, SQL: `SELECT token,container,video_codec,audio_codec,width,height,duration,playback_origin_incarnation,playback_origin_revision,prepared_source_revision,file_size_revision,info_invalidation_revision,info_epoch FROM catalog_assets WHERE id=? AND library_id=? AND path=? AND size=? AND modified_ns=? AND available=1 AND info_publication_pending=0`, Params: []stmt.Param{i("asset_id"), i("library_id"), t("asset_path"), i("size"), i("modified_ns")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "first_preprobe_vector", Bindings: []any{int64(41), int64(7), "/media/Arrival.mkv", int64(42), int64(-11)}}}},
		{ID: basicProjectionObject, Kind: stmt.Read, SQL: `SELECT 1 FROM inventory_objects WHERE id=? AND source_id=? AND asset_id=? AND root_incarnation=? AND revision=? AND size=? AND modified_ns=? AND seen_job=? AND state='available' AND retired=0`, Params: []stmt.Param{t("object_id"), t("source_id"), t("asset_token"), t("incarnation"), t("object_revision"), i("size"), i("modified_ns"), t("run_id")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_inventory_objects_1"}, Seeks: []string{"id=?"}}, Cohorts: []stmt.Cohort{{Name: "exact_observation", Bindings: []any{"basic-object", "basic-source", "basic-asset", "basic-incarnation", "object:019", int64(42), int64(-11), "basic-run"}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_projection.go"}
	}
	return defs
}
