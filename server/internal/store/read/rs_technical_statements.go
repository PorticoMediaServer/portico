package read

import (
	"encoding/json"

	"github.com/PorticoMediaServer/portico/server/internal/store/stmt"
)

// RSTechnicalAsset is the accepted scalar file frame for the selected asset.
// Music depth/rate/lossless fields are nullable because a scan may not have
// measured them; a nil pointer is "not observed", never zero-as-fact.
type RSTechnicalAsset struct {
	Size       int64
	ModifiedNS int64
	Container  string
	VideoCodec string
	AudioCodec string
	Width      int64
	Height     int64
	Duration   float64
	SampleRate *int64
	BitDepth   *int64
	Channels   *int64
	Lossless   *int64
}

func rsTechnicalDecodeAsset(v []any) RSTechnicalAsset {
	return RSTechnicalAsset{
		Size:       v[0].(int64),
		ModifiedNS: v[1].(int64),
		Container:  v[2].(string),
		VideoCodec: v[3].(string),
		AudioCodec: v[4].(string),
		Width:      v[5].(int64),
		Height:     v[6].(int64),
		Duration:   rsTechnicalFloat(v[7]),
		SampleRate: rsTechnicalInt(v[8]),
		BitDepth:   rsTechnicalInt(v[9]),
		Channels:   rsTechnicalInt(v[10]),
		Lossless:   rsTechnicalInt(v[11]),
	}
}

// rsTechnicalStreamDetailVersion is the exact stored-detail decoder revision.
// It must equal assets.StreamDetailVersion. The read package deliberately does
// not import internal/assets: that package links the owner's SQL functions
// (through internal/dbwork), which would change the closed function environment
// every read plan golden pins. An owner version bump therefore reports "stale"
// here until the two constants are updated together.
const rsTechnicalStreamDetailVersion = 5

// RSTechnicalStreamDetail mirrors the owner's stored planning detail exactly.
// StreamBreadth is deliberately absent: it is stored in scalar relational
// columns, never in detail_json.
type RSTechnicalStreamDetail struct {
	MaxBitRate               int64   `json:"maxBitRate,omitempty"`
	HDR10Plus                bool    `json:"hdr10Plus,omitempty"`
	Profile                  string  `json:"profile,omitempty"`
	Level                    int     `json:"level,omitempty"`
	PixelFormat              string  `json:"pixelFormat,omitempty"`
	BitDepth                 int     `json:"bitDepth,omitempty"`
	Width                    int     `json:"width,omitempty"`
	Height                   int     `json:"height,omitempty"`
	FrameRate                float64 `json:"frameRate,omitempty"`
	Interlaced               bool    `json:"interlaced,omitempty"`
	Rotation                 int     `json:"rotation,omitempty"`
	SampleAspectRatio        string  `json:"sampleAspectRatio,omitempty"`
	Anamorphic               bool    `json:"anamorphic,omitempty"`
	RefFrames                int     `json:"refFrames,omitempty"`
	CodecTag                 string  `json:"codecTag,omitempty"`
	BitRate                  int64   `json:"bitRate,omitempty"`
	SampleRate               int     `json:"sampleRate,omitempty"`
	BitsPerSample            int     `json:"bitsPerSample,omitempty"`
	DynamicRange             string  `json:"dynamicRange,omitempty"`
	DolbyVisionProfile       int     `json:"dolbyVisionProfile,omitempty"`
	DolbyVisionLevel         int     `json:"dolbyVisionLevel,omitempty"`
	DolbyVisionCompatibility int     `json:"dolbyVisionCompatibility,omitempty"`
	EnhancementLayer         bool    `json:"enhancementLayer,omitempty"`
	MasterDisplay            string  `json:"masterDisplay,omitempty"`
	MaxCLL                   string  `json:"maxCll,omitempty"`
	ObjectAudio              string  `json:"objectAudio,omitempty"`
}

