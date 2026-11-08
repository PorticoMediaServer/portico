package writer

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

const (
	basicAbandonHeader    stmt.ID = "scan.basic_stage.abandon_header"
	basicAbandonOldRows   stmt.ID = "scan.basic_stage.abandon_old_rows"
	basicAbandonRestore   stmt.ID = "scan.basic_stage.abandon_restore_scalars"
	basicAbandonAsset     stmt.ID = "scan.basic_stage.abandon_clear_asset_fence"
	basicAbandonLink      stmt.ID = "scan.basic_stage.abandon_clear_link_fence"
	basicAbandonShape     stmt.ID = "scan.basic_stage.abandon_release_shape"
	basicAbandonMark      stmt.ID = "scan.basic_stage.abandon_mark"
	basicAbandonCursor    stmt.ID = "scan.basic_stage.abandon_reclaim_cursor"
	basicAbandonDeleteRow stmt.ID = "scan.basic_stage.abandon_delete_header"
)

// One staged table, in foreign-key order: children before parents. base and
// texts reproduce the logical charge its row took at staging, so the refund
// is exact; manifests and the witness are refunded with the header.
type basicAbandonTable struct {
	name, key string
	base      int64
	texts     []string
	index     string // the UNIQUE(change_id,input_ordinal) index; empty when key is the primary key
}

var basicAbandonTables = []basicAbandonTable{
	{"catalog_info_new_subtitles", "input_ordinal", 248, []string{"id", "asset_id", "origin", "locator", "sidecar_id", "format", "language", "title", "reason"}, "sqlite_autoindex_catalog_info_new_subtitles_3"},
	{"catalog_info_new_subtitle_sidecars", "input_ordinal", 184, []string{"id", "path", "digest"}, "sqlite_autoindex_catalog_info_new_subtitle_sidecars_3"},
	{"catalog_info_new_subtitle_inventory_evidence", "input_ordinal", 168, []string{"asset_id", "evidence"}, "sqlite_autoindex_catalog_info_new_subtitle_inventory_evidence_2"},
	{"catalog_info_new_subtitle_facts", "input_ordinal", 208, []string{"asset_id", "status", "fingerprint"}, "sqlite_autoindex_catalog_info_new_subtitle_facts_2"},
	{"catalog_info_new_chapters", "input_ordinal", 184, []string{"asset_id", "title"}, "sqlite_autoindex_catalog_info_new_chapters_2"},
	{"catalog_info_new_chapter_facts", "input_ordinal", 192, []string{"asset_id", "status", "fingerprint"}, "sqlite_autoindex_catalog_info_new_chapter_facts_2"},
	{"catalog_info_new_stream_breadth", "input_ordinal", 280, []string{"asset_id", "audio_variant", "chroma_subsampling", "frame_rate_mode", "interlace_analysis", "dovi_enhancement_type", "dovi_cm_version", "playback_frames_revision", "packet_peak_revision"}, "sqlite_autoindex_catalog_info_new_stream_breadth_2"},
	{"catalog_info_new_streams", "input_ordinal", 248, []string{"asset_id", "type", "codec", "language", "title", "channel_layout", "color_transfer", "color_primaries", "detail_json"}, "sqlite_autoindex_catalog_info_new_streams_2"},
	{"catalog_info_new_stream_facts", "input_ordinal", 192, []string{"asset_id", "fingerprint"}, "sqlite_autoindex_catalog_info_new_stream_facts_2"},
	{"catalog_info_scalar_old", "field", 176, []string{"old_text"}, ""},
	{"catalog_info_input_families", "family", 0, nil, ""},
	{"catalog_info_input_witnesses", "ordinal", 0, nil, ""},
}

const basicAbandonRowsPerTurn = 8

func basicAbandonReadID(n int) stmt.ID {
	return stmt.ID(fmt.Sprintf("scan.basic_stage.abandon_read_%d", n))
}
func basicAbandonDeleteID(n int) stmt.ID {
	return stmt.ID(fmt.Sprintf("scan.basic_stage.abandon_delete_%d", n))
}

