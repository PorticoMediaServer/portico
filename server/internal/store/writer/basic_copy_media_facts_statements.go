package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicCopyChapterFactScope   stmt.ID = "scan.basic_stage.copy_chapter_fact_scope"
	basicCopyChapterFactStaged  stmt.ID = "scan.basic_stage.copy_chapter_fact_staged"
	basicCopyChapterFactPresent stmt.ID = "scan.basic_stage.copy_chapter_fact_present"
	basicCopyChapterFactInsert  stmt.ID = "scan.basic_stage.copy_chapter_fact_insert"
	basicCopyChapterFactUpdate  stmt.ID = "scan.basic_stage.copy_chapter_fact_update"
)

func basicCopyChapterFactDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicCopyChapterFactScope, Kind: stmt.Read, SQL: `SELECT a.info_publication_pending,l.info_pending_change,a.file_size_revision,a.info_invalidation_revision,l.library_id,e.library_id,e.kind,e.retired,a.info_owner_object,a.info_owner_revision,s.id,s.library_id,s.kind FROM catalog_assets AS a INNER JOIN catalog_asset_links AS l ON l.asset_id=a.id INNER JOIN catalog_entities AS e ON e.id=l.entity_id INNER JOIN catalog_subjects AS s ON s.id=e.id WHERE a.id=? AND l.entity_id=? AND l.asset_id=?`, Params: []stmt.Param{i("asset_id"), i("entity_id"), i("linked_asset_id")},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"a", "l", "e", "s"}, Seeks: []string{"rowid=?", "entity_id=? AND asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "fenced_retained_ordinary_holder", Bindings: []any{int64(41), int64(31), int64(41)}}}},
		{ID: basicCopyChapterFactStaged, Kind: stmt.Read, SQL: `SELECT input_ordinal,asset_id,revision,size,modified_ns,status,fingerprint FROM catalog_info_new_chapter_facts WHERE change_id=? AND input_ordinal=?`, Params: []stmt.Param{i("change_id"), i("input_ordinal")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_chapter_facts_2"}, Seeks: []string{"change_id=? AND input_ordinal=?"}}, Cohorts: []stmt.Cohort{{Name: "chapter_fact_staged", Bindings: []any{int64(51), int64(0)}}}},
		{ID: basicCopyChapterFactPresent, Kind: stmt.Read, SQL: `SELECT 1 FROM asset_chapter_facts WHERE asset_id=?`, Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapter_facts_1"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "chapter_fact_present", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopyChapterFactInsert, Kind: stmt.Write, SQL: `INSERT INTO asset_chapter_facts(asset_id,revision,size,modified_ns,status,fingerprint) VALUES(?,?,?,?,?,?) RETURNING asset_id,revision,size,modified_ns,status,fingerprint`, Params: []stmt.Param{t("asset_token"), i("revision"), i("size"), i("modified_ns"), t("status"), t("fingerprint")}, Plan: stmt.Plan{Hot: true}, Cohorts: []stmt.Cohort{{Name: "chapter_fact_insert", Bindings: []any{"basic-asset", int64(41), int64(42), int64(-11), "known", "subtitles:41"}}}},
		{ID: basicCopyChapterFactUpdate, Kind: stmt.Write, SQL: `UPDATE asset_chapter_facts SET revision=?,size=?,modified_ns=?,status=?,fingerprint=? WHERE asset_id=? AND revision=? AND size=? AND modified_ns=? AND status=? AND fingerprint=? RETURNING asset_id,revision,size,modified_ns,status,fingerprint`, Params: []stmt.Param{i("next_revision"), i("next_size"), i("next_modified_ns"), t("next_status"), t("next_fingerprint"), t("asset_token"), i("revision"), i("size"), i("modified_ns"), t("status"), t("fingerprint")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_chapter_facts_1"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "chapter_fact_update", Bindings: []any{int64(41), int64(42), int64(-11), "known", "subtitles:41", "basic-asset", int64(41), int64(42), int64(-11), "known", "subtitles:41"}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_media_facts.go"}
	}
	return defs
}

const (
	basicCopySubtitleEvidenceScope   stmt.ID = "scan.basic_stage.copy_subtitle_evidence_scope"
	basicCopySubtitleEvidenceStaged  stmt.ID = "scan.basic_stage.copy_subtitle_evidence_staged"
	basicCopySubtitleEvidencePresent stmt.ID = "scan.basic_stage.copy_subtitle_evidence_present"
	basicCopySubtitleEvidenceInsert  stmt.ID = "scan.basic_stage.copy_subtitle_evidence_insert"
	basicCopySubtitleEvidenceUpdate  stmt.ID = "scan.basic_stage.copy_subtitle_evidence_update"
)

func basicCopySubtitleEvidenceDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	defs := []stmt.Definition{
		{ID: basicCopySubtitleEvidenceScope, Kind: stmt.Read, SQL: `SELECT a.info_publication_pending,l.info_pending_change,a.file_size_revision,a.info_invalidation_revision,l.library_id,e.library_id,e.kind,e.retired,a.info_owner_object,a.info_owner_revision,s.id,s.library_id,s.kind FROM catalog_assets AS a INNER JOIN catalog_asset_links AS l ON l.asset_id=a.id INNER JOIN catalog_entities AS e ON e.id=l.entity_id INNER JOIN catalog_subjects AS s ON s.id=e.id WHERE a.id=? AND l.entity_id=? AND l.asset_id=?`, Params: []stmt.Param{i("asset_id"), i("entity_id"), i("linked_asset_id")},
			Plan: stmt.Plan{Hot: true, PrimaryKeys: []string{"a", "l", "e", "s"}, Seeks: []string{"rowid=?", "entity_id=? AND asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "fenced_retained_ordinary_holder", Bindings: []any{int64(41), int64(31), int64(41)}}}},

		{ID: basicCopySubtitleEvidenceStaged, Kind: stmt.Read, SQL: `SELECT input_ordinal,asset_id,revision,evidence FROM catalog_info_new_subtitle_inventory_evidence WHERE change_id=? AND input_ordinal=?`, Params: []stmt.Param{i("change_id"), i("input_ordinal")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitle_inventory_evidence_2"}, Seeks: []string{"change_id=? AND input_ordinal=?"}}, Cohorts: []stmt.Cohort{{Name: "sealed_evidence", Bindings: []any{int64(51), int64(0)}}}},
		{ID: basicCopySubtitleEvidencePresent, Kind: stmt.Read, SQL: `SELECT has_subtitle_inventory_evidence,inventory_revision,inventory_evidence FROM asset_subtitle_facts WHERE asset_id=?`, Params: []stmt.Param{t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitle_facts_1"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "folded_parent", Bindings: []any{"basic-asset"}}}},
		{ID: basicCopySubtitleEvidenceInsert, Kind: stmt.Write, SQL: `UPDATE asset_subtitle_facts SET has_subtitle_inventory_evidence=1,inventory_revision=?,inventory_evidence=? WHERE asset_id=? AND has_subtitle_inventory_evidence=0 AND inventory_revision IS NULL AND inventory_evidence IS NULL RETURNING asset_id,has_subtitle_inventory_evidence,inventory_revision,inventory_evidence`, Params: []stmt.Param{i("revision"), t("evidence"), t("asset_token")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitle_facts_1"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "absent_evidence", Bindings: []any{int64(41), "inventory:41", "basic-asset"}}}},
		{ID: basicCopySubtitleEvidenceUpdate, Kind: stmt.Write, SQL: `UPDATE asset_subtitle_facts SET has_subtitle_inventory_evidence=1,inventory_revision=?,inventory_evidence=? WHERE asset_id=? AND has_subtitle_inventory_evidence=1 AND inventory_revision=? AND inventory_evidence=? RETURNING asset_id,has_subtitle_inventory_evidence,inventory_revision,inventory_evidence`, Params: []stmt.Param{i("next_revision"), t("next_evidence"), t("asset_token"), i("revision"), t("evidence")}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_asset_subtitle_facts_1"}, Seeks: []string{"asset_id=?"}}, Cohorts: []stmt.Cohort{{Name: "present_evidence", Bindings: []any{int64(41), "inventory:41", "basic-asset", int64(23), "old"}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_copy_media_facts.go"}
	}
	return defs
}
func basicCopyMediaFactsDefinitions() []stmt.Definition {
	return append(basicCopyChapterFactDefinitions(), basicCopySubtitleEvidenceDefinitions()...)
}