// RSTechnicalStream is one selected source stream. DetailState is separate from
// the basic stream facts: the versioned stored detail is only exposed when the
// row's detail_version is the exact decoder revision this build understands.
type RSTechnicalStream struct {
	Detail                      *RSTechnicalStreamDetail
	HearingImpaired, Commentary *bool
	DetailState                 string

	Index                  int64
	Kind                   string
	Codec                  string
	Language               string
	Title                  string
	Channels               int64
	ChannelLayout          string
	Default                int64
	Forced                 int64
	ColorTransfer          string
	ColorPrimaries         string
	HasBreadth             int64
	AudioDescription       *int64
	AudioVariant           *string
	ChromaSubsampling      *string
	FpsNumerator           *int64
	FpsDenominator         *int64
	FrameRateMode          *string
	InterlaceAnalysis      *string
	Telecine               *int64
	DoviRpu                *int64
	DoviBaseLayer          *int64
	DoviMetadataKnown      *int64
	DoviEnhancementType    *string
	DoviCmVersion          *string
	PlaybackFramesRevision *string
	PacketPeakRevision     *string
}

func rsTechnicalDecodeStream(v []any) RSTechnicalStream {
	out := RSTechnicalStream{
		Index:                  v[0].(int64),
		Kind:                   v[1].(string),
		Codec:                  v[2].(string),
		Language:               v[3].(string),
		Title:                  v[4].(string),
		Channels:               v[5].(int64),
		ChannelLayout:          v[6].(string),
		Default:                v[7].(int64),
		Forced:                 v[8].(int64),
		ColorTransfer:          v[9].(string),
		ColorPrimaries:         v[10].(string),
		HasBreadth:             v[11].(int64),
		AudioDescription:       rsTechnicalInt(v[12]),
		AudioVariant:           rsTechnicalText(v[13]),
		ChromaSubsampling:      rsTechnicalText(v[14]),
		FpsNumerator:           rsTechnicalInt(v[15]),
		FpsDenominator:         rsTechnicalInt(v[16]),
		FrameRateMode:          rsTechnicalText(v[17]),
		InterlaceAnalysis:      rsTechnicalText(v[18]),
		Telecine:               rsTechnicalInt(v[19]),
		DoviRpu:                rsTechnicalInt(v[20]),
		DoviBaseLayer:          rsTechnicalInt(v[21]),
		DoviMetadataKnown:      rsTechnicalInt(v[22]),
		DoviEnhancementType:    rsTechnicalText(v[23]),
		DoviCmVersion:          rsTechnicalText(v[24]),
		PlaybackFramesRevision: rsTechnicalText(v[25]),
		PacketPeakRevision:     rsTechnicalText(v[26]),
	}
	// Breadth lives in scalar relational columns, not in detail_json. An absent
	// breadth row must not read as observed false/empty.
	if out.HasBreadth == 0 {
		out.AudioDescription = nil
		out.AudioVariant = nil
		out.ChromaSubsampling = nil
		out.FpsNumerator = nil
		out.FpsDenominator = nil
		out.FrameRateMode = nil
		out.InterlaceAnalysis = nil
		out.Telecine = nil
		out.DoviRpu = nil
		out.DoviBaseLayer = nil
		out.DoviMetadataKnown = nil
		out.DoviEnhancementType = nil
		out.DoviCmVersion = nil
		out.PlaybackFramesRevision = nil
		out.PacketPeakRevision = nil
	}
	return out
}

type RSTechnicalChapter struct {
	Index int64
	Title string
	Start float64
	End   float64
}

func rsTechnicalDecodeChapter(v []any) RSTechnicalChapter {
	return RSTechnicalChapter{
		Index: v[0].(int64),
		Title: v[1].(string),
		Start: rsTechnicalFloat(v[2]),
		End:   rsTechnicalFloat(v[3]),
	}
}

type RSTechnicalSubtitle struct {
	Origin          string
	Index           int64
	Format          string
	Language        string
	Title           string
	Default         int64
	Forced          int64
	HearingImpaired int64
}

func rsTechnicalDecodeSubtitle(v []any) RSTechnicalSubtitle {
	return RSTechnicalSubtitle{
		Origin:          v[0].(string),
		Index:           v[1].(int64),
		Format:          v[2].(string),
		Language:        v[3].(string),
		Title:           v[4].(string),
		Default:         v[5].(int64),
		Forced:          v[6].(int64),
		HearingImpaired: v[7].(int64),
	}
}

