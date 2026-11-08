package plan

import (
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// Every RS selected-technical octet projection is an exact point over the same
// accepted asset/link/object/source/stream/chapter/subtitle/disc composite key
// as its payload. These entries add parser and audit evidence only; a receipt
// still requires the actual unanalyzed/analyzed Capture.
const rsTechnicalSourceOctetsPlanSQL = `SELECT octet_length(a.token),octet_length(o.id),octet_length(o.revision),octet_length(s.incarnation),octet_length(l.playback_origin_incarnation),octet_length(ch.status),octet_length(su.status),octet_length(d.source_revision),octet_length(d.root_incarnation),octet_length(d.kind),octet_length(d.packaging),octet_length(d.encryption),octet_length(d.fingerprint),octet_length(d.media_title_key),octet_length(s.kind),octet_length(st.fingerprint),octet_length(ch.fingerprint),octet_length(su.fingerprint),octet_length(a.music_technical_source_revision),octet_length(s.health),octet_length(l.edition) FROM catalog_assets AS a NOT INDEXED CROSS JOIN catalog_asset_links AS l INDEXED BY sqlite_autoindex_catalog_asset_links_1 ON l.asset_id=a.id AND l.library_id=a.library_id CROSS JOIN inventory_objects AS o INDEXED BY sqlite_autoindex_inventory_objects_1 ON o.id=a.info_owner_object AND o.asset_id=a.token CROSS JOIN library_sources AS s INDEXED BY sqlite_autoindex_library_sources_1 ON s.id=o.source_id LEFT JOIN asset_stream_facts AS st INDEXED BY sqlite_autoindex_asset_stream_facts_1 ON st.asset_id=a.token LEFT JOIN asset_chapter_facts AS ch INDEXED BY sqlite_autoindex_asset_chapter_facts_1 ON ch.asset_id=a.token LEFT JOIN asset_subtitle_facts AS su INDEXED BY sqlite_autoindex_asset_subtitle_facts_1 ON su.asset_id=a.token LEFT JOIN inventory_disc_facts AS d INDEXED BY sqlite_autoindex_inventory_disc_facts_1 ON d.object_id=o.id WHERE l.entity_id=? AND l.asset_id=? AND l.library_id=? AND a.id=? AND a.library_id=? AND s.library_id=? AND l.available=1 AND a.available=1 AND a.info_publication_pending=0 AND l.info_pending_change IS NULL AND a.info_owner_revision=o.revision AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND o.size=a.size AND o.modified_ns=a.modified_ns AND o.root_incarnation=s.incarnation AND s.enabled=1 AND s.health NOT IN('root_changed','removing')`

func init() {
	add := func(name, sql, points string, types []stmt.Type, primary, indexes []string) {
		pointOctets.add(octetShape{
			id:         "read.browse." + stmt.ID(name),
			sql:        sql,
			mismatch:   "technical " + name + " requires its exact compiled point",
			definition: rsTechnicalOctetDefinition(types, primary, indexes),
			audit:      auditRSDetailOctetPoint,
			point:      rsTechnicalOctetPoint(name, points),
		})
	}

	add("rs.technical.library_octets", `SELECT octet_length(library_id) FROM catalog_libraries WHERE id=? AND retired=0`,
		"SEARCH catalog_libraries USING INTEGER PRIMARY KEY (rowid=?)",
		[]stmt.Type{stmt.Integer}, []string{"catalog_libraries"}, nil)

	add("rs.technical.source_octets", rsTechnicalSourceOctetsPlanSQL,
		"SEARCH a USING INTEGER PRIMARY KEY (rowid=?)\n"+
			"SEARCH l USING PRIMARY KEY (entity_id=? AND asset_id=?)\n"+
			"SEARCH o USING INDEX sqlite_autoindex_inventory_objects_1 (id=?)\n"+
			"SEARCH s USING INDEX sqlite_autoindex_library_sources_1 (id=?)\n"+
			"SEARCH st USING INDEX sqlite_autoindex_asset_stream_facts_1 (asset_id=?) LEFT-JOIN\n"+
			"SEARCH ch USING INDEX sqlite_autoindex_asset_chapter_facts_1 (asset_id=?) LEFT-JOIN\n"+
			"SEARCH su USING INDEX sqlite_autoindex_asset_subtitle_facts_1 (asset_id=?) LEFT-JOIN\n"+
			"SEARCH d USING INDEX sqlite_autoindex_inventory_disc_facts_1 (object_id=?) LEFT-JOIN",
		[]stmt.Type{stmt.Integer, stmt.Integer, stmt.Integer, stmt.Integer, stmt.Integer, stmt.Text}, []string{"a"},
		[]string{"sqlite_autoindex_catalog_asset_links_1", "sqlite_autoindex_inventory_objects_1", "sqlite_autoindex_library_sources_1", "sqlite_autoindex_asset_stream_facts_1", "sqlite_autoindex_asset_chapter_facts_1", "sqlite_autoindex_asset_subtitle_facts_1", "sqlite_autoindex_inventory_disc_facts_1"})

	add("rs.technical.asset_octets", `SELECT octet_length(container),octet_length(video_codec),octet_length(audio_codec) FROM catalog_assets WHERE id=? AND library_id=?`,
		"SEARCH catalog_assets USING INTEGER PRIMARY KEY (rowid=?)",
		[]stmt.Type{stmt.Integer, stmt.Integer}, []string{"catalog_assets"}, nil)

	add("rs.technical.stream_octets", `SELECT octet_length(type),octet_length(codec),octet_length(language),octet_length(title),octet_length(channel_layout),octet_length(color_transfer),octet_length(color_primaries),octet_length(breadth_audio_variant),octet_length(breadth_chroma_subsampling),octet_length(breadth_frame_rate_mode),octet_length(breadth_interlace_analysis),octet_length(breadth_dovi_enhancement_type),octet_length(breadth_dovi_cm_version),octet_length(breadth_playback_frames_revision),octet_length(breadth_packet_peak_revision),octet_length(detail_json) FROM asset_streams INDEXED BY sqlite_autoindex_asset_streams_1 WHERE asset_id=? AND stream_index=?`,
		"SEARCH asset_streams USING INDEX sqlite_autoindex_asset_streams_1 (asset_id=? AND stream_index=?)",
		[]stmt.Type{stmt.Text, stmt.Integer}, nil, []string{"sqlite_autoindex_asset_streams_1"})

	add("rs.technical.chapter_octets", `SELECT octet_length(title) FROM asset_chapters INDEXED BY sqlite_autoindex_asset_chapters_1 WHERE asset_id=? AND chapter_index=?`,
		"SEARCH asset_chapters USING INDEX sqlite_autoindex_asset_chapters_1 (asset_id=? AND chapter_index=?)",
		[]stmt.Type{stmt.Text, stmt.Integer}, nil, []string{"sqlite_autoindex_asset_chapters_1"})

	add("rs.technical.subtitle_octets", `SELECT octet_length(origin),octet_length(format),octet_length(language),octet_length(title) FROM asset_subtitles INDEXED BY sqlite_autoindex_asset_subtitles_1 WHERE id=? AND asset_id=?`,
		"SEARCH asset_subtitles USING INDEX sqlite_autoindex_asset_subtitles_1 (id=?)",
		[]stmt.Type{stmt.Text, stmt.Text}, nil, []string{"sqlite_autoindex_asset_subtitles_1"})

	add("rs.technical.disctitle_octets", `SELECT octet_length(title_key) FROM inventory_disc_titles INDEXED BY sqlite_autoindex_inventory_disc_titles_3 WHERE object_id=? AND title_number=?`,
		"SEARCH inventory_disc_titles USING INDEX sqlite_autoindex_inventory_disc_titles_3 (object_id=? AND title_number=?)",
		[]stmt.Type{stmt.Text, stmt.Integer}, nil, []string{"sqlite_autoindex_inventory_disc_titles_3"})

	add("rs.technical.media_octets", `SELECT octet_length(title_key) FROM inventory_disc_titles WHERE object_id=? AND title_key=?`,
		"SEARCH inventory_disc_titles USING PRIMARY KEY (object_id=? AND title_key=?)",
		[]stmt.Type{stmt.Text, stmt.Text}, []string{"inventory_disc_titles"}, nil)

	add("rs.technical.path_octets", `SELECT octet_length(path) FROM catalog_assets WHERE id=? AND library_id=?`,
		"SEARCH catalog_assets USING INTEGER PRIMARY KEY (rowid=?)",
		[]stmt.Type{stmt.Integer, stmt.Integer}, []string{"catalog_assets"}, nil)
}

func rsTechnicalOctetDefinition(types []stmt.Type, primary, indexes []string) func(stmt.Definition) bool {
	// The complete live PK/UNIQUE binding set is derived from the same closed
	// list the payload definition uses, so a dropped or reordered key refuses.
	auto := rsTechnicalPlanAutoIndexes(indexes)
	return func(d stmt.Definition) bool {
		if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != len(types) ||
			!equalStrings(d.Plan.PrimaryKeys, primary) || !equalStrings(d.Plan.Indexes, indexes) ||
			len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 ||
			d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" ||
			len(d.Plan.AutoIndexes) != len(auto) {
			return false
		}
		for n, p := range d.Params {
			if p.Nullable || p.Type != types[n] {
				return false
			}
		}
		for n := range auto {
			if d.Plan.AutoIndexes[n].Table != auto[n].Table || d.Plan.AutoIndexes[n].Name != auto[n].Name || d.Plan.AutoIndexes[n].Origin != auto[n].Origin || !equalIndexColumns(d.Plan.AutoIndexes[n].Columns, auto[n].Columns) {
				return false
			}
		}
		return true
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for n := range a {
		if a[n] != b[n] {
			return false
		}
	}
	return true
}

func equalIndexColumns(a, b []stmt.IndexColumn) bool {
	if len(a) != len(b) {
		return false
	}
	for n := range a {
		if a[n] != b[n] {
			return false
		}
	}
	return true
}

func rsTechnicalOctetPoint(name, expected string) func(Rows) error {
	want := strings.Split(expected, "\n")
	return func(r Rows) error {
		if len(r.Values) != len(want) {
			return fmt.Errorf("technical %s: %d plan rows, want %d", name, len(r.Values), len(want))
		}
		for n, row := range r.Values {
			if len(row) != 4 || row[3] != want[n] {
				return fmt.Errorf("technical %s: plan row %d is %v, want %q", name, n, row, want[n])
			}
		}
		return nil
	}
}

// rsTechnicalPlanAutoIndexes restates the closed live PK/UNIQUE bindings the
// read package declares for the same statements. A key that is no longer the
// exact full constraint index refuses rather than silently matching a scan.
func rsTechnicalPlanAutoIndexes(names []string) []stmt.AutoIndexBinding {
	var out []stmt.AutoIndexBinding
	for _, n := range names {
		var table, origin string
		var cols []string
		switch n {
		case "sqlite_autoindex_catalog_asset_links_1":
			table, cols = "catalog_asset_links", []string{"entity_id", "asset_id"}
		case "sqlite_autoindex_inventory_objects_1":
			table, cols = "inventory_objects", []string{"id"}
		case "sqlite_autoindex_library_sources_1":
			table, cols = "library_sources", []string{"id"}
		case "sqlite_autoindex_asset_stream_facts_1":
			table, cols = "asset_stream_facts", []string{"asset_id"}
		case "sqlite_autoindex_asset_chapter_facts_1":
			table, cols = "asset_chapter_facts", []string{"asset_id"}
		case "sqlite_autoindex_asset_subtitle_facts_1":
			table, cols = "asset_subtitle_facts", []string{"asset_id"}
		case "sqlite_autoindex_inventory_disc_facts_1":
			table, cols = "inventory_disc_facts", []string{"object_id"}
		case "sqlite_autoindex_asset_streams_1":
			table, cols = "asset_streams", []string{"asset_id", "stream_index"}
		case "sqlite_autoindex_asset_chapters_1":
			table, cols = "asset_chapters", []string{"asset_id", "chapter_index"}
		case "sqlite_autoindex_asset_subtitles_1":
			table, cols, origin = "asset_subtitles", []string{"id"}, "pk"
		case "sqlite_autoindex_inventory_disc_titles_3":
			table, cols, origin = "inventory_disc_titles", []string{"object_id", "title_number"}, "u"
		default:
			continue
		}
		if origin == "" {
			origin = "pk"
		}
		b := stmt.AutoIndexBinding{Table: table, Name: n, Origin: origin}
		for _, c := range cols {
			b.Columns = append(b.Columns, stmt.IndexColumn{Name: c, Collation: "BINARY"})
		}
		out = append(out, b)
	}
	return out
}
