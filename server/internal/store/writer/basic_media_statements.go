package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicMediaChapterFactInsert  stmt.ID = "scan.basic_stage.chapter_fact_insert"
	basicMediaChapterFactRows    stmt.ID = "scan.basic_seal.chapter_fact_rows"
	basicMediaChapterInsert      stmt.ID = "scan.basic_stage.chapter_insert"
	basicMediaChapterRows        stmt.ID = "scan.basic_seal.chapter_rows"
	basicMediaSubtitleFactInsert stmt.ID = "scan.basic_stage.subtitle_fact_insert"
	basicMediaSubtitleFactRows   stmt.ID = "scan.basic_seal.subtitle_fact_rows"
	basicMediaSidecarInsert      stmt.ID = "scan.basic_stage.sidecar_insert"
	basicMediaSidecarRows        stmt.ID = "scan.basic_seal.sidecar_rows"
	basicMediaSubtitleInsert     stmt.ID = "scan.basic_stage.subtitle_insert"
	basicMediaSubtitleRows       stmt.ID = "scan.basic_seal.subtitle_rows"
	basicMediaEvidenceInsert     stmt.ID = "scan.basic_stage.evidence_insert"
	basicMediaEvidenceRows       stmt.ID = "scan.basic_seal.evidence_rows"
)

func basicMediaDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	r := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Real} }
	nt := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text, Nullable: true} }
	defs := []stmt.Definition{
		{ID: basicMediaChapterFactInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_info_new_chapter_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,status,fingerprint) VALUES(?,?,?,?,?,?,?,?) RETURNING input_ordinal,asset_id,revision,size,modified_ns,status,fingerprint`, Params: []stmt.Param{i("change_id"), i("input_ordinal"), t("asset_id"), i("revision"), i("size"), i("modified_ns"), t("status"), t("fingerprint")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "new_chapter_fact", Bindings: []any{int64(51), int64(0), "basic-asset", int64(37), int64(42), int64(-11), "known", "chapters:37"}}}},
		{ID: basicMediaChapterFactRows, Kind: stmt.Read, SQL: `SELECT input_ordinal,asset_id,revision,size,modified_ns,status,fingerprint FROM catalog_info_new_chapter_facts WHERE change_id=? ORDER BY input_ordinal LIMIT ?`, Params: []stmt.Param{i("change_id"), i("row_limit")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_chapter_facts_2"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "complete_chapter_fact", Bindings: []any{int64(51), int64(4097)}}}},
		{ID: basicMediaChapterInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_info_new_chapters(change_id,input_ordinal,asset_id,chapter_index,title,start_seconds,end_seconds) VALUES(?,?,?,?,?,?,?) RETURNING input_ordinal,asset_id,chapter_index,title,start_seconds,end_seconds`, Params: []stmt.Param{i("change_id"), i("input_ordinal"), t("asset_id"), i("chapter_index"), t("title"), r("start_seconds"), r("end_seconds")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "new_chapter", Bindings: []any{int64(51), int64(0), "basic-asset", int64(1), "Opening", float64(0), float64(3)}}}},
		{ID: basicMediaChapterRows, Kind: stmt.Read, SQL: `SELECT input_ordinal,asset_id,chapter_index,title,start_seconds,end_seconds FROM catalog_info_new_chapters WHERE change_id=? ORDER BY input_ordinal LIMIT ?`, Params: []stmt.Param{i("change_id"), i("row_limit")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_chapters_2"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "complete_chapter", Bindings: []any{int64(51), int64(4097)}}}},
		{ID: basicMediaSubtitleFactInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_info_new_subtitle_facts(change_id,input_ordinal,asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(?,?,?,?,?,?,?,?,?,?) RETURNING input_ordinal,asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint`, Params: []stmt.Param{i("change_id"), i("input_ordinal"), t("asset_id"), i("revision"), i("size"), i("modified_ns"), i("origin_us"), i("timing_known"), t("status"), t("fingerprint")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "new_subtitle_fact", Bindings: []any{int64(51), int64(0), "basic-asset", int64(41), int64(42), int64(-11), int64(0), int64(1), "known", "subtitles:41"}}}},
		{ID: basicMediaSubtitleFactRows, Kind: stmt.Read, SQL: `SELECT input_ordinal,asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint FROM catalog_info_new_subtitle_facts WHERE change_id=? ORDER BY input_ordinal LIMIT ?`, Params: []stmt.Param{i("change_id"), i("row_limit")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitle_facts_2"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "complete_subtitle_fact", Bindings: []any{int64(51), int64(4097)}}}},
		{ID: basicMediaSidecarInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_info_new_subtitle_sidecars(change_id,input_ordinal,id,path,size,modified_ns,digest) VALUES(?,?,?,?,?,?,?) RETURNING input_ordinal,id,path,size,modified_ns,digest`, Params: []stmt.Param{i("change_id"), i("input_ordinal"), t("id"), t("path"), i("size"), i("modified_ns"), t("digest")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "new_sidecar", Bindings: []any{int64(51), int64(0), "sidecar-1", "/media/Arrival.srt", int64(30), int64(-7), "sidecar:30"}}}},
		{ID: basicMediaSidecarRows, Kind: stmt.Read, SQL: `SELECT input_ordinal,id,path,size,modified_ns,digest FROM catalog_info_new_subtitle_sidecars WHERE change_id=? ORDER BY input_ordinal LIMIT ?`, Params: []stmt.Param{i("change_id"), i("row_limit")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitle_sidecars_3"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "complete_sidecar", Bindings: []any{int64(51), int64(4097)}}}},
		{ID: basicMediaSubtitleInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_info_new_subtitles(change_id,input_ordinal,id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) RETURNING input_ordinal,id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired`, Params: []stmt.Param{i("change_id"), i("input_ordinal"), t("id"), t("asset_id"), t("origin"), t("locator"), i("stream_index"), nt("sidecar_id"), t("format"), t("language"), t("title"), i("is_default"), i("is_forced"), t("reason"), i("hearing_impaired")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "new_subtitle", Bindings: []any{int64(51), int64(0), "subtitle-1", "basic-asset", "embedded", "2", int64(2), nil, "subrip", "en", "English", int64(0), int64(0), "", int64(0)}}}},
		{ID: basicMediaSubtitleRows, Kind: stmt.Read, SQL: `SELECT input_ordinal,id,asset_id,origin,locator,stream_index,sidecar_id,format,language,title,is_default,is_forced,reason,hearing_impaired FROM catalog_info_new_subtitles WHERE change_id=? ORDER BY input_ordinal LIMIT ?`, Params: []stmt.Param{i("change_id"), i("row_limit")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitles_3"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "complete_subtitle", Bindings: []any{int64(51), int64(4097)}}}},
		{ID: basicMediaEvidenceInsert, Kind: stmt.Write, SQL: `INSERT INTO catalog_info_new_subtitle_inventory_evidence(change_id,input_ordinal,asset_id,revision,evidence) VALUES(?,?,?,?,?) RETURNING input_ordinal,asset_id,revision,evidence`, Params: []stmt.Param{i("change_id"), i("input_ordinal"), t("asset_id"), i("revision"), t("evidence")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "new_evidence", Bindings: []any{int64(51), int64(0), "basic-asset", int64(41), "inventory:41"}}}},
		{ID: basicMediaEvidenceRows, Kind: stmt.Read, SQL: `SELECT input_ordinal,asset_id,revision,evidence FROM catalog_info_new_subtitle_inventory_evidence WHERE change_id=? ORDER BY input_ordinal LIMIT ?`, Params: []stmt.Param{i("change_id"), i("row_limit")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitle_inventory_evidence_2"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "complete_evidence", Bindings: []any{int64(51), int64(4097)}}}},
	}
	defs = append(defs, basicStreamRowDefinitions()...)
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_media_stage.go"}
	}
	return defs
}
func basicMediaRowsID(family int64) stmt.ID {
	switch family {
	case 2:
		return basicMediaStreamRows
	case 3:
		return basicMediaBreadthRows
	case 4:
		return basicMediaChapterFactRows
	case 5:
		return basicMediaChapterRows
	case 6:
		return basicMediaSubtitleFactRows
	case 7:
		return basicMediaSidecarRows
	case 8:
		return basicMediaSubtitleRows
	case 9:
		return basicMediaEvidenceRows
	}
	return ""
}