type RSTechnicalDisctitle struct {
	Number     int64
	Order      int64
	DurationMS int64
	Angles     int64
	key        string
}

func rsTechnicalDecodeDisctitle(v []any) RSTechnicalDisctitle {
	return RSTechnicalDisctitle{
		Number:     v[0].(int64),
		Order:      v[1].(int64),
		DurationMS: v[2].(int64),
		Angles:     v[3].(int64),
		key:        v[4].(string),
	}
}

// rsTechnicalStreamDetail decodes one stored stream detail row. A value longer
// than the writer cap or malformed JSON is refused rather than guessed.
func rsTechnicalStreamDetail(raw string) (*RSTechnicalStreamDetail, *bool, *bool, error) {
	if raw == "" {
		return nil, nil, nil, nil
	}
	if len(raw) > 4096 {
		return nil, nil, nil, ErrUnqualified
	}
	var d struct {
		RSTechnicalStreamDetail
		HearingImpaired bool `json:"hearingImpaired"`
		Commentary      bool `json:"commentary"`
	}
	if json.Unmarshal([]byte(raw), &d) != nil {
		return nil, nil, nil, ErrUnqualified
	}
	v, h, c := d.RSTechnicalStreamDetail, d.HearingImpaired, d.Commentary
	return &v, &h, &c, nil
}

type rsTechnicalSQL struct {
	name, sql        string
	types            []stmt.Type
	columns          []int
	lengths          string
	primary, indexes []string
}

// Every statement is an exact point (or a bounded one-row-per-key inventory) on
// an existing asset/link/object/source/stream/chapter/subtitle/disc composite
// key. No probe, filesystem opening, playback grant or inferred authority is
// expressed here.
const rsTechnicalSourceSQL = `SELECT a.token,o.id,o.revision,s.incarnation,s.generation,a.size,a.modified_ns,a.info_epoch,a.info_invalidation_revision,l.playback_origin_incarnation,l.playback_origin_revision,st.revision,st.size,st.modified_ns,st.detail_version,ch.revision,ch.size,ch.modified_ns,ch.status,su.revision,su.size,su.modified_ns,su.status,d.source_revision,d.source_generation,d.root_incarnation,d.kind,d.packaging,d.encryption,d.fingerprint,d.media_title_key,d.media_stream_revision,d.media_chapter_revision,s.kind,st.fingerprint,ch.fingerprint,su.fingerprint,a.music_technical_source_revision,s.health,l.part_index,l.edition,l.start_seconds,l.end_seconds,l.info_shape_id FROM catalog_assets AS a NOT INDEXED CROSS JOIN catalog_asset_links AS l INDEXED BY sqlite_autoindex_catalog_asset_links_1 ON l.asset_id=a.id AND l.library_id=a.library_id CROSS JOIN inventory_objects AS o INDEXED BY sqlite_autoindex_inventory_objects_1 ON o.id=a.info_owner_object AND o.asset_id=a.token CROSS JOIN library_sources AS s INDEXED BY sqlite_autoindex_library_sources_1 ON s.id=o.source_id LEFT JOIN asset_stream_facts AS st INDEXED BY sqlite_autoindex_asset_stream_facts_1 ON st.asset_id=a.token LEFT JOIN asset_chapter_facts AS ch INDEXED BY sqlite_autoindex_asset_chapter_facts_1 ON ch.asset_id=a.token LEFT JOIN asset_subtitle_facts AS su INDEXED BY sqlite_autoindex_asset_subtitle_facts_1 ON su.asset_id=a.token LEFT JOIN inventory_disc_facts AS d INDEXED BY sqlite_autoindex_inventory_disc_facts_1 ON d.object_id=o.id WHERE l.entity_id=? AND l.asset_id=? AND l.library_id=? AND a.id=? AND a.library_id=? AND s.library_id=? AND l.available=1 AND a.available=1 AND a.info_publication_pending=0 AND l.info_pending_change IS NULL AND a.info_owner_revision=o.revision AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND o.size=a.size AND o.modified_ns=a.modified_ns AND o.root_incarnation=s.incarnation AND s.enabled=1 AND s.health NOT IN('root_changed','removing')`

