package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCleanupStreamFirst    stmt.ID = "scan.basic_stage.cleanup_stream_first"
	basicCleanupStreamAfter    stmt.ID = "scan.basic_stage.cleanup_stream_after"
	basicCleanupStreamRetained stmt.ID = "scan.basic_stage.cleanup_stream_retained"
	basicCleanupStreamDelete   stmt.ID = "scan.basic_stage.cleanup_stream_delete"
)

func basicCopyStreamCleanupDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicCleanupStreamFirst, Kind: stmt.Read, SQL: `SELECT stream_index FROM asset_streams WHERE asset_id=? ORDER BY stream_index LIMIT 9`, Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "first_including_zero", Bindings: []any{"basic-asset"}}}},
		{ID: basicCleanupStreamAfter, Kind: stmt.Read, SQL: `SELECT stream_index FROM asset_streams WHERE asset_id=? AND stream_index>? ORDER BY stream_index LIMIT 9`, Params: []stmt.Param{t("asset_token"), i("after_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index>?"}}, Cohorts: []stmt.Cohort{{Name: "after_native_key", Bindings: []any{"basic-asset", int64(0)}}}},
		{ID: basicCleanupStreamRetained, Kind: stmt.Read, SQL: `SELECT change_id,asset_id,stream_index FROM catalog_info_new_streams WHERE change_id=? AND asset_id=? AND stream_index=?`, Params: []stmt.Param{i("change_id"), t("asset_token"), i("stream_index")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_new_streams"}, Seeks: []string{"change_id=? AND asset_id=? AND stream_index=?"}}, Cohorts: []stmt.Cohort{{Name: "exact_retained_key", Bindings: []any{int64(51), "basic-asset", int64(0)}}}},
		{ID: basicCleanupStreamDelete, Kind: stmt.Write, SQL: `DELETE FROM asset_streams WHERE asset_id=? AND stream_index=? RETURNING asset_id,stream_index`, Params: []stmt.Param{t("asset_token"), i("stream_index")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_streams_1"}, Seeks: []string{"asset_id=? AND stream_index=?"}}, Cohorts: []stmt.Cohort{{Name: "exact_obsolete_key", Bindings: []any{"basic-asset", int64(9)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_stream_cleanup.go"}
	}
	return defs
}
