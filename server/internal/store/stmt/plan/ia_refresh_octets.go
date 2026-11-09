package plan

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"reflect"
	"strings"
)

type iaRefreshOctetPoint struct {
	id                      stmt.ID
	sql, table, index, seek string
	keys, texts             []string
	params                  []stmt.Param
}

// Fourteen exact C019 points; no domain-wide SQL or function exemption. Capture
// will audit these only after the actual adopted C schema embeds.
var iaRefreshOctetPoints = []iaRefreshOctetPoint{
	{id: "ia.refresh.row1.bytes", sql: `SELECT octet_length(asset_id),octet_length(fingerprint) FROM asset_stream_facts WHERE asset_id=?`, table: "asset_stream_facts", index: "sqlite_autoindex_asset_stream_facts_1", seek: "asset_id=?", keys: []string{"asset_id"}, texts: []string{"asset_id", "fingerprint"}, params: []stmt.Param{{Name: "asset_id", Type: stmt.Text}}},
	{id: "ia.refresh.row1.old_bytes", sql: `SELECT octet_length(asset_id),octet_length(fingerprint) FROM catalog_info_undo_asset_stream_facts WHERE refresh_id=? AND asset_id=?`, table: "catalog_info_undo_asset_stream_facts", index: "", seek: "refresh_id=? AND asset_id=?", keys: []string{"refresh_id", "asset_id"}, texts: []string{"asset_id", "fingerprint"}, params: []stmt.Param{{Name: "refresh", Type: stmt.Integer}, {Name: "asset_id", Type: stmt.Text}}},
	{id: "ia.refresh.row2.bytes", sql: `SELECT octet_length(asset_id),octet_length(type),octet_length(codec),octet_length(language),octet_length(title),octet_length(channel_layout),octet_length(color_transfer),octet_length(color_primaries),octet_length(detail_json),octet_length(breadth_audio_variant),octet_length(breadth_chroma_subsampling),octet_length(breadth_frame_rate_mode),octet_length(breadth_interlace_analysis),octet_length(breadth_dovi_enhancement_type),octet_length(breadth_dovi_cm_version),octet_length(breadth_playback_frames_revision),octet_length(breadth_packet_peak_revision) FROM asset_streams WHERE asset_id=? AND stream_index=?`, table: "asset_streams", index: "sqlite_autoindex_asset_streams_1", seek: "asset_id=? AND stream_index=?", keys: []string{"asset_id", "stream_index"}, texts: []string{"asset_id", "type", "codec", "language", "title", "channel_layout", "color_transfer", "color_primaries", "detail_json", "breadth_audio_variant", "breadth_chroma_subsampling", "breadth_frame_rate_mode", "breadth_interlace_analysis", "breadth_dovi_enhancement_type", "breadth_dovi_cm_version", "breadth_playback_frames_revision", "breadth_packet_peak_revision"}, params: []stmt.Param{{Name: "asset_id", Type: stmt.Text}, {Name: "stream_index", Type: stmt.Integer}}},
	{id: "ia.refresh.row2.old_bytes", sql: `SELECT octet_length(asset_id),octet_length(type),octet_length(codec),octet_length(language),octet_length(title),octet_length(channel_layout),octet_length(color_transfer),octet_length(color_primaries),octet_length(detail_json),octet_length(breadth_audio_variant),octet_length(breadth_chroma_subsampling),octet_length(breadth_frame_rate_mode),octet_length(breadth_interlace_analysis),octet_length(breadth_dovi_enhancement_type),octet_length(breadth_dovi_cm_version),octet_length(breadth_playback_frames_revision),octet_length(breadth_packet_peak_revision) FROM catalog_info_undo_asset_streams WHERE refresh_id=? AND asset_id=? AND stream_index=?`, table: "catalog_info_undo_asset_streams", index: "", seek: "refresh_id=? AND asset_id=? AND stream_index=?", keys: []string{"refresh_id", "asset_id", "stream_index"}, texts: []string{"asset_id", "type", "codec", "language", "title", "channel_layout", "color_transfer", "color_primaries", "detail_json", "breadth_audio_variant", "breadth_chroma_subsampling", "breadth_frame_rate_mode", "breadth_interlace_analysis", "breadth_dovi_enhancement_type", "breadth_dovi_cm_version", "breadth_playback_frames_revision", "breadth_packet_peak_revision"}, params: []stmt.Param{{Name: "refresh", Type: stmt.Integer}, {Name: "asset_id", Type: stmt.Text}, {Name: "stream_index", Type: stmt.Integer}}},
	{id: "ia.refresh.row3.bytes", sql: `SELECT octet_length(asset_id),octet_length(status),octet_length(fingerprint) FROM asset_chapter_facts WHERE asset_id=?`, table: "asset_chapter_facts", index: "sqlite_autoindex_asset_chapter_facts_1", seek: "asset_id=?", keys: []string{"asset_id"}, texts: []string{"asset_id", "status", "fingerprint"}, params: []stmt.Param{{Name: "asset_id", Type: stmt.Text}}},
	{id: "ia.refresh.row3.old_bytes", sql: `SELECT octet_length(asset_id),octet_length(status),octet_length(fingerprint) FROM catalog_info_undo_asset_chapter_facts WHERE refresh_id=? AND asset_id=?`, table: "catalog_info_undo_asset_chapter_facts", index: "", seek: "refresh_id=? AND asset_id=?", keys: []string{"refresh_id", "asset_id"}, texts: []string{"asset_id", "status", "fingerprint"}, params: []stmt.Param{{Name: "refresh", Type: stmt.Integer}, {Name: "asset_id", Type: stmt.Text}}},
	{id: "ia.refresh.row4.bytes", sql: `SELECT octet_length(asset_id),octet_length(title) FROM asset_chapters WHERE asset_id=? AND chapter_index=?`, table: "asset_chapters", index: "sqlite_autoindex_asset_chapters_1", seek: "asset_id=? AND chapter_index=?", keys: []string{"asset_id", "chapter_index"}, texts: []string{"asset_id", "title"}, params: []stmt.Param{{Name: "asset_id", Type: stmt.Text}, {Name: "chapter_index", Type: stmt.Integer}}},
	{id: "ia.refresh.row4.old_bytes", sql: `SELECT octet_length(asset_id),octet_length(title) FROM catalog_info_undo_asset_chapters WHERE refresh_id=? AND asset_id=? AND chapter_index=?`, table: "catalog_info_undo_asset_chapters", index: "", seek: "refresh_id=? AND asset_id=? AND chapter_index=?", keys: []string{"refresh_id", "asset_id", "chapter_index"}, texts: []string{"asset_id", "title"}, params: []stmt.Param{{Name: "refresh", Type: stmt.Integer}, {Name: "asset_id", Type: stmt.Text}, {Name: "chapter_index", Type: stmt.Integer}}},
	{id: "ia.refresh.row5.bytes", sql: `SELECT octet_length(asset_id),octet_length(status),octet_length(fingerprint),octet_length(inventory_evidence) FROM asset_subtitle_facts WHERE asset_id=?`, table: "asset_subtitle_facts", index: "sqlite_autoindex_asset_subtitle_facts_1", seek: "asset_id=?", keys: []string{"asset_id"}, texts: []string{"asset_id", "status", "fingerprint", "inventory_evidence"}, params: []stmt.Param{{Name: "asset_id", Type: stmt.Text}}},
	{id: "ia.refresh.row5.old_bytes", sql: `SELECT octet_length(asset_id),octet_length(status),octet_length(fingerprint),octet_length(inventory_evidence) FROM catalog_info_undo_asset_subtitle_facts WHERE refresh_id=? AND asset_id=?`, table: "catalog_info_undo_asset_subtitle_facts", index: "", seek: "refresh_id=? AND asset_id=?", keys: []string{"refresh_id", "asset_id"}, texts: []string{"asset_id", "status", "fingerprint", "inventory_evidence"}, params: []stmt.Param{{Name: "refresh", Type: stmt.Integer}, {Name: "asset_id", Type: stmt.Text}}},
	{id: "ia.refresh.row6.bytes", sql: `SELECT octet_length(id),octet_length(path),octet_length(digest) FROM subtitle_sidecars WHERE id=?`, table: "subtitle_sidecars", index: "sqlite_autoindex_subtitle_sidecars_1", seek: "id=?", keys: []string{"id"}, texts: []string{"id", "path", "digest"}, params: []stmt.Param{{Name: "id", Type: stmt.Text}}},
	{id: "ia.refresh.row6.old_bytes", sql: `SELECT octet_length(id),octet_length(path),octet_length(digest) FROM catalog_info_undo_subtitle_sidecars WHERE refresh_id=? AND id=?`, table: "catalog_info_undo_subtitle_sidecars", index: "", seek: "refresh_id=? AND id=?", keys: []string{"refresh_id", "id"}, texts: []string{"id", "path", "digest"}, params: []stmt.Param{{Name: "refresh", Type: stmt.Integer}, {Name: "id", Type: stmt.Text}}},
	{id: "ia.refresh.row7.bytes", sql: `SELECT octet_length(id),octet_length(asset_id),octet_length(origin),octet_length(locator),octet_length(sidecar_id),octet_length(format),octet_length(language),octet_length(title),octet_length(reason) FROM asset_subtitles WHERE id=?`, table: "asset_subtitles", index: "sqlite_autoindex_asset_subtitles_1", seek: "id=?", keys: []string{"id"}, texts: []string{"id", "asset_id", "origin", "locator", "sidecar_id", "format", "language", "title", "reason"}, params: []stmt.Param{{Name: "id", Type: stmt.Text}}},
	{id: "ia.refresh.row7.old_bytes", sql: `SELECT octet_length(id),octet_length(asset_id),octet_length(origin),octet_length(locator),octet_length(sidecar_id),octet_length(format),octet_length(language),octet_length(title),octet_length(reason) FROM catalog_info_undo_asset_subtitles WHERE refresh_id=? AND id=?`, table: "catalog_info_undo_asset_subtitles", index: "", seek: "refresh_id=? AND id=?", keys: []string{"refresh_id", "id"}, texts: []string{"id", "asset_id", "origin", "locator", "sidecar_id", "format", "language", "title", "reason"}, params: []stmt.Param{{Name: "refresh", Type: stmt.Integer}, {Name: "id", Type: stmt.Text}}},
}