const rsTechnicalSourceOctetsSQL = `SELECT octet_length(a.token),octet_length(o.id),octet_length(o.revision),octet_length(s.incarnation),octet_length(l.playback_origin_incarnation),octet_length(ch.status),octet_length(su.status),octet_length(d.source_revision),octet_length(d.root_incarnation),octet_length(d.kind),octet_length(d.packaging),octet_length(d.encryption),octet_length(d.fingerprint),octet_length(d.media_title_key),octet_length(s.kind),octet_length(st.fingerprint),octet_length(ch.fingerprint),octet_length(su.fingerprint),octet_length(a.music_technical_source_revision),octet_length(s.health),octet_length(l.edition) FROM catalog_assets AS a NOT INDEXED CROSS JOIN catalog_asset_links AS l INDEXED BY sqlite_autoindex_catalog_asset_links_1 ON l.asset_id=a.id AND l.library_id=a.library_id CROSS JOIN inventory_objects AS o INDEXED BY sqlite_autoindex_inventory_objects_1 ON o.id=a.info_owner_object AND o.asset_id=a.token CROSS JOIN library_sources AS s INDEXED BY sqlite_autoindex_library_sources_1 ON s.id=o.source_id LEFT JOIN asset_stream_facts AS st INDEXED BY sqlite_autoindex_asset_stream_facts_1 ON st.asset_id=a.token LEFT JOIN asset_chapter_facts AS ch INDEXED BY sqlite_autoindex_asset_chapter_facts_1 ON ch.asset_id=a.token LEFT JOIN asset_subtitle_facts AS su INDEXED BY sqlite_autoindex_asset_subtitle_facts_1 ON su.asset_id=a.token LEFT JOIN inventory_disc_facts AS d INDEXED BY sqlite_autoindex_inventory_disc_facts_1 ON d.object_id=o.id WHERE l.entity_id=? AND l.asset_id=? AND l.library_id=? AND a.id=? AND a.library_id=? AND s.library_id=? AND l.available=1 AND a.available=1 AND a.info_publication_pending=0 AND l.info_pending_change IS NULL AND a.info_owner_revision=o.revision AND o.retired=0 AND o.state='available' AND o.unsupported_reason='' AND o.size=a.size AND o.modified_ns=a.modified_ns AND o.root_incarnation=s.incarnation AND s.enabled=1 AND s.health NOT IN('root_changed','removing')`

