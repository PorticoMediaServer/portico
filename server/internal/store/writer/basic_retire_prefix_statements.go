package writer

import "github.com/PorticoMediaServer/portico/server/internal/store/stmt"

const (
	basicRetireMediaKeys2 stmt.ID = "scan.basic_retire.media_keys_2"
	basicRetireMediaKeys3 stmt.ID = "scan.basic_retire.media_keys_3"
	basicRetireMediaKeys4 stmt.ID = "scan.basic_retire.media_keys_4"
	basicRetireMediaKeys5 stmt.ID = "scan.basic_retire.media_keys_5"
	basicRetireMediaKeys6 stmt.ID = "scan.basic_retire.media_keys_6"
	basicRetireMediaKeys7 stmt.ID = "scan.basic_retire.media_keys_7"
	basicRetireMediaKeys8 stmt.ID = "scan.basic_retire.media_keys_8"
	basicRetireMediaKeys9 stmt.ID = "scan.basic_retire.media_keys_9"
)

func basicRetirePrefixDefinitions() []stmt.Definition {
	defs := []stmt.Definition{
		{ID: basicRetireMediaKeys2, Kind: stmt.Read, SQL: `SELECT input_ordinal FROM catalog_info_new_streams WHERE change_id=? ORDER BY input_ordinal LIMIT ?`, Params: []stmt.Param{{Name: "change_id", Type: stmt.Integer}, {Name: "row_limit", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_streams_2"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "retirement_head", Bindings: []any{int64(51), int64(9)}}}},
		{ID: basicRetireMediaKeys3, Kind: stmt.Read, SQL: `SELECT input_ordinal FROM catalog_info_new_stream_breadth WHERE change_id=? ORDER BY input_ordinal LIMIT ?`, Params: []stmt.Param{{Name: "change_id", Type: stmt.Integer}, {Name: "row_limit", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_stream_breadth_2"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "retirement_head", Bindings: []any{int64(51), int64(9)}}}},
		{ID: basicRetireMediaKeys4, Kind: stmt.Read, SQL: `SELECT input_ordinal FROM catalog_info_new_chapter_facts WHERE change_id=? ORDER BY input_ordinal LIMIT ?`, Params: []stmt.Param{{Name: "change_id", Type: stmt.Integer}, {Name: "row_limit", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_chapter_facts_2"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "retirement_head", Bindings: []any{int64(51), int64(9)}}}},
		{ID: basicRetireMediaKeys5, Kind: stmt.Read, SQL: `SELECT input_ordinal FROM catalog_info_new_chapters WHERE change_id=? ORDER BY input_ordinal LIMIT ?`, Params: []stmt.Param{{Name: "change_id", Type: stmt.Integer}, {Name: "row_limit", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_chapters_2"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "retirement_head", Bindings: []any{int64(51), int64(9)}}}},
		{ID: basicRetireMediaKeys6, Kind: stmt.Read, SQL: `SELECT input_ordinal FROM catalog_info_new_subtitle_facts WHERE change_id=? ORDER BY input_ordinal LIMIT ?`, Params: []stmt.Param{{Name: "change_id", Type: stmt.Integer}, {Name: "row_limit", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitle_facts_2"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "retirement_head", Bindings: []any{int64(51), int64(9)}}}},
		{ID: basicRetireMediaKeys7, Kind: stmt.Read, SQL: `SELECT input_ordinal FROM catalog_info_new_subtitle_sidecars WHERE change_id=? ORDER BY input_ordinal LIMIT ?`, Params: []stmt.Param{{Name: "change_id", Type: stmt.Integer}, {Name: "row_limit", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitle_sidecars_2"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "retirement_head", Bindings: []any{int64(51), int64(9)}}}},
		{ID: basicRetireMediaKeys8, Kind: stmt.Read, SQL: `SELECT input_ordinal FROM catalog_info_new_subtitles WHERE change_id=? ORDER BY input_ordinal LIMIT ?`, Params: []stmt.Param{{Name: "change_id", Type: stmt.Integer}, {Name: "row_limit", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitles_2"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "retirement_head", Bindings: []any{int64(51), int64(9)}}}},
		{ID: basicRetireMediaKeys9, Kind: stmt.Read, SQL: `SELECT input_ordinal FROM catalog_info_new_subtitle_inventory_evidence WHERE change_id=? ORDER BY input_ordinal LIMIT ?`, Params: []stmt.Param{{Name: "change_id", Type: stmt.Integer}, {Name: "row_limit", Type: stmt.Integer}}, Plan: stmt.Plan{Hot: true, Indexes: []string{"sqlite_autoindex_catalog_info_new_subtitle_inventory_evidence_2"}, Seeks: []string{"change_id=?"}}, Cohorts: []stmt.Cohort{{Name: "retirement_head", Bindings: []any{int64(51), int64(9)}}}},
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_retire_live.go"}
	}
	return defs
}

func basicRetirePrefixKeysID(family int64) stmt.ID {
	switch family {
	case 2:
		return basicRetireMediaKeys2
	case 3:
		return basicRetireMediaKeys3
	case 4:
		return basicRetireMediaKeys4
	case 5:
		return basicRetireMediaKeys5
	case 6:
		return basicRetireMediaKeys6
	case 7:
		return basicRetireMediaKeys7
	case 8:
		return basicRetireMediaKeys8
	case 9:
		return basicRetireMediaKeys9
	}
	return ""
}

func basicRetirePrefixMaxUses(id stmt.ID) int {
	switch id {
	case basicRetireMediaKeys2, basicRetireMediaKeys3, basicRetireMediaKeys4, basicRetireMediaKeys5, basicRetireMediaKeys6, basicRetireMediaKeys7, basicRetireMediaKeys8, basicRetireMediaKeys9:
		return 1
	}
	return 0
}
