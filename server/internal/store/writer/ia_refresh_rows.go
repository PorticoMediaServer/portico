package writer

// C019's seven complete row shapes. Values are private typed SQLite projections,
// not caller SQL or a map of optional fields. The typed records below are the
// only producers of replacement rows. IA21/22 own their bounded source walks.
type iaRefreshColumn struct {
	name, kind string
	nullable   bool
}
type iaRefreshShape struct {
	table   string
	columns []iaRefreshColumn
	keys    int
}

var iaRefreshShapes = [...]iaRefreshShape{
	{},
	{"asset_stream_facts", []iaRefreshColumn{{"asset_id", "TEXT", false}, {"revision", "INTEGER", false}, {"size", "INTEGER", false}, {"modified_ns", "INTEGER", false}, {"fingerprint", "TEXT", false}, {"detail_version", "INTEGER", false}}, 1},
	{"asset_streams", []iaRefreshColumn{{"asset_id", "TEXT", false}, {"stream_index", "INTEGER", false}, {"type", "TEXT", false}, {"codec", "TEXT", false}, {"language", "TEXT", false}, {"title", "TEXT", false}, {"channels", "INTEGER", false}, {"channel_layout", "TEXT", false}, {"is_default", "INTEGER", false}, {"is_forced", "INTEGER", false}, {"color_transfer", "TEXT", false}, {"color_primaries", "TEXT", false}, {"detail_json", "TEXT", false}, {"has_asset_stream_breadth", "INTEGER", false}, {"breadth_audio_description", "INTEGER", true}, {"breadth_audio_variant", "TEXT", true}, {"breadth_chroma_subsampling", "TEXT", true}, {"breadth_fps_numerator", "INTEGER", true}, {"breadth_fps_denominator", "INTEGER", true}, {"breadth_frame_rate_mode", "TEXT", true}, {"breadth_interlace_analysis", "TEXT", true}, {"breadth_telecine", "INTEGER", true}, {"breadth_dovi_rpu", "INTEGER", true}, {"breadth_dovi_base_layer", "INTEGER", true}, {"breadth_dovi_metadata_known", "INTEGER", true}, {"breadth_dovi_enhancement_type", "TEXT", true}, {"breadth_dovi_cm_version", "TEXT", true}, {"breadth_playback_frames_revision", "TEXT", true}, {"breadth_packet_peak_revision", "TEXT", true}}, 2},
	{"asset_chapter_facts", []iaRefreshColumn{{"asset_id", "TEXT", false}, {"revision", "INTEGER", false}, {"size", "INTEGER", false}, {"modified_ns", "INTEGER", false}, {"status", "TEXT", false}, {"fingerprint", "TEXT", false}}, 1},
	{"asset_chapters", []iaRefreshColumn{{"asset_id", "TEXT", false}, {"chapter_index", "INTEGER", false}, {"title", "TEXT", false}, {"start_seconds", "REAL", false}, {"end_seconds", "REAL", false}}, 2},
	{"asset_subtitle_facts", []iaRefreshColumn{{"asset_id", "TEXT", false}, {"revision", "INTEGER", false}, {"size", "INTEGER", false}, {"modified_ns", "INTEGER", false}, {"origin_us", "INTEGER", false}, {"timing_known", "INTEGER", false}, {"status", "TEXT", false}, {"fingerprint", "TEXT", false}, {"has_subtitle_inventory_evidence", "INTEGER", false}, {"inventory_revision", "INTEGER", true}, {"inventory_evidence", "TEXT", true}}, 1},
	{"subtitle_sidecars", []iaRefreshColumn{{"id", "TEXT", false}, {"path", "TEXT", false}, {"size", "INTEGER", false}, {"modified_ns", "INTEGER", false}, {"digest", "TEXT", false}}, 1},
	{"asset_subtitles", []iaRefreshColumn{{"id", "TEXT", false}, {"asset_id", "TEXT", false}, {"origin", "TEXT", false}, {"locator", "TEXT", false}, {"stream_index", "INTEGER", false}, {"sidecar_id", "TEXT", true}, {"format", "TEXT", false}, {"language", "TEXT", false}, {"title", "TEXT", false}, {"is_default", "INTEGER", false}, {"is_forced", "INTEGER", false}, {"reason", "TEXT", false}, {"hearing_impaired", "INTEGER", false}}, 1},
}

type iaRefreshRecord interface{ refreshRecord() (uint8, []any) }

type iaRefreshStreamFactRow struct {
	AssetID       string
	Revision      int64
	Size          int64
	ModifiedNS    int64
	Fingerprint   string
	DetailVersion int64
}

func (r iaRefreshStreamFactRow) refreshRecord() (uint8, []any) {
	return 1, []any{r.AssetID, r.Revision, r.Size, r.ModifiedNS, r.Fingerprint, r.DetailVersion}
}