var rsTechnicalSQLs = []rsTechnicalSQL{
	{name: "source", sql: rsTechnicalSourceSQL, types: []stmt.Type{stmt.Integer, stmt.Integer, stmt.Integer, stmt.Integer, stmt.Integer, stmt.Text}, columns: []int{0, 1, 2, 3, 9, 18, 22, 23, 25, 26, 27, 28, 29, 30, 33, 34, 35, 36, 37, 38, 40}, lengths: rsTechnicalSourceOctetsSQL, primary: []string{"a"}, indexes: []string{"sqlite_autoindex_catalog_asset_links_1", "sqlite_autoindex_inventory_objects_1", "sqlite_autoindex_library_sources_1", "sqlite_autoindex_asset_stream_facts_1", "sqlite_autoindex_asset_chapter_facts_1", "sqlite_autoindex_asset_subtitle_facts_1", "sqlite_autoindex_inventory_disc_facts_1"}},
	{name: "asset", sql: `SELECT size,modified_ns,container,video_codec,audio_codec,width,height,duration,music_sample_rate,music_bit_depth,music_channels,music_lossless FROM catalog_assets WHERE id=? AND library_id=?`, types: []stmt.Type{stmt.Integer, stmt.Integer}, columns: []int{2, 3, 4}, lengths: `SELECT octet_length(container),octet_length(video_codec),octet_length(audio_codec) FROM catalog_assets WHERE id=? AND library_id=?`, primary: []string{"catalog_assets"}, indexes: []string{}},
	{name: "stream", sql: `SELECT stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,color_primaries,has_asset_stream_breadth,breadth_audio_description,breadth_audio_variant,breadth_chroma_subsampling,breadth_fps_numerator,breadth_fps_denominator,breadth_frame_rate_mode,breadth_interlace_analysis,breadth_telecine,breadth_dovi_rpu,breadth_dovi_base_layer,breadth_dovi_metadata_known,breadth_dovi_enhancement_type,breadth_dovi_cm_version,breadth_playback_frames_revision,breadth_packet_peak_revision,detail_json FROM asset_streams INDEXED BY sqlite_autoindex_asset_streams_1 WHERE asset_id=? AND stream_index=?`, types: []stmt.Type{stmt.Text, stmt.Integer}, columns: []int{1, 2, 3, 4, 6, 9, 10, 13, 14, 17, 18, 23, 24, 25, 26, 27}, lengths: `SELECT octet_length(type),octet_length(codec),octet_length(language),octet_length(title),octet_length(channel_layout),octet_length(color_transfer),octet_length(color_primaries),octet_length(breadth_audio_variant),octet_length(breadth_chroma_subsampling),octet_length(breadth_frame_rate_mode),octet_length(breadth_interlace_analysis),octet_length(breadth_dovi_enhancement_type),octet_length(breadth_dovi_cm_version),octet_length(breadth_playback_frames_revision),octet_length(breadth_packet_peak_revision),octet_length(detail_json) FROM asset_streams INDEXED BY sqlite_autoindex_asset_streams_1 WHERE asset_id=? AND stream_index=?`, indexes: []string{"sqlite_autoindex_asset_streams_1"}},
	{name: "stream_keys", sql: `SELECT stream_index FROM asset_streams INDEXED BY sqlite_autoindex_asset_streams_1 WHERE asset_id=? ORDER BY stream_index LIMIT ?`, types: []stmt.Type{stmt.Text, stmt.Integer}, indexes: []string{"sqlite_autoindex_asset_streams_1"}},
	{name: "chapter", sql: `SELECT chapter_index,title,start_seconds,end_seconds FROM asset_chapters INDEXED BY sqlite_autoindex_asset_chapters_1 WHERE asset_id=? AND chapter_index=?`, types: []stmt.Type{stmt.Text, stmt.Integer}, columns: []int{1}, lengths: `SELECT octet_length(title) FROM asset_chapters INDEXED BY sqlite_autoindex_asset_chapters_1 WHERE asset_id=? AND chapter_index=?`, indexes: []string{"sqlite_autoindex_asset_chapters_1"}},
	{name: "chapter_keys", sql: `SELECT chapter_index FROM asset_chapters INDEXED BY sqlite_autoindex_asset_chapters_1 WHERE asset_id=? ORDER BY chapter_index LIMIT ?`, types: []stmt.Type{stmt.Text, stmt.Integer}, indexes: []string{"sqlite_autoindex_asset_chapters_1"}},
	// Subtitles are keyed by their stored text primary key, not an implicit
	// rowid: the key survives a vacuum between preparation windows.
	{name: "subtitle", sql: `SELECT origin,stream_index,format,language,title,is_default,is_forced,hearing_impaired FROM asset_subtitles INDEXED BY sqlite_autoindex_asset_subtitles_1 WHERE id=? AND asset_id=?`, types: []stmt.Type{stmt.Text, stmt.Text}, columns: []int{0, 2, 3, 4}, lengths: `SELECT octet_length(origin),octet_length(format),octet_length(language),octet_length(title) FROM asset_subtitles INDEXED BY sqlite_autoindex_asset_subtitles_1 WHERE id=? AND asset_id=?`, indexes: []string{"sqlite_autoindex_asset_subtitles_1"}},
	{name: "subtitle_keys", sql: `SELECT id FROM asset_subtitles INDEXED BY asset_subtitles_asset WHERE asset_id=? ORDER BY id LIMIT ?`, types: []stmt.Type{stmt.Text, stmt.Integer}, indexes: []string{"asset_subtitles_asset"}},
	{name: "disctitle", sql: `SELECT title_number,disc_order,duration_ms,angle_count,title_key FROM inventory_disc_titles INDEXED BY sqlite_autoindex_inventory_disc_titles_3 WHERE object_id=? AND title_number=?`, types: []stmt.Type{stmt.Text, stmt.Integer}, columns: []int{4}, lengths: `SELECT octet_length(title_key) FROM inventory_disc_titles INDEXED BY sqlite_autoindex_inventory_disc_titles_3 WHERE object_id=? AND title_number=?`, indexes: []string{"sqlite_autoindex_inventory_disc_titles_3"}},
	{name: "media", sql: `SELECT title_number,disc_order,duration_ms,angle_count,title_key FROM inventory_disc_titles WHERE object_id=? AND title_key=?`, types: []stmt.Type{stmt.Text, stmt.Text}, columns: []int{4}, lengths: `SELECT octet_length(title_key) FROM inventory_disc_titles WHERE object_id=? AND title_key=?`, primary: []string{"inventory_disc_titles"}},
	{name: "disctitle_keys", sql: `SELECT title_number FROM inventory_disc_titles INDEXED BY sqlite_autoindex_inventory_disc_titles_3 WHERE object_id=? ORDER BY title_number LIMIT ?`, types: []stmt.Type{stmt.Text, stmt.Integer}, indexes: []string{"sqlite_autoindex_inventory_disc_titles_3"}},
	{name: "observations", sql: `SELECT 1 FROM inventory_objects INDEXED BY inventory_objects_reading_asset_object WHERE asset_id=? AND retired=0 AND state='available' LIMIT 2`, types: []stmt.Type{stmt.Text}, indexes: []string{"inventory_objects_reading_asset_object"}},
	{name: "discchapters", sql: `SELECT chapter_order FROM inventory_disc_chapters WHERE object_id=? AND title_key=? ORDER BY chapter_order LIMIT ?`, types: []stmt.Type{stmt.Text, stmt.Text, stmt.Integer}, primary: []string{"inventory_disc_chapters"}},
	{name: "path", sql: `SELECT path FROM catalog_assets WHERE id=? AND library_id=?`, types: []stmt.Type{stmt.Integer, stmt.Integer}, columns: []int{0}, lengths: `SELECT octet_length(path) FROM catalog_assets WHERE id=? AND library_id=?`, primary: []string{"catalog_assets"}},
	{name: "library", sql: `SELECT library_id FROM catalog_libraries WHERE id=? AND retired=0`, types: []stmt.Type{stmt.Integer}, columns: []int{0}, lengths: `SELECT octet_length(library_id) FROM catalog_libraries WHERE id=? AND retired=0`, primary: []string{"catalog_libraries"}},
}