func basicAbandonDefinitions() []stmt.Definition {
	i := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Integer} }
	t := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Text} }
	b := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Blob} }
	r := func(n string) stmt.Param { return stmt.Param{Name: n, Type: stmt.Real} }
	fingerprint := bytes.Repeat([]byte{0x5a}, 32)
	tuple := func(prefix string) []stmt.Param {
		return []stmt.Param{t(prefix + "container"), t(prefix + "video_codec"), t(prefix + "audio_codec"), i(prefix + "width"), i(prefix + "height"), r(prefix + "duration"), i(prefix + "playback_revision"), i(prefix + "prepared_revision")}
	}
	restore := append(tuple("next_"), i("asset_id"), t("asset_token"), i("library_id"), i("expected_epoch"))
	restore = append(restore, tuple("expected_")...)
	values := []any{"mkv", "h264", "", int64(0), int64(1080), float64(100), int64(101), int64(103)}
	defs := []stmt.Definition{
		{ID: basicAbandonHeader, Kind: stmt.Read,
			SQL:     `SELECT phase,copy_aux,copy_family,job_revision,target_shape_id,has_after,after_entity FROM catalog_info_changes WHERE id=? AND asset_id=? AND asset_token=? AND slot=0 AND expected_epoch=? AND input_revision=? AND fingerprint=? AND family_mask=511 AND fault='' AND job_lease_owner=0 AND job_lease_generation=0 AND job_lease_expires_ms=0 AND target_owner_object IS NULL AND target_owner_revision IS NULL`,
			Params:  []stmt.Param{i("change_id"), i("asset_id"), t("asset_token"), i("expected_epoch"), i("input_revision"), b("fingerprint")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "unaccepted_first_input", Bindings: []any{int64(51), int64(41), "basic-asset", int64(17), int64(1), fingerprint}}}},
		{ID: basicAbandonOldRows, Kind: stmt.Read,
			SQL:     `SELECT field,kind,old_integer,old_real,old_text FROM catalog_info_scalar_old WHERE change_id=? ORDER BY field LIMIT 7`,
			Params:  []stmt.Param{i("change_id")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_scalar_old"}, Seeks: []string{"change_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "captured_old", Bindings: []any{int64(51)}}}},
		{ID: basicAbandonRestore, Kind: stmt.Write,
			SQL:     `UPDATE catalog_assets SET container=?,video_codec=?,audio_codec=?,width=?,height=?,duration=?,playback_origin_revision=?,prepared_source_revision=? WHERE id=? AND token=? AND library_id=? AND info_epoch=? AND container=? AND video_codec=? AND audio_codec=? AND width=? AND height=? AND duration=? AND playback_origin_revision=? AND prepared_source_revision=? RETURNING id,container,video_codec,audio_codec,width,height,duration,playback_origin_revision,prepared_source_revision`,
			Params:  restore,
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "restore_old_tuple", Bindings: append(append(append([]any{}, values...), int64(41), "basic-asset", int64(7), int64(17)), values...)}}},
		{ID: basicAbandonAsset, Kind: stmt.Write,
			SQL:     `UPDATE catalog_assets SET info_publication_pending=0 WHERE id=? AND token=? AND library_id=? AND info_epoch=? AND info_publication_pending=1 RETURNING id,info_publication_pending`,
			Params:  []stmt.Param{i("asset_id"), t("asset_token"), i("library_id"), i("expected_epoch")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_assets"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "publication_obligation_closes", Bindings: []any{int64(41), "basic-asset", int64(7), int64(17)}}}},
		{ID: basicAbandonLink, Kind: stmt.Write,
			SQL:     `UPDATE catalog_asset_links SET info_pending_change=? WHERE entity_id=? AND asset_id=? AND library_id=? AND info_pending_change=? RETURNING entity_id,asset_id,info_pending_change`,
			Params:  []stmt.Param{{Name: "next_pending_change", Type: stmt.Integer, Nullable: true}, i("entity_id"), i("asset_id"), i("library_id"), i("change_id")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_asset_links"}, Seeks: []string{"entity_id=? AND asset_id=?"}},
			Cohorts: []stmt.Cohort{{Name: "sole_leaf_fence_clears", Bindings: []any{nil, int64(31), int64(41), int64(7), int64(51)}}}},
		{ID: basicAbandonShape, Kind: stmt.Write,
			SQL:     `UPDATE catalog_info_shapes SET ref_count=ref_count-1 WHERE id=? AND ref_count>0 RETURNING id,ref_count`,
			Params:  []stmt.Param{i("shape_id")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_shapes"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "pin_ref_released", Bindings: []any{int64(61)}}}},
		{ID: basicAbandonMark, Kind: stmt.Write,
			SQL:     `UPDATE catalog_info_changes SET copy_aux=-1,copy_family=0,copy_phase=0,copy_id=0,copy_key='',copy_key2='',copy_key3='',job_revision=? WHERE id=? AND asset_id=? AND asset_token=? AND phase=? AND copy_aux>=0 AND job_revision=? RETURNING copy_aux,copy_family,job_revision`,
			Params:  []stmt.Param{i("next_job_revision"), i("change_id"), i("asset_id"), t("asset_token"), i("phase"), i("job_revision")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "marked_abandoned", Bindings: []any{int64(3), int64(51), int64(41), "basic-asset", int64(0), int64(2)}}}},
		{ID: basicAbandonCursor, Kind: stmt.Write,
			SQL:     `UPDATE catalog_info_changes SET copy_family=?,job_revision=? WHERE id=? AND asset_id=? AND asset_token=? AND copy_aux=-1 AND copy_family=? AND job_revision=? RETURNING copy_family,job_revision`,
			Params:  []stmt.Param{i("next_table"), i("next_job_revision"), i("change_id"), i("asset_id"), t("asset_token"), i("table"), i("job_revision")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "reclaim_progress", Bindings: []any{int64(1), int64(4), int64(51), int64(41), "basic-asset", int64(0), int64(3)}}}},
		{ID: basicAbandonDeleteRow, Kind: stmt.Write,
			SQL:     `DELETE FROM catalog_info_changes WHERE id=? AND asset_id=? AND asset_token=? AND copy_aux=-1 AND copy_family=? AND job_revision=? RETURNING id`,
			Params:  []stmt.Param{i("change_id"), i("asset_id"), t("asset_token"), i("table"), i("job_revision")},
			Plan:    stmt.Plan{Hot: true, PrimaryKeys: []string{"catalog_info_changes"}, Seeks: []string{"rowid=?"}},
			Cohorts: []stmt.Cohort{{Name: "header_last", Bindings: []any{int64(51), int64(41), "basic-asset", int64(11), int64(5)}}}},
	}
	for n, table := range basicAbandonTables {
		columns := strings.Join(append([]string{table.key}, table.texts...), ",")
		plan := stmt.Plan{Hot: true, Indexes: []string{table.index}, Seeks: []string{"change_id=?"}}
		deleted := stmt.Plan{Hot: true, Indexes: []string{table.index}, Seeks: []string{"change_id=? AND " + table.key + "=?"}}
		if table.index == "" {
			plan = stmt.Plan{Hot: true, PrimaryKeys: []string{table.name}, Seeks: []string{"change_id=?"}}
			deleted = stmt.Plan{Hot: true, PrimaryKeys: []string{table.name}, Seeks: []string{"change_id=? AND " + table.key + "=?"}}
		}
		defs = append(defs,
			stmt.Definition{ID: basicAbandonReadID(n), Kind: stmt.Read,
				SQL:     "SELECT " + columns + " FROM " + table.name + " WHERE change_id=? ORDER BY " + table.key + " LIMIT ?",
				Params:  []stmt.Param{i("change_id"), i("row_limit")},
				Plan:    plan,
				Cohorts: []stmt.Cohort{{Name: "next_staged_rows", Bindings: []any{int64(51), int64(basicAbandonRowsPerTurn)}}}},
			stmt.Definition{ID: basicAbandonDeleteID(n), Kind: stmt.Write,
				SQL:     "DELETE FROM " + table.name + " WHERE change_id=? AND " + table.key + "=? RETURNING " + table.key,
				Params:  []stmt.Param{i("change_id"), i("key")},
				Plan:    deleted,
				Cohorts: []stmt.Cohort{{Name: "one_staged_row", Bindings: []any{int64(51), int64(0)}}}})
	}
	for n := range defs {
		defs[n].Owner = "store/writer Basic domain"
		defs[n].CallSites = []string{"store/writer/basic_abandon.go"}
	}
	return defs
}

// Upper-bound uses inside ONE abandon turn.
func basicAbandonMaxUses(id stmt.ID) int {
	switch id {
	case basicAbandonHeader, basicAbandonOldRows, basicAbandonRestore, basicAbandonAsset, basicAbandonLink, basicAbandonShape, basicAbandonMark, basicAbandonCursor, basicAbandonDeleteRow:
		return 1
	}
	for n := range basicAbandonTables {
		switch id {
		case basicAbandonReadID(n):
			return 1
		case basicAbandonDeleteID(n):
			return basicAbandonRowsPerTurn
		}
	}
	return 0
}