func init() {
	for _, v := range iaRefreshOctetPoints {
		v := v
		pointOctets.add(octetShape{v.id, v.sql, "IA refresh bytes require the exact closed native key", func(d stmt.Definition) bool {
			want := stmt.Plan{Hot: true}
			if v.index == "" {
				want.PrimaryKeys = []string{v.table}
			} else {
				want.Indexes = []string{v.index}
			}
			return d.ID == v.id && d.SQL == v.sql && d.Kind == stmt.Read && reflect.DeepEqual(d.Params, v.params) && reflect.DeepEqual(d.Plan, want)
		}, func(ctx context.Context, c *sql.Conn) error { return auditIARefreshOctets(ctx, c, v) }, func(eqp Rows) error {
			kind := "INDEX " + v.index
			if v.index == "" {
				kind = "PRIMARY KEY"
			}
			want := "SEARCH " + v.table + " USING " + kind + " (" + v.seek + ")"
			if len(eqp.Values) != 1 || len(eqp.Values[0]) != 4 || eqp.Values[0][3] != want {
				return fmt.Errorf("IA refresh requires exact SEARCH: %s", want)
			}
			return nil
		}})
	}
}
func auditIARefreshOctets(ctx context.Context, c *sql.Conn, v iaRefreshOctetPoint) error {
	f, err := query(ctx, c, "PRAGMA function_list")
	if err != nil {
		return err
	}
	native := 0
	for _, r := range f.Values {
		if len(r) != 6 {
			return fmt.Errorf("IA refresh: native function metadata")
		}
		n, ok := r[0].(string)
		if ok && strings.EqualFold(n, "octet_length") {
			if r[1] != int64(1) || r[4] != int64(1) {
				return fmt.Errorf("IA refresh: shadowed octet_length")
			}
			native++
		}
	}
	if native != 1 {
		return fmt.Errorf("IA refresh: missing native octet_length")
	}
	rows, err := query(ctx, c, "PRAGMA main.table_xinfo("+v.table+")")
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	keys := 0
	for _, r := range rows.Values {
		if len(r) != 7 {
			return fmt.Errorf("IA refresh: native columns")
		}
		name, ok := r[1].(string)
		if !ok {
			return fmt.Errorf("IA refresh: native column name")
		}
		for _, n := range v.texts {
			if name == n {
				if r[2] != "TEXT" || r[6] != int64(0) {
					return fmt.Errorf("IA refresh: stored TEXT required")
				}
				seen[n] = true
			}
		}
		ordinal, ok := r[5].(int64)
		if !ok {
			return fmt.Errorf("IA refresh: primary key ordinal")
		}
		if ordinal > 0 {
			wantType := "TEXT"
			if name == "refresh_id" || name == "stream_index" || name == "chapter_index" {
				wantType = "INTEGER"
			}
			if r[2] != wantType {
				return fmt.Errorf("IA refresh: native key type")
			}

			if ordinal > int64(len(v.keys)) || name != v.keys[ordinal-1] || r[6] != int64(0) {
				return fmt.Errorf("IA refresh: exact primary key required")
			}
			keys++
		}
	}
	if len(seen) != len(v.texts) || keys != len(v.keys) {
		return fmt.Errorf("IA refresh: incomplete native point shape")
	}
	return nil
}