func init() {
	for _, s := range rsTechnicalSQLs {
		if len(s.columns) > 0 {
			registerRSBytePoint("rs.technical."+s.name, rsBytePoint{lengths: "rs.technical." + s.name + "_octets", columns: s.columns})
		}
	}
}

// rsTechnicalCohorts binds one real populated and one real empty cohort per
// statement. Empty is a valid point that matches no row; a populated cohort must
// return at least one row in the technical fixture.
func rsTechnicalCohorts(name string) []stmt.Cohort {
	switch name {
	case "source":
		return []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1), int64(1), int64(1), int64(1), "movies"}}, {Name: "empty", Bindings: []any{int64(999999), int64(1), int64(1), int64(999999), int64(1), "movies"}}}
	case "asset", "path":
		return []stmt.Cohort{{Name: "present", Bindings: []any{int64(1), int64(1)}}, {Name: "absent", Bindings: []any{int64(999999), int64(1)}}}
	case "library":
		return []stmt.Cohort{{Name: "present", Bindings: []any{int64(1)}}, {Name: "absent", Bindings: []any{int64(999999)}}}
	case "observations":
		return []stmt.Cohort{{Name: "present", Bindings: []any{"asset"}}, {Name: "empty", Bindings: []any{"missing-asset"}}}
	case "stream", "chapter":
		return []stmt.Cohort{{Name: "present", Bindings: []any{"asset", int64(0)}}, {Name: "absent", Bindings: []any{"asset", int64(99)}}}
	case "stream_keys", "chapter_keys":
		return []stmt.Cohort{{Name: "present", Bindings: []any{"asset", int64(rsTechnicalRows + 1)}}, {Name: "absent", Bindings: []any{"missing-asset", int64(rsTechnicalRows + 1)}}}
	case "subtitle":
		return []stmt.Cohort{{Name: "present", Bindings: []any{"tech-subtitle", "asset"}}, {Name: "absent", Bindings: []any{"missing-subtitle", "asset"}}}
	case "subtitle_keys":
		return []stmt.Cohort{{Name: "present", Bindings: []any{"asset", int64(rsTechnicalRows + 1)}}, {Name: "absent", Bindings: []any{"missing-asset", int64(rsTechnicalRows + 1)}}}
	case "disctitle":
		return []stmt.Cohort{{Name: "present", Bindings: []any{"tech-object", int64(1)}}, {Name: "absent", Bindings: []any{"tech-object", int64(99)}}}
	case "media":
		return []stmt.Cohort{{Name: "present", Bindings: []any{"tech-object", "pl:1"}}, {Name: "absent", Bindings: []any{"tech-object", "pl:missing"}}}
	case "disctitle_keys":
		return []stmt.Cohort{{Name: "present", Bindings: []any{"tech-object", int64(rsTechnicalRows + 1)}}, {Name: "absent", Bindings: []any{"missing-object", int64(rsTechnicalRows + 1)}}}
	case "discchapters":
		return []stmt.Cohort{{Name: "present", Bindings: []any{"tech-object", "pl:1", int64(rsTechnicalRows + 1)}}, {Name: "absent", Bindings: []any{"tech-object", "pl:missing", int64(rsTechnicalRows + 1)}}}
	}
	panic("plan: no technical cohort for " + name)
}

