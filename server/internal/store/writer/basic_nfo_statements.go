package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicNFOLibrary      stmt.ID = "scan.basic_accept.nfo_library"
	basicNFORows         stmt.ID = "scan.basic_accept.nfo_rows"
	basicNFOStatusRead   stmt.ID = "scan.basic_accept.nfo_status"
	basicNFODelete       stmt.ID = "scan.basic_accept.nfo_delete"
	basicNFOInsert       stmt.ID = "scan.basic_accept.nfo_insert"
	basicNFOStatusInsert stmt.ID = "scan.basic_accept.nfo_status_insert"
	basicNFOStatusCAS    stmt.ID = "scan.basic_accept.nfo_status_cas"
)

func basicNFODefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicNFOLibrary, Kind: stmt.Read, SQL: `SELECT library_id FROM catalog_libraries WHERE id=?`, Params: []stmt.Param{i("library_id")}, Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_libraries"}, Seeks: []string{"rowid=?"}}, Cohorts: []stmt.Cohort{{Name: "same_native_library", Bindings: []any{int64(7)}}}},
		{ID: basicNFORows, Kind: stmt.Read, SQL: `SELECT locator,kind,coordinate,digest,payload,source_size,source_modified,observed_at FROM video_nfo_evidence WHERE library_id=? AND asset_id=? ORDER BY locator,kind,coordinate LIMIT ?`, Params: []stmt.Param{t("library_id"), t("asset_id"), i("row_limit")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_video_nfo_evidence_1"}, Seeks: []string{"library_id=? AND asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "complete_before_nfo", Bindings: []any{"basic-library", "basic-asset", int64(2)}}}},
		{ID: basicNFOStatusRead, Kind: stmt.Read, SQL: `SELECT status,issue FROM video_nfo_status WHERE library_id=? AND asset_id=?`, Params: []stmt.Param{t("library_id"), t("asset_id")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_video_nfo_status_1"}, Seeks: []string{"library_id=? AND asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "prior_nfo_status", Bindings: []any{"basic-library", "basic-asset"}}}},
		{ID: basicNFODelete, Kind: stmt.Write, SQL: `DELETE FROM video_nfo_evidence WHERE library_id=? AND asset_id=? AND locator=? AND kind=? AND coordinate=? AND digest=? AND payload=? AND source_size=? AND source_modified=? AND observed_at=?`, Params: []stmt.Param{t("library_id"), t("asset_id"), t("locator"), t("kind"), t("coordinate"), t("digest"), t("payload"), i("source_size"), i("source_modified"), t("observed_at")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_video_nfo_evidence_1"}, Seeks: []string{"library_id=? AND asset_id=? AND locator=? AND kind=? AND coordinate=?"}}, Cohorts: []stmt.Cohort{{Name: "exact_old_nfo", Bindings: []any{"basic-library", "basic-asset", "locator-1", "movie", "[null,null,null]", "digest-1", `{"title":"Old"}`, int64(42), int64(-11), "old-time"}}}},
		{ID: basicNFOInsert, Kind: stmt.Write, SQL: `INSERT INTO video_nfo_evidence(library_id,asset_id,locator,kind,coordinate,digest,payload,source_size,source_modified,observed_at) VALUES(?,?,?,?,?,?,?,?,?,?) RETURNING library_id,asset_id,locator,kind,coordinate,digest,payload,source_size,source_modified,observed_at`, Params: []stmt.Param{t("library_id"), t("asset_id"), t("locator"), t("kind"), t("coordinate"), t("digest"), t("payload"), i("source_size"), i("source_modified"), t("observed_at")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "new_nfo", Bindings: []any{"basic-library", "basic-asset", "locator-2", "movie", "[null,null,null]", "digest-2", `{"title":"Arrival"}`, int64(42), int64(-11), "1970-01-01T00:00:02Z"}}}},
		{ID: basicNFOStatusInsert, Kind: stmt.Write, SQL: `INSERT INTO video_nfo_status(library_id,asset_id,status,issue) VALUES(?,?,?,?) RETURNING library_id,asset_id,status,issue`, Params: []stmt.Param{t("library_id"), t("asset_id"), t("status"), t("issue")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "first_nfo_status", Bindings: []any{"basic-library", "basic-asset", "observed", ""}}}},
		{ID: basicNFOStatusCAS, Kind: stmt.Write, SQL: `UPDATE video_nfo_status SET status=?,issue=? WHERE library_id=? AND asset_id=? AND status=? AND issue=? RETURNING library_id,asset_id,status,issue`, Params: []stmt.Param{t("next_status"), t("next_issue"), t("library_id"), t("asset_id"), t("old_status"), t("old_issue")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_video_nfo_status_1"}, Seeks: []string{"library_id=? AND asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "replace_nfo_status", Bindings: []any{"observed", "", "basic-library", "basic-asset", "observed", ""}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_nfo.go"}
	}
	return defs
}
