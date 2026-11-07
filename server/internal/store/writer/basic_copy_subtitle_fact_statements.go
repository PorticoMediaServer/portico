package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCopySubtitleFactScope   stmt.ID = "scan.basic_stage.copy_subtitle_fact_scope"
	basicCopySubtitleFactStaged  stmt.ID = "scan.basic_stage.copy_subtitle_fact_staged"
	basicCopySubtitleFactPresent stmt.ID = "scan.basic_stage.copy_subtitle_fact_present"
	basicCopySubtitleFactInsert  stmt.ID = "scan.basic_stage.copy_subtitle_fact_insert"
	basicCopySubtitleFactUpdate  stmt.ID = "scan.basic_stage.copy_subtitle_fact_update"
)

func basicCopySubtitleFactDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicCopySubtitleFactScope, Kind: stmt.Read, SQL: `SELECT a.info_publication_pending,l.info_pending_change,a.file_size_revision,a.info_invalidation_revision,l.library_id,e.library_id,e.kind,e.retired,a.info_owner_object,a.info_owner_revision,s.id,s.library_id,s.kind FROM catalog_assets AS a INNER JOIN catalog_asset_links AS l ON l.asset_id=a.id INNER JOIN catalog_entities AS e ON e.id=l.entity_id INNER JOIN catalog_subjects AS s ON s.id=e.id WHERE a.id=? AND l.entity_id=? AND l.asset_id=?`, Params: []stmt.Param{i("asset_id"), i("entity_id"), i("linked_asset_id")},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"a", "l", "e", "s"}, Seeks: []string{"rowid=?", "entity_id=? AND asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "fenced_retained_ordinary_holder", Bindings: []any{int64(41), int64(31), int64(41)}}}},
		{ID: basicCopySubtitleFactStaged, Kind: stmt.Read, SQL: `SELECT input_ordinal,asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint FROM catalog_info_new_subtitle_facts WHERE change_id=? AND input_ordinal=?`, Params: []stmt.Param{i("change_id"), i("input_ordinal")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitle_facts_2"}, Seeks: []string{"change_id=? AND input_ordinal=?"}}, Cohorts: []stmt.Cohort{{Name: "subtitle_fact_staged", Bindings: []any{int64(51), int64(0)}}}},
		{ID: basicCopySubtitleFactPresent, Kind: stmt.Read, SQL: `SELECT 1 FROM asset_subtitle_facts WHERE asset_id=?`, Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitle_facts_1"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "subtitle_fact_present", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopySubtitleFactInsert, Kind: stmt.Write, SQL: `INSERT INTO asset_subtitle_facts(asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint) VALUES(?,?,?,?,?,?,?,?) RETURNING asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint`, Params: []stmt.Param{t("asset_token"), i("revision"), i("size"), i("modified_ns"), i("origin_us"), i("timing_known"), t("status"), t("fingerprint")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "subtitle_fact_insert", Bindings: []any{"basic-asset", int64(41), int64(42), int64(-11), int64(0), int64(1), "known", "subtitles:41"}}}},
		{ID: basicCopySubtitleFactUpdate, Kind: stmt.Write, SQL: `UPDATE asset_subtitle_facts SET revision=?,size=?,modified_ns=?,origin_us=?,timing_known=?,status=?,fingerprint=? WHERE asset_id=? AND revision=? AND size=? AND modified_ns=? AND origin_us=? AND timing_known=? AND status=? AND fingerprint=? RETURNING asset_id,revision,size,modified_ns,origin_us,timing_known,status,fingerprint`, Params: []stmt.Param{i("next_revision"), i("next_size"), i("next_modified_ns"), i("next_origin_us"), i("next_timing_known"), t("next_status"), t("next_fingerprint"), t("asset_token"), i("revision"), i("size"), i("modified_ns"), i("origin_us"), i("timing_known"), t("status"), t("fingerprint")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitle_facts_1"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "subtitle_fact_update", Bindings: []any{int64(41), int64(42), int64(-11), int64(0), int64(1), "known", "subtitles:41", "basic-asset", int64(41), int64(42), int64(-11), int64(0), int64(1), "known", "subtitles:41"}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_subtitle_fact.go"}
	}
	return defs
}
