package plan

import (
	"fmt"
	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
	"slices"
	"strings"
)

func init() {
	pointOctets.add(octetShape{"read.browse.rs.technical.library_octets", `SELECT octet_length(library_id) FROM catalog_libraries WHERE id=? AND retired=0`, "selected library identity", rsTechnicalOctetDefinition([]stmt.Type{stmt.Integer}, []string{"catalog_libraries"}, nil), auditRSDetailOctetPoint, checkRSTechnicalLibraryPoint})

	pointOctets.add(octetShape{"read.browse.rs.technical.source_octets", `SELECT octet_length(a.token),octet_length(o.id),octet_length(o.revision),octet_length(s.incarnation),octet_length(l.playback_origin_incarnation),octet_length(ch.status),octet_length(su.status),octet_length(d.source_revision),octet_length(d.root_incarnation),octet_length(d.kind),octet_length(d.packaging),octet_length(d.encryption),octet_length(d.fingerprint),octet_length(d.media_title_key),octet_length(s.kind),octet_length(st.fingerprint),octet_length(ch.fingerprint),octet_length(su.fingerprint),octet_length(a.music_technical_source_revision),octet_length(s.health),octet_length(l.edition) FROM catalog_assets AS a NOT INDEXED CROSS JOIN catalog_asset_links AS l INDEXED BY sqlite_autoindex_catalog_asset_links_1 ON l.asset_id=a.id AND l.library_id=a.library_id CROSS JOIN inventory_objects AS o INDEXED BY sqlite_autoindex_inventory_objects_1 ON o.id=a.info_owner_object AND o.asset_id=a.token CROSS JOIN library_sources AS s INDEXED BY sqlite_autoindex_library_sources_1 ON s.id=o.source_id LEFT JOIN asset_stream_facts AS st INDEXED BY sqlite_autoindex_asset_stream_facts_1 ON st.asset_id=a.token LEFT JOIN asset_chapter_facts AS ch INDEXED BY sqlite_autoindex_asset_chapter_facts_1 ON ch.asset_id=a.token LEFT JOIN asset_subtitle_facts AS su INDEXED BY sqlite_autoindex_asset_subtitle_facts_1 ON su.asset_id=a.token LEFT JOIN inventory_disc_facts AS d INDEXED BY sqlite_autoindex_inventory_disc_facts_1 ON d.object_id=o.id WHERE l.entity_id=? AND l.asset_id=? AND l.library_id=? AND a.id=? AND a.library_id=? AND s.library_id=? AND l.available=1 AND a.available=1 AND a.info_publication_pending=0 AND l.info_pending_change IS NULL AND a.info_owner_revision=o.revision AND o.retired=0 AND o.state='available' AND o.size=a.size AND o.modified_ns=a.modified_ns AND o.root_incarnation=s.incarnation AND s.enabled=1 AND s.health NOT IN('root_changed','removing')`, "selected technical exact point", rsTechnicalOctetDefinition([]stmt.Type{stmt.Integer, stmt.Integer, stmt.Integer, stmt.Integer, stmt.Integer, stmt.Text}, []string{"a"}, []string{"sqlite_autoindex_catalog_asset_links_1", "sqlite_autoindex_inventory_objects_1", "sqlite_autoindex_library_sources_1", "sqlite_autoindex_asset_stream_facts_1", "sqlite_autoindex_asset_chapter_facts_1", "sqlite_autoindex_asset_subtitle_facts_1", "sqlite_autoindex_inventory_disc_facts_1"}), auditRSDetailOctetPoint, func(r Rows) error {
		for _, v := range r.Values {
			s, ok := v[3].(string)
			if !ok || !strings.HasPrefix(s, "SEARCH ") {
				return fmt.Errorf("technical point requires SEARCH: %v", v)
			}
		}
		return nil
	}})
	pointOctets.add(octetShape{"read.browse.rs.technical.asset_octets", `SELECT octet_length(container),octet_length(video_codec),octet_length(audio_codec) FROM catalog_assets WHERE id=? AND library_id=?`, "selected technical exact point", rsTechnicalOctetDefinition([]stmt.Type{stmt.Integer, stmt.Integer}, []string{"catalog_assets"}, []string{}), auditRSDetailOctetPoint, func(r Rows) error {
		for _, v := range r.Values {
			s, ok := v[3].(string)
			if !ok || !strings.HasPrefix(s, "SEARCH ") {
				return fmt.Errorf("technical point requires SEARCH: %v", v)
			}
		}
		return nil
	}})
	pointOctets.add(octetShape{"read.browse.rs.technical.stream_octets", `SELECT octet_length(type),octet_length(codec),octet_length(language),octet_length(title),octet_length(channel_layout),octet_length(color_transfer),octet_length(color_primaries),octet_length(breadth_audio_variant),octet_length(breadth_chroma_subsampling),octet_length(breadth_frame_rate_mode),octet_length(breadth_interlace_analysis),octet_length(breadth_dovi_enhancement_type),octet_length(breadth_dovi_cm_version),octet_length(breadth_playback_frames_revision),octet_length(breadth_packet_peak_revision),octet_length(detail_json) FROM asset_streams INDEXED BY sqlite_autoindex_asset_streams_1 WHERE asset_id=? AND stream_index=?`, "selected technical exact point", rsTechnicalOctetDefinition([]stmt.Type{stmt.Text, stmt.Integer}, []string{}, []string{"sqlite_autoindex_asset_streams_1"}), auditRSDetailOctetPoint, func(r Rows) error {
		for _, v := range r.Values {
			s, ok := v[3].(string)
			if !ok || !strings.HasPrefix(s, "SEARCH ") {
				return fmt.Errorf("technical point requires SEARCH: %v", v)
			}
		}
		return nil
	}})
	pointOctets.add(octetShape{"read.browse.rs.technical.chapter_octets", `SELECT octet_length(title) FROM asset_chapters INDEXED BY sqlite_autoindex_asset_chapters_1 WHERE asset_id=? AND chapter_index=?`, "selected technical exact point", rsTechnicalOctetDefinition([]stmt.Type{stmt.Text, stmt.Integer}, []string{}, []string{"sqlite_autoindex_asset_chapters_1"}), auditRSDetailOctetPoint, func(r Rows) error {
		for _, v := range r.Values {
			s, ok := v[3].(string)
			if !ok || !strings.HasPrefix(s, "SEARCH ") {
				return fmt.Errorf("technical point requires SEARCH: %v", v)
			}
		}
		return nil
	}})
	pointOctets.add(octetShape{"read.browse.rs.technical.subtitle_octets", `SELECT octet_length(origin),octet_length(format),octet_length(language),octet_length(title) FROM asset_subtitles WHERE rowid=? AND asset_id=?`, "selected technical exact point", rsTechnicalOctetDefinition([]stmt.Type{stmt.Integer, stmt.Text}, []string{"asset_subtitles"}, []string{}), auditRSDetailOctetPoint, func(r Rows) error {
		for _, v := range r.Values {
			s, ok := v[3].(string)
			if !ok || !strings.HasPrefix(s, "SEARCH ") {
				return fmt.Errorf("technical point requires SEARCH: %v", v)
			}
		}
		return nil
	}})
	pointOctets.add(octetShape{"read.browse.rs.technical.disctitle_octets", `SELECT octet_length(title_key) FROM inventory_disc_titles INDEXED BY sqlite_autoindex_inventory_disc_titles_3 WHERE object_id=? AND title_number=?`, "selected technical exact point", rsTechnicalOctetDefinition([]stmt.Type{stmt.Text, stmt.Integer}, []string{}, []string{"sqlite_autoindex_inventory_disc_titles_3"}), auditRSDetailOctetPoint, func(r Rows) error {
		for _, v := range r.Values {
			s, ok := v[3].(string)
			if !ok || !strings.HasPrefix(s, "SEARCH ") {
				return fmt.Errorf("technical point requires SEARCH: %v", v)
			}
		}
		return nil
	}})
	pointOctets.add(octetShape{"read.browse.rs.technical.media_octets", `SELECT octet_length(title_key) FROM inventory_disc_titles WHERE object_id=? AND title_key=?`, "selected technical exact point", rsTechnicalOctetDefinition([]stmt.Type{stmt.Text, stmt.Text}, []string{"inventory_disc_titles"}, []string{}), auditRSDetailOctetPoint, func(r Rows) error {
		for _, v := range r.Values {
			s, ok := v[3].(string)
			if !ok || !strings.HasPrefix(s, "SEARCH ") {
				return fmt.Errorf("technical point requires SEARCH: %v", v)
			}
		}
		return nil
	}})
	pointOctets.add(octetShape{"read.browse.rs.technical.path_octets", `SELECT octet_length(path) FROM catalog_assets WHERE id=? AND library_id=?`, "selected technical exact point", rsTechnicalOctetDefinition([]stmt.Type{stmt.Integer, stmt.Integer}, []string{"catalog_assets"}, []string{}), auditRSDetailOctetPoint, func(r Rows) error {
		for _, v := range r.Values {
			s, ok := v[3].(string)
			if !ok || !strings.HasPrefix(s, "SEARCH ") {
				return fmt.Errorf("technical point requires SEARCH: %v", v)
			}
		}
		return nil
	}})
}

func rsTechnicalOctetDefinition(types []stmt.Type, primary, indexes []string) func(stmt.Definition) bool {
	return func(d stmt.Definition) bool {
		if d.Kind != stmt.Read || !d.Plan.Hot || len(d.Params) != len(types) || !slices.Equal(d.Plan.PrimaryKeys, primary) || !slices.Equal(d.Plan.Indexes, indexes) || len(d.Plan.Waivers) != 0 || len(d.Plan.AllowedScans) != 0 || d.Plan.NativeInsert != "" || d.Plan.NativeRetained != "" {
			return false
		}
		for n, p := range d.Params {
			if p.Nullable || p.Type != types[n] {
				return false
			}
		}
		return true
	}
}

func checkRSTechnicalLibraryPoint(r Rows) error {
	if len(r.Values) != 1 || r.Values[0][3] != "SEARCH catalog_libraries USING INTEGER PRIMARY KEY (rowid=?)" {
		return fmt.Errorf("library point requires exact SEARCH")
	}
	return nil
}