type iaRefreshStreamRow struct {
	AssetID                       string
	StreamIndex                   int64
	Type                          string
	Codec                         string
	Language                      string
	Title                         string
	Channels                      int64
	ChannelLayout                 string
	IsDefault                     int64
	IsForced                      int64
	ColorTransfer                 string
	ColorPrimaries                string
	DetailJSON                    string
	HasAssetStreamBreadth         int64
	BreadthAudioDescription       *int64
	BreadthAudioVariant           *string
	BreadthChromaSubsampling      *string
	BreadthFpsNumerator           *int64
	BreadthFpsDenominator         *int64
	BreadthFrameRateMode          *string
	BreadthInterlaceAnalysis      *string
	BreadthTelecine               *int64
	BreadthDoviRpu                *int64
	BreadthDoviBaseLayer          *int64
	BreadthDoviMetadataKnown      *int64
	BreadthDoviEnhancementType    *string
	BreadthDoviCmVersion          *string
	BreadthPlaybackFramesRevision *string
	BreadthPacketPeakRevision     *string
}

func (r iaRefreshStreamRow) refreshRecord() (uint8, []any) {
	return 2, []any{r.AssetID, r.StreamIndex, r.Type, r.Codec, r.Language, r.Title, r.Channels, r.ChannelLayout, r.IsDefault, r.IsForced, r.ColorTransfer, r.ColorPrimaries, r.DetailJSON, r.HasAssetStreamBreadth, iaRefreshNullable(r.BreadthAudioDescription), iaRefreshNullable(r.BreadthAudioVariant), iaRefreshNullable(r.BreadthChromaSubsampling), iaRefreshNullable(r.BreadthFpsNumerator), iaRefreshNullable(r.BreadthFpsDenominator), iaRefreshNullable(r.BreadthFrameRateMode), iaRefreshNullable(r.BreadthInterlaceAnalysis), iaRefreshNullable(r.BreadthTelecine), iaRefreshNullable(r.BreadthDoviRpu), iaRefreshNullable(r.BreadthDoviBaseLayer), iaRefreshNullable(r.BreadthDoviMetadataKnown), iaRefreshNullable(r.BreadthDoviEnhancementType), iaRefreshNullable(r.BreadthDoviCmVersion), iaRefreshNullable(r.BreadthPlaybackFramesRevision), iaRefreshNullable(r.BreadthPacketPeakRevision)}
}

type iaRefreshChapterFactRow struct {
	AssetID     string
	Revision    int64
	Size        int64
	ModifiedNS  int64
	Status      string
	Fingerprint string
}

func (r iaRefreshChapterFactRow) refreshRecord() (uint8, []any) {
	return 3, []any{r.AssetID, r.Revision, r.Size, r.ModifiedNS, r.Status, r.Fingerprint}
}

type iaRefreshChapterRow struct {
	AssetID      string
	ChapterIndex int64
	Title        string
	StartSeconds float64
	EndSeconds   float64
}

func (r iaRefreshChapterRow) refreshRecord() (uint8, []any) {
	return 4, []any{r.AssetID, r.ChapterIndex, r.Title, r.StartSeconds, r.EndSeconds}
}

type iaRefreshSubtitleFactRow struct {
	AssetID                      string
	Revision                     int64
	Size                         int64
	ModifiedNS                   int64
	OriginUs                     int64
	TimingKnown                  int64
	Status                       string
	Fingerprint                  string
	HasSubtitleInventoryEvidence int64
	InventoryRevision            *int64
	InventoryEvidence            *string
}

func (r iaRefreshSubtitleFactRow) refreshRecord() (uint8, []any) {
	return 5, []any{r.AssetID, r.Revision, r.Size, r.ModifiedNS, r.OriginUs, r.TimingKnown, r.Status, r.Fingerprint, r.HasSubtitleInventoryEvidence, iaRefreshNullable(r.InventoryRevision), iaRefreshNullable(r.InventoryEvidence)}
}

type iaRefreshSidecarRow struct {
	ID         string
	Path       string
	Size       int64
	ModifiedNS int64
	Digest     string
}

func (r iaRefreshSidecarRow) refreshRecord() (uint8, []any) {
	return 6, []any{r.ID, r.Path, r.Size, r.ModifiedNS, r.Digest}
}

type iaRefreshSubtitleRow struct {
	ID              string
	AssetID         string
	Origin          string
	Locator         string
	StreamIndex     int64
	SidecarID       *string
	Format          string
	Language        string
	Title           string
	IsDefault       int64
	IsForced        int64
	Reason          string
	HearingImpaired int64
}

func (r iaRefreshSubtitleRow) refreshRecord() (uint8, []any) {
	return 7, []any{r.ID, r.AssetID, r.Origin, r.Locator, r.StreamIndex, iaRefreshNullable(r.SidecarID), r.Format, r.Language, r.Title, r.IsDefault, r.IsForced, r.Reason, r.HearingImpaired}
}

func iaRefreshNullable[T string | int64 | float64](v *T) any {
	if v == nil {
		return nil
	}
	return *v
}