// TechnicalFactsDefinitions is the inert, owner-registered inventory for this
// read family. It is never wired into a route or a default browse recipe.
func TechnicalFactsDefinitions() []stmt.Definition {
	var out []stmt.Definition
	for _, s := range rsTechnicalSQLs {
		for pass := 0; pass < 2; pass++ {
			if pass == 1 && s.lengths == "" {
				continue
			}
			name, query := s.name, s.sql
			if pass == 1 {
				name += "_octets"
				query = s.lengths
			}
			params := []stmt.Param{}
			for n, t := range s.types {
				params = append(params, stmt.Param{Name: string(rune('a' + n)), Type: t})
			}
			out = append(out, stmt.Definition{ID: readStatementID("rs.technical." + name), SQL: query, Kind: stmt.Read, Owner: "store/read", CallSites: []string{"store/read/rs_technical.go:RSSelectedTechnicalReader.Poll"}, Params: params, Cohorts: rsTechnicalCohorts(s.name), Plan: stmt.Plan{Hot: true, PrimaryKeys: s.primary, Indexes: s.indexes, AutoIndexes: rsTechnicalAutoIndexes(s.indexes)}})
		}
	}
	return out
}

func rsTechnicalAutoIndexes(names []string) []stmt.AutoIndexBinding {
	var out []stmt.AutoIndexBinding
	for _, n := range names {
		var table, origin string
		var cols []string
		switch n {
		case "sqlite_autoindex_catalog_asset_links_1":
			table = "catalog_asset_links"
			cols = []string{"entity_id", "asset_id"}
		case "sqlite_autoindex_inventory_objects_1":
			table = "inventory_objects"
			cols = []string{"id"}
		case "sqlite_autoindex_library_sources_1":
			table = "library_sources"
			cols = []string{"id"}
		case "sqlite_autoindex_asset_stream_facts_1":
			table = "asset_stream_facts"
			cols = []string{"asset_id"}
		case "sqlite_autoindex_asset_chapter_facts_1":
			table = "asset_chapter_facts"
			cols = []string{"asset_id"}
		case "sqlite_autoindex_asset_subtitle_facts_1":
			table = "asset_subtitle_facts"
			cols = []string{"asset_id"}
		case "sqlite_autoindex_inventory_disc_facts_1":
			table = "inventory_disc_facts"
			cols = []string{"object_id"}
		case "sqlite_autoindex_asset_streams_1":
			table = "asset_streams"
			cols = []string{"asset_id", "stream_index"}
		case "sqlite_autoindex_asset_chapters_1":
			table = "asset_chapters"
			cols = []string{"asset_id", "chapter_index"}
		case "sqlite_autoindex_asset_subtitles_1":
			table = "asset_subtitles"
			cols = []string{"id"}
			origin = "pk"
		case "sqlite_autoindex_inventory_disc_titles_3":
			table = "inventory_disc_titles"
			cols = []string{"object_id", "title_number"}
			origin = "u"
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
