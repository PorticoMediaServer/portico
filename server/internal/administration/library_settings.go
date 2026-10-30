package administration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/mediaanalysis"
	"github.com/PorticoMediaServer/portico/server/internal/metadata"
	"github.com/PorticoMediaServer/portico/server/internal/usermutation"
	"reflect"
	"sort"
	"strings"
)

// AnalysisOperation is one thing the server can do to a file after it has been
// discovered. Cost classes group the matrix so an owner can reason about what a
// library will make the machine do, not about individual feature names.
type AnalysisOperation struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	CostClass      string   `json:"costClass"`
	Description    string   `json:"description"`
	MediaKinds     []string `json:"mediaKinds"`
	DefaultEnabled bool     `json:"defaultEnabled"`
	Requires       []string `json:"requires"`
	// ProducesArtifacts says the operation writes bytes into server storage, so
	// the storage care page has a category for it.
	ProducesArtifacts bool `json:"producesArtifacts"`
	// Network says the operation contacts something outside this machine.
	Network bool `json:"network"`
}

// CostClass is the published vocabulary the matrix groups by.
type CostClass struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	// Order lets a client render the groups cheapest first without inventing an
	// order of its own.
	Order int `json:"order"`
}

// CostClasses is the whole vocabulary, cheapest first.
func CostClasses() []CostClass {
	return []CostClass{
		{"metadata-only", "Metadata only", "Reads container headers and sidecar files. Costs one small read per file.", 1},
		{"single-pass-read", "One pass over the file", "Reads the media once end to end without decoding video.", 2},
		{"decode", "Decodes the video", "Decodes frames. This is the expensive class: expect sustained CPU or a busy hardware encoder for the length of the file.", 3},
		{"network", "Contacts a provider", "Sends a request off this machine. Rate limits and provider availability apply.", 4},
	}
}

// AnalysisOperations is every analysis operation this server knows about. The
// library configuration page enables them per library; the scan policy carries
// the enabled set into the ingestion pipeline.
func AnalysisOperations() []AnalysisOperation {
	video := []string{"movie", "show", "episode"}
	all := []string{"movie", "show", "episode", "music", "book"}
	operations := []AnalysisOperation{
		{"probe", "Media probe", "metadata-only", "Reads container, stream, codec, resolution and duration facts. Everything else depends on this.", all, true, nil, false, false},
		{"local_metadata", "Local metadata and sidecars", "metadata-only", "Reads embedded tags, NFO files and sidecar images that sit next to the media.", all, true, []string{"probe"}, false, false},
		{"artwork_extraction", "Embedded artwork", "metadata-only", "Extracts cover art already embedded in the file.", all, true, []string{"probe"}, true, false},
		{"subtitle_discovery", "Subtitle discovery", "metadata-only", "Lists the subtitle tracks inside the file and the subtitle files beside it.", video, true, []string{"probe"}, false, false},
		{"chapter_read", "Chapter markers", "metadata-only", "Reads chapter markers the file already carries.", append([]string{"book"}, video...), true, []string{"probe"}, false, false},
		{"deep_stream_analysis", "Deep stream analysis", "single-pass-read", "Reads the whole file to confirm frame rate, bit depth, HDR format and stream health when headers are incomplete.", video, false, []string{"probe"}, false, false},
		{"subtitle_extraction", "Subtitle extraction", "single-pass-read", "Writes embedded text subtitles out as separate tracks so they can be delivered without a conversion.", video, false, []string{"subtitle_discovery"}, true, false},
		{"loudness", "Loudness measurement", "single-pass-read", "Measures programme loudness so playback can normalise between items.", all, false, []string{"probe"}, false, false},
		{"audio_fingerprint", "Audio fingerprint", "single-pass-read", "Computes an acoustic fingerprint used to match recordings that tags cannot identify.", []string{"music"}, false, []string{"probe"}, false, false},
		{"trickplay", "Trickplay tiles", "decode", "Decodes evenly spaced frames into scrubbing tiles. Storage grows with interval and tile width.", video, false, []string{"probe"}, true, false},
		{"chapter_images", "Chapter images", "decode", "Decodes one frame per chapter for the chapter list.", video, false, []string{"probe"}, true, false},
		{"video_preview", "Video preview", "decode", "Encodes a short silent preview clip used when browsing.", video, false, []string{"probe"}, true, false},
		{"subtitle_download", "Subtitle download", "network", "Asks the configured subtitle provider for tracks the file does not carry.", video, false, []string{"subtitle_discovery"}, true, true},
		{"lyrics", "Lyrics", "network", "Fetches timed lyrics for tracks that have none.", []string{"music"}, false, []string{"probe"}, true, true},
	}
	operations = append(operations, AnalysisOperation{ID: "checksum", Name: "File checksum", CostClass: "single-pass-read", MediaKinds: all}, AnalysisOperation{ID: "waveform", Name: "Audio waveform", CostClass: "decode", MediaKinds: all, Requires: []string{"probe"}, ProducesArtifacts: true})
	supported := []AnalysisOperation{}
	for _, op := range operations {
		if _, ok := analysisRuntime[op.ID]; ok {
			if op.Requires == nil {
				op.Requires = []string{}
			}
			supported = append(supported, op)
		}
	}
	return supported

}

// AnalysisMatrix is the published document behind the library configuration
// page's analysis section.
type AnalysisMatrix struct {
	CostClasses []CostClass         `json:"costClasses"`
	Operations  []AnalysisOperation `json:"operations"`
}

// Matrix publishes the operation vocabulary. It is static server knowledge and
// needs no database read.
func Matrix() AnalysisMatrix { return AnalysisMatrix{CostClasses(), AnalysisOperations()} }

func analysisOperationIDs() map[string]AnalysisOperation {
	out := map[string]AnalysisOperation{}
	for _, op := range AnalysisOperations() {
		out[op.ID] = op
	}
	return out
}

// MetadataProvider is one provider the server can ask about one media kind.
type MetadataProvider struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	MediaKinds      []string `json:"mediaKinds"`
	SupportsAPIKey  bool     `json:"supportsApiKey"`
	SupportsLocale  bool     `json:"supportsLocale"`
	AttributionNote string   `json:"attributionNote"`
}

// MetadataProviders publishes provider choices so a client never hard-codes the
// list or guesses which provider can serve which kind.
func MetadataProviders() []MetadataProvider {
	return []MetadataProvider{
		{"tmdb", "TMDB", []string{"movie", "show", "episode"}, false, true, "Metadata provided by TMDB."},
		{"tvdb", "TheTVDB", []string{"movie", "show", "episode"}, false, true, "Metadata provided by TheTVDB."},
		{"musicbrainz", "MusicBrainz", []string{"music", "book"}, false, false, "Music metadata provided by MusicBrainz."},
		{"anilist", "AniList", []string{"show", "episode", "movie"}, false, false, "Anime metadata provided by AniList."},
		{"local", "Local files only", []string{"movie", "show", "episode", "music", "book", "comic"}, false, false, ""},
	}
}

// ProviderSelection is one media kind's provider choice for one library.
type ProviderSelection struct {
	MediaKind string `json:"mediaKind"`
	Provider  string `json:"provider"`
	// APIKey is retained for wire compatibility; nonempty writes are rejected.
	// Credentials are managed by the provider runtime, never this document.
	APIKey    string `json:"apiKey"`
	APIKeySet bool   `json:"apiKeySet"`
	Language  string `json:"language"`
	Region    string `json:"region"`
}

// GeneratedNavigation controls the artifacts the player navigates with.
type GeneratedNavigation struct {
	TrickplayIntervalMs  int    `json:"trickplayIntervalMs"`
	TrickplayTileWidth   int    `json:"trickplayTileWidth"`
	TrickplayMaxTiles    int    `json:"trickplayMaxTiles"`
	ChapterThumbnailMode string `json:"chapterThumbnailMode"`
	VideoPreviewEnabled  bool   `json:"videoPreviewEnabled"`
	VideoPreviewMs       int    `json:"videoPreviewMs"`
}

// LibrarySettings is one library's whole administration document.
type LibrarySettings struct {
	ScanDepth          string `json:"scanDepth"`
	GroupCollections   bool   `json:"groupCollections"`
	LocalTags          string `json:"localTags"`
	LyricsSource       string `json:"lyricsSource"`
	TitleSource        string `json:"titleSource"`
	EpisodeNumbering   string `json:"episodeNumbering"`
	FindExtras         bool   `json:"findExtras"`
	SortIgnoreArticles bool   `json:"sortIgnoreArticles"`
	RefreshMode        string `json:"refreshMode"`
	AcoustIDEnabled    bool   `json:"acoustidEnabled"`

	MonitorChanges     bool                             `json:"monitorChanges"`
	AllowMediaDeletion bool                             `json:"allowMediaDeletion"`
	ContinueWatching   catalog.ContinueWatchingSettings `json:"continueWatching"`
	Providers          []ProviderSelection              `json:"providers"`
	Analysis           []string                         `json:"analysis"`
	Navigation         GeneratedNavigation              `json:"navigation"`
	// SegmentDetection is this library's intro and credits detection choice
	// (mediaanalysis.SegmentLibraryChoices): "server" follows the server's
	// mode; off, standard or thorough decide for this library alone.
	SegmentDetection string `json:"segmentDetection"`

	// sent records which members the write body carried, so an explicit save
	// writes only the controls the owner actually sent. A member a client does
	// not send keeps the value the library has now instead of being replaced by
	// a zero. It is unexported on purpose: it is not published and not stored,
	// and it reaches a digest only through the canonical mask below. A caller
	// that never decoded a body leaves it nil, which means the whole document
	// was sent.
	sent sentMembers
}

// sentMembers records which members a write body carried. A member that maps to
// nil was applied whole: a scalar, a list, or an array of objects. A member that
// maps to a set of its own is an object applied member by member, so an object
// that names one control cannot reset the rest.
type sentMembers map[string]sentMembers

func (p sentMembers) has(name string) bool {
	_, ok := p[name]
	return ok
}

// object reports an object member the body carried, with the members it carried
// inside it.
func (p sentMembers) object(name string) (sentMembers, bool) {
	nested, ok := p[name]
	return nested, ok && nested != nil
}

// paths lists the controls this body carried as dotted paths, in the order the
// caller sorts. An object with no members changes nothing, so it contributes
// none.
func (p sentMembers) paths(prefix string) []string {
	out := []string{}
	for name, nested := range p {
		if nested == nil {
			out = append(out, prefix+name)
			continue
		}
		out = append(out, nested.paths(prefix+name+".")...)
	}
	return out
}

// publishedFields is the exact wire vocabulary of one document type: the member
// names this build publishes, with the type each one carries.
func publishedFields(typ reflect.Type) map[string]reflect.Type {
	fields := make(map[string]reflect.Type, typ.NumField())
	for i := range typ.NumField() {
		field := typ.Field(i)
		if !field.IsExported() {
			continue
		}
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		if name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
	return fields
}

// readSentMembers walks one decoded object against its published type and
// records what it carried. The request decoder applies its exact-member rule by
// walking these types, and stops at a type that decodes itself, so the rule is
// repeated here: an unknown or case-mismatched member is refused at any depth,
// and null is refused unless the member publishes a choice (only the premiere
// toggle does, where null means the client sent no value rather than false).
func readSentMembers(value any, typ reflect.Type) (sentMembers, error) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	object, ok := value.(map[string]any)
	if !ok || typ.Kind() != reflect.Struct {
		return nil, nil
	}
	fields := publishedFields(typ)
	sent := sentMembers{}
	for name, child := range object {
		field, ok := fields[name]
		if !ok {
			return nil, errors.New("unknown or case-mismatched library setting: " + name)
		}
		if child == nil {
			if field.Kind() != reflect.Pointer {
				return nil, errors.New("library setting cannot be null: " + name)
			}
			continue
		}
		target := field
		for target.Kind() == reflect.Pointer {
			target = target.Elem()
		}
		if target.Kind() == reflect.Struct {
			nested, err := readSentMembers(child, target)
			if err != nil {
				return nil, err
			}
			sent[name] = nested
			continue
		}
		if err := refuseUnknownElements(child, target); err != nil {
			return nil, err
		}
		sent[name] = nil
	}
	return sent, nil
}

// refuseUnknownElements applies the same member rule to the objects inside a
// list, which is written whole and therefore checked as it arrived.
func refuseUnknownElements(value any, typ reflect.Type) error {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Slice && typ.Kind() != reflect.Array {
		return nil
	}
	element := typ.Elem()
	for element.Kind() == reflect.Pointer {
		element = element.Elem()
	}
	if element.Kind() != reflect.Struct {
		return nil
	}
	array, ok := value.([]any)
	if !ok {
		return nil
	}
	for _, item := range array {
		if _, err := readSentMembers(item, element); err != nil {
			return err
		}
	}
	return nil
}

// completeProviderEntries refuses a provider entry that omits a field this
// server applies. The list is written whole, so an entry that named only its
// provider would clear the locale the library has now instead of keeping it.
func completeProviderEntries(value any) error {
	array, ok := value.([]any)
	if !ok {
		return nil
	}
	for _, item := range array {
		entry, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, name := range []string{"mediaKind", "provider", "language", "region"} {
			if _, ok := entry[name]; !ok {
				return errors.New("library provider entry must name " + name)
			}
		}
	}
	return nil
}

// wholeDocumentMask is every control this document publishes, sorted. A caller
// that never decoded a body sends all of them.
func wholeDocumentMask() []string {
	out := []string{}
	var walk func(typ reflect.Type, prefix string)
	walk = func(typ reflect.Type, prefix string) {
		for typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct {
			return
		}
		for name, field := range publishedFields(typ) {
			target := field
			for target.Kind() == reflect.Pointer {
				target = target.Elem()
			}
			if target.Kind() == reflect.Struct {
				walk(target, prefix+name+".")
				continue
			}
			out = append(out, prefix+name)
		}
	}
	walk(reflect.TypeFor[LibrarySettings](), "")
	sort.Strings(out)
	return out
}

// storedLibrarySettings is the stored document without the write document's
// member rule and mask, so a body an older build wrote still reads.
type storedLibrarySettings LibrarySettings

// UnmarshalJSON records which members arrived, so a save writes only the
// controls the owner sent. The request decoder stops checking a type that
// decodes itself, so this keeps its exact-member, null and nested-object rules
// too, rather than bypassing them.
func (v *LibrarySettings) UnmarshalJSON(raw []byte) error {
	type document LibrarySettings
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return err
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return errors.New("library settings must be one object")
	}
	sent, err := readSentMembers(object, reflect.TypeFor[LibrarySettings]())
	if err != nil {
		return err
	}
	if providers, ok := object["providers"]; ok {
		if err = completeProviderEntries(providers); err != nil {
			return err
		}
	}
	var out document
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	*v = LibrarySettings(out)
	v.sent = sent
	return nil
}

// mergeLibrarySettings fills every control the write body did not carry with the
// value the library has now, at the granularity the owner edits: a member the
// body omitted keeps its stored value, and so does a Continue Watching or
// navigation member an object did not name. A missing control is not a request
// to reset it: the page saves one control at a time and a client built against
// an older document sends only the members it knows.
func mergeLibrarySettings(request, current LibrarySettings) LibrarySettings {
	sent := request.sent
	if sent == nil {
		return request
	}
	value := request
	value.sent = nil
	if !sent.has("scanDepth") {
		value.ScanDepth = current.ScanDepth
	}
	if !sent.has("groupCollections") {
		value.GroupCollections = current.GroupCollections
	}
	if !sent.has("localTags") {
		value.LocalTags = current.LocalTags
	}
	if !sent.has("lyricsSource") {
		value.LyricsSource = current.LyricsSource
	}
	if !sent.has("titleSource") {
		value.TitleSource = current.TitleSource
	}
	if !sent.has("episodeNumbering") {
		value.EpisodeNumbering = current.EpisodeNumbering
	}
	if !sent.has("findExtras") {
		value.FindExtras = current.FindExtras
	}
	if !sent.has("sortIgnoreArticles") {
		value.SortIgnoreArticles = current.SortIgnoreArticles
	}
	if !sent.has("refreshMode") {
		value.RefreshMode = current.RefreshMode
	}
	if !sent.has("acoustidEnabled") {
		value.AcoustIDEnabled = current.AcoustIDEnabled
	}
	if !sent.has("monitorChanges") {
		value.MonitorChanges = current.MonitorChanges
	}
	if !sent.has("allowMediaDeletion") {
		value.AllowMediaDeletion = current.AllowMediaDeletion
	}
	if !sent.has("segmentDetection") {
		value.SegmentDetection = current.SegmentDetection
	}
	if !sent.has("analysis") {
		value.Analysis = current.Analysis
	}
	if !sent.has("providers") {
		value.Providers = current.Providers
	}
	if watched, ok := sent.object("continueWatching"); !ok {
		value.ContinueWatching = current.ContinueWatching
	} else {
		// All five Continue Watching controls keep their stored values unless
		// this body named them.
		if !watched.has("weeks") {
			value.ContinueWatching.Weeks = current.ContinueWatching.Weeks
		}
		if !watched.has("maximumItems") {
			value.ContinueWatching.MaximumItems = current.ContinueWatching.MaximumItems
		}
		if !watched.has("videoPlayedThreshold") {
			value.ContinueWatching.VideoPlayedThreshold = current.ContinueWatching.VideoPlayedThreshold
		}
		if !watched.has("videoCompletion") {
			value.ContinueWatching.VideoCompletion = current.ContinueWatching.VideoCompletion
		}
		// A null premiere choice means the client did not send it, not false.
		if !watched.has("includeSeasonPremieres") || value.ContinueWatching.IncludeSeasonPremieres == nil {
			value.ContinueWatching.IncludeSeasonPremieres = current.ContinueWatching.IncludeSeasonPremieres
		}
	}
	if navigation, ok := sent.object("navigation"); !ok {
		value.Navigation = current.Navigation
	} else {
		if !navigation.has("trickplayIntervalMs") {
			value.Navigation.TrickplayIntervalMs = current.Navigation.TrickplayIntervalMs
		}
		if !navigation.has("trickplayTileWidth") {
			value.Navigation.TrickplayTileWidth = current.Navigation.TrickplayTileWidth
		}
		if !navigation.has("trickplayMaxTiles") {
			value.Navigation.TrickplayMaxTiles = current.Navigation.TrickplayMaxTiles
		}
		if !navigation.has("chapterThumbnailMode") {
			value.Navigation.ChapterThumbnailMode = current.Navigation.ChapterThumbnailMode
		}
		if !navigation.has("videoPreviewEnabled") {
			value.Navigation.VideoPreviewEnabled = current.Navigation.VideoPreviewEnabled
		}
		if !navigation.has("videoPreviewMs") {
			value.Navigation.VideoPreviewMs = current.Navigation.VideoPreviewMs
		}
	}
	return value
}

// ChapterThumbnailModes is the published choice list for chapter images.
var ChapterThumbnailModes = []string{"embedded", "generated"}

// DefaultLibrarySettings is the answer before any owner write. Deletion is off:
// a library only deletes media once its owner has said so on this page.
func DefaultLibrarySettings() LibrarySettings {
	enabled := []string{}
	for _, op := range AnalysisOperations() {
		if op.DefaultEnabled {
			enabled = append(enabled, op.ID)
		}
	}
	return LibrarySettings{
		MonitorChanges:   true,
		ScanDepth:        "basic",
		GroupCollections: true, LocalTags: "prefer", LyricsSource: "files", TitleSource: "tags", EpisodeNumbering: "season", FindExtras: true, SortIgnoreArticles: true, RefreshMode: "replace_unlocked",
		AllowMediaDeletion: false,
		ContinueWatching:   catalog.DefaultContinueWatchingSettings(),
		Providers:          []ProviderSelection{},
		Analysis:           enabled,
		Navigation:         GeneratedNavigation{TrickplayIntervalMs: 10000, TrickplayTileWidth: 320, TrickplayMaxTiles: 1080, ChapterThumbnailMode: "embedded", VideoPreviewEnabled: false, VideoPreviewMs: 30000},
		SegmentDetection:   "server",
	}
}

func normalizeLibrarySettings(v *LibrarySettings) {
	if v.ScanDepth == "" {
		v.ScanDepth = "custom"
	}
	catalog.NormalizeContinueWatchingSettings(&v.ContinueWatching)
	if v.LocalTags == "" {
		v.LocalTags = "prefer"
	}
	if v.LyricsSource == "" {
		v.LyricsSource = "files"
	}
	if v.TitleSource == "" {
		v.TitleSource = "tags"
	}
	if v.EpisodeNumbering == "" {
		v.EpisodeNumbering = "season"
	}
	if v.RefreshMode == "" {
		v.RefreshMode = "replace_unlocked"
	}
	if v.Providers == nil {
		v.Providers = []ProviderSelection{}
	}
	if v.Analysis == nil {
		v.Analysis = []string{}
	}
	if v.SegmentDetection == "" {
		v.SegmentDetection = "server"
	}
	sort.Strings(v.Analysis)
	sort.Slice(v.Providers, func(i, j int) bool { return v.Providers[i].MediaKind < v.Providers[j].MediaKind })
}

// redactLibrarySettings strips provider keys on the way out. The stored document
// does not accept keys; no read hands legacy values back.
func redactLibrarySettings(v *LibrarySettings) {
	normalizeLibrarySettings(v)
	for i := range v.Providers {
		v.Providers[i].APIKeySet = v.Providers[i].APIKey != ""
		v.Providers[i].APIKey = ""
	}
}

func validLocale(v string, max int) bool {
	if v == "" {
		return true
	}
	if len(v) > max {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '-' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func validateLibrarySettings(v *LibrarySettings) error {
	fields := []string{}
	if !oneOf(v.ScanDepth, "file_list_only", "basic", "complete", "custom") {
		fields = append(fields, "settings.scanDepth")
	}
	if !oneOf(v.LocalTags, "prefer", "supplement", "off") {
		fields = append(fields, "settings.localTags")
	}
	if !oneOf(v.LyricsSource, "files", "lrclib") {
		fields = append(fields, "settings.lyricsSource")
	}
	if !oneOf(v.TitleSource, "tags", "names") {
		fields = append(fields, "settings.titleSource")
	}
	if !oneOf(v.EpisodeNumbering, "season", "absolute") {
		fields = append(fields, "settings.episodeNumbering")
	}
	if !oneOf(v.RefreshMode, "replace_unlocked", "fill_missing") {
		fields = append(fields, "settings.refreshMode")
	}
	if !catalog.ValidContinueWatchingSettings(v.ContinueWatching) {
		fields = append(fields, "settings.continueWatching")
	}
	providers, kinds := map[string]MetadataProvider{}, map[string]bool{}
	for _, p := range MetadataProviders() {
		providers[p.ID] = p
	}
	for _, sel := range v.Providers {
		p, ok := providers[sel.Provider]
		// One provider per media kind: providers are chosen, never chained.
		if !ok || kinds[sel.MediaKind] {
			fields = append(fields, "settings.providers")
			break
		}
		kinds[sel.MediaKind] = true
		serves := false
		for _, kind := range p.MediaKinds {
			serves = serves || kind == sel.MediaKind
		}
		if !serves || !safeText(sel.APIKey, 256) || sel.APIKey != "" && !p.SupportsAPIKey || !validLocale(sel.Language, 16) || !validLocale(sel.Region, 8) {
			fields = append(fields, "settings.providers")
			break
		}
	}
	if len(v.Providers) > 16 {
		fields = append(fields, "settings.providers")
	}
	known, seen := analysisOperationIDs(), map[string]bool{}
	for _, id := range v.Analysis {
		if _, ok := known[id]; !ok || seen[id] {
			fields = append(fields, "settings.analysis")
			break
		}
		seen[id] = true
	}
	// A dependency that is not enabled would make the operation silently never
	// run, so the page refuses the combination instead of running a half matrix.
	for _, id := range v.Analysis {
		for _, need := range known[id].Requires {
			if !seen[need] {
				fields = append(fields, "settings.analysis")
			}
		}
	}
	// Thumbnails are cut on whole seconds, so the interval is too, and the bounds
	// are the ones that will be stored: a value outside them is refused with its
	// own name, never quietly replaced by one the owner did not choose.
	if v.Navigation.TrickplayIntervalMs < 1000 || v.Navigation.TrickplayIntervalMs > 600000 || v.Navigation.TrickplayIntervalMs%1000 != 0 {
		fields = append(fields, "settings.navigation.trickplayIntervalMs")
	}
	if v.Navigation.TrickplayTileWidth < 64 || v.Navigation.TrickplayTileWidth > 640 || v.Navigation.TrickplayTileWidth%2 != 0 {
		fields = append(fields, "settings.navigation.trickplayTileWidth")
	}
	if v.Navigation.TrickplayMaxTiles < 16 || v.Navigation.TrickplayMaxTiles > catalog.MaxTrickplayTiles {
		fields = append(fields, "settings.navigation.trickplayMaxTiles")
	}
	if v.Navigation.VideoPreviewMs < 5000 || v.Navigation.VideoPreviewMs > 120000 {
		fields = append(fields, "settings.navigation.videoPreviewMs")
	}
	if !oneOf(v.Navigation.ChapterThumbnailMode, ChapterThumbnailModes...) {
		fields = append(fields, "settings.navigation.chapterThumbnailMode")
	}
	if !oneOf(v.SegmentDetection, mediaanalysis.SegmentLibraryChoices...) {
		fields = append(fields, "settings.segmentDetection")
	}
	if len(fields) > 0 {
		return invalid(fields...)
	}
	return nil
}

// LibraryDocument is the library configuration page's response: the settings,
// the library it belongs to, and the vocabularies the page renders from.
type LibraryDocument struct {
	EligibleControls []string                `json:"eligibleControls"`
	LibraryID        string                  `json:"libraryId"`
	LibraryName      string                  `json:"libraryName"`
	LibraryKind      string                  `json:"libraryKind"`
	Revision         int64                   `json:"revision"`
	Digest           string                  `json:"digest"`
	Settings         LibrarySettings         `json:"settings"`
	Defaults         LibrarySettings         `json:"defaults"`
	Matrix           AnalysisMatrix          `json:"analysisMatrix"`
	Providers        []MetadataProvider      `json:"availableProviders"`
	Modes            map[string][]string     `json:"enumerations"`
	Sources          []catalog.LibrarySource `json:"sources"`
	Audience         catalog.LibraryAudience `json:"audience"`
	Actions          []string                `json:"actions"`
}

func libraryScope(id string) string { return scopeFor("library", id) }

func librarySettingsIdentity(ctx context.Context, tx *sql.Tx, id string) (name, kind string, managed bool, err error) {
	err = tx.QueryRowContext(ctx, `SELECT l.name,l.kind,EXISTS(SELECT 1 FROM dvr_private_libraries p WHERE p.library_id=l.id) FROM libraries l WHERE l.id=? AND NOT EXISTS(SELECT 1 FROM catalog_libraries c WHERE c.library_id=l.id AND c.retired=1)`, id).Scan(&name, &kind, &managed)
	if err == sql.ErrNoRows {
		err = ErrNotFound
	}
	return
}

// libraryDefaults is what a library holds before its own page is written.
func libraryDefaults(context.Context, *sql.Tx) (LibrarySettings, error) {
	return DefaultLibrarySettings(), nil
}

// readLibraryDocument reads the stored library document through the shared
// document reader. The body is decoded as the stored shape, so the write
// document's member rule and mask never apply to storage: a member an older
// build wrote still reads, and a body this build cannot read still falls back to
// the caller's defaults at the stored revision, exactly as every other
// configuration document does.
func readLibraryDocument(ctx context.Context, tx *sql.Tx, scope string, defaults LibrarySettings) (LibrarySettings, int64, error) {
	value, revision, err := readDocument(ctx, tx, scope, storedLibrarySettings(defaults))
	return LibrarySettings(value), revision, err
}

// decodeLibraryDocument is the same stored-document rule for a body a listing
// already read: the shared decoder, the library's stored shape and the library's
// own defaults. It is the only way a batched reader may turn a stored body into
// a library document.
func decodeLibraryDocument(body string) LibrarySettings {
	return LibrarySettings(decodeDocument(body, storedLibrarySettings(DefaultLibrarySettings())))
}

func (s *Service) libraryDocument(ctx context.Context, tx *sql.Tx, id string) (LibraryDocument, error) {
	out := LibraryDocument{LibraryID: id, Matrix: Matrix(), Modes: map[string][]string{"chapterThumbnailMode": ChapterThumbnailModes, "videoCompletion": {"earliest", "threshold", "credits"}, "segmentDetection": mediaanalysis.SegmentLibraryChoices}}
	name, kind, managed, err := librarySettingsIdentity(ctx, tx, id)
	if err != nil {
		return out, err
	}
	out.LibraryName, out.LibraryKind = name, kind
	out.Defaults = DefaultLibrarySettings()
	defaultProvider := metadata.DefaultAdministrationPolicy(kind)
	// A screen library is created with the server's own metadata language and
	// region, so the defaults this page offers for Reset must be those values: the
	// kind's factory pair would restore a language and region the owner never
	// configured for new libraries.
	defaultLanguage, defaultRegion := defaultProvider.Language, defaultProvider.Region
	if kind == "movie" || kind == "tv" || kind == "anime" {
		var language, region string
		err = tx.QueryRowContext(ctx, `SELECT default_language,default_region FROM metadata_runtime_settings WHERE singleton=1`).Scan(&language, &region)
		switch {
		case err == nil:
			if language != "" {
				defaultLanguage = language
			}
			if region != "" {
				defaultRegion = region
			}
		case !errors.Is(err, sql.ErrNoRows):
			return out, err
		}
	}
	for _, provider := range defaultProvider.Providers {
		out.Defaults.Providers = append(out.Defaults.Providers, ProviderSelection{MediaKind: libraryMediaKind(kind), Provider: provider, Language: defaultLanguage, Region: defaultRegion})
	}
	if len(out.Defaults.Providers) == 0 {
		out.Defaults.Providers = []ProviderSelection{{MediaKind: libraryMediaKind(kind), Provider: "local"}}
	}
	redactLibrarySettings(&out.Defaults)
	out.Modes["localTags"] = []string{"prefer", "off"}
	if kind == "music" || kind == "audiobook" {
		out.Modes["localTags"] = []string{"prefer", "supplement", "off"}
	}
	out.Modes["lyricsSource"] = []string{"files", "lrclib"}
	out.Modes["titleSource"] = []string{"tags", "names"}
	out.Modes["episodeNumbering"] = []string{"season", "absolute"}
	out.Modes["refreshMode"] = []string{"replace_unlocked", "fill_missing"}

	out.EligibleControls = []string{"libraries.name", "libraries.type", "libraries.access", "libraries.folders", "libraries.folderPath", "libraries.folderName", "libraries.location", "libraries.rescan", "libraries.symlinks", "libraries.allowDelete", "libraries.remove", "libraries.scanNow", "libraries.scanAll", "libraries.watch", "libraries.scanDepth", "libraries.analysisSteps", "libraries.skipped", "libraries.sortArticles"}
	if kind == "movie" {
		out.EligibleControls = append(out.EligibleControls, "libraries.collections")
	}
	if kind == "music" || kind == "audiobook" {
		out.EligibleControls = append(out.EligibleControls, "libraries.localTags")
	}
	if kind == "music" {
		out.EligibleControls = append(out.EligibleControls, "libraries.lyrics", "libraries.fetchLyrics", "libraries.acoustid")
	}
	if kind == "tv" || kind == "anime" {
		out.EligibleControls = append(out.EligibleControls, "libraries.episodeNumbering", "libraries.cwPremieres")
	}
	if kind == "movie" || kind == "tv" || kind == "anime" {
		out.EligibleControls = append(out.EligibleControls, "libraries.language", "libraries.region", "libraries.localTags", "libraries.titleSource", "libraries.extras", "libraries.refreshMode", "libraries.scrub", "libraries.scrubSpacing", "libraries.scrubWidth", "libraries.scrubMax", "libraries.chapterPictures", "libraries.intros", "libraries.cwWeeks", "libraries.cwMax", "libraries.cwWatched", "libraries.cwRule")
	}

	// Only what this library can choose: AniList serves anime libraries alone.
	out.Providers = []MetadataProvider{}
	for _, p := range MetadataProviders() {
		allowed := false
		for _, k := range p.MediaKinds {
			if k == libraryMediaKind(kind) {
				allowed = true
			}
		}
		if kind == "audiobook" || kind == "book" || kind == "comic" {
			allowed = p.ID == "local"
		}
		if allowed && (p.ID != "anilist" || kind == "anime") {
			out.Providers = append(out.Providers, p)
		}
	}
	defaults, err := libraryDefaults(ctx, tx)
	if err != nil {
		return out, err
	}
	settings, revision, err := readLibraryDocument(ctx, tx, libraryScope(id), defaults)
	if err != nil {
		return out, err
	}
	revision, err = effectiveLibraryPolicy(ctx, tx, id, kind, &settings, revision)
	if err != nil {
		return out, err
	}
	redactLibrarySettings(&settings)
	out.Settings, out.Revision, out.Digest = settings, revision, digestOf(settings)
	for _, p := range settings.Providers {
		if p.Provider != "local" {
			out.EligibleControls = append(out.EligibleControls, "libraries.unmatched")
			break
		}
	}
	if kind != "book" && kind != "comic" && kind != "audiobook" {
		out.EligibleControls = append(out.EligibleControls, "libraries.titlesFrom")
	}

	out.Sources, err = catalog.LibrarySourcesTx(ctx, tx, id)
	if err != nil {
		return out, err
	}
	out.Audience, err = catalog.LibraryAudienceTx(ctx, tx, id)
	out.Actions = []string{"rename", "scan", "remove"}
	if managed {
		out.Actions = []string{"rename"}
		out.EligibleControls = []string{"libraries.name", "libraries.type", "libraries.access"}
	}
	return out, err
}

// LibrarySettingsFor reads one library's configuration page.
func (s *Service) LibrarySettingsFor(ctx context.Context, auth Authorize, id string, sonic ...LibrarySonicAuthority) (LibraryDocument, error) {
	var out LibraryDocument
	err := s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		var err error
		out, err = s.libraryDocument(ctx, tx, id)
		if err != nil {
			return err
		}
		return s.librarySonicEligibility(ctx, tx, &out, sonic, false)
	})
	return out, err
}

// SaveLibrarySettings applies one library configuration write. An empty apiKey
// keeps whatever key is already stored, so a client that never receives a key
// can still save the rest of the form.
// librarySettingsPlan bounds one library settings save, which applies the whole
// document and its dependent policy rows in one transaction.
var librarySettingsPlan = dbwork.WritePlan{Name: "library-settings", Rows: 2048, Pages: 256, VMOps: 1_000_000, Why: "a library's settings are one atomic document with its dependent policy rows; their count follows the library's settings, not its content"}

func (s *Service) SaveLibrarySettings(ctx context.Context, auth Authorize, id string, change Change[LibrarySettings], sonic ...LibrarySonicAuthority) (LibraryDocument, error) {
	ctx = dbwork.WithPlan(ctx, librarySettingsPlan)
	var out LibraryDocument
	if !validOperationID(change.OperationID) {
		return out, ErrInput
	}
	// The body as sent is the write's identity: the same identifier with the same
	// body replays, and the same identifier with a different body is refused. The
	// body alone is not enough. A save that omits allowMediaDeletion keeps the
	// stored value while one that sends false changes it, and both decode to the
	// same document, so the identity also carries the canonical mask of the
	// controls the body named.
	sent := change.Settings
	normalizeLibrarySettings(&sent)
	mask := wholeDocumentMask()
	if sent.sent != nil {
		mask = sent.sent.paths("")
		sort.Strings(mask)
	}
	// A member the save omits is replaced with the library's stored value inside
	// the transaction, which cannot be read here. Refuse the members that were
	// sent now, against factory defaults for the rest, so a bad body never takes
	// a write lock or leaves a receipt.
	check := mergeLibrarySettings(change.Settings, DefaultLibrarySettings())
	normalizeLibrarySettings(&check)
	if err := validateLibrarySettings(&check); err != nil {
		return out, err
	}
	scope := libraryScope(id)
	digest := digestOf(struct {
		Expected int64           `json:"expected"`
		Present  []string        `json:"present"`
		Settings LibrarySettings `json:"settings"`
	}{change.ExpectedRevision, mask, sent})
	err := s.transaction(ctx, auth, func(tx *sql.Tx) error {
		r, err := usermutation.CheckContext(ctx, tx, s.milliseconds())
		if err != nil {
			return err
		}
		if r != nil {
			out, err = s.libraryDocument(ctx, tx, id)
			if err != nil {
				return err
			}
			return s.librarySonicEligibility(ctx, tx, &out, sonic, true)
		}

		// A receipt written before the mask carries a digest over the sent document
		// alone. It cannot say which controls its first attempt named, so it must
		// never answer this one: the masks differ in shape, so such a receipt can
		// never match, and the receipt helper refuses the identifier rather than
		// replaying it or writing again.
		stored, replayed, err := receipt[LibraryDocument](ctx, tx, scope, change.OperationID, digest)
		if err != nil {
			return err
		}
		if replayed {
			out = stored
			return s.librarySonicEligibility(ctx, tx, &out, sonic, true)
		}
		_, kind, _, err := librarySettingsIdentity(ctx, tx, id)
		if err != nil {
			return err
		}
		defaults, err := libraryDefaults(ctx, tx)
		if err != nil {
			return err
		}
		current, revision, err := readLibraryDocument(ctx, tx, scope, defaults)
		if err != nil {
			return err
		}
		effective, err := effectiveLibraryPolicy(ctx, tx, id, kind, &current, revision)
		if err != nil {
			return err
		}
		if effective != change.ExpectedRevision {
			return ErrConflict
		}
		// Write the document the owner sent, with every control they did not send
		// left exactly as this library has it now.
		value := mergeLibrarySettings(change.Settings, current)
		// The provider selection is written whole, but an empty region is not a
		// choice the page offers: on an online policy it would clear the region the
		// library stores, while the metadata policy itself only falls back for an
		// empty language. Keep the stored region instead.
		if len(value.Providers) == 1 && len(current.Providers) == 1 && value.Providers[0].Region == "" {
			value.Providers[0].Region = current.Providers[0].Region
		}
		normalizeLibrarySettings(&value)
		if err = validateLibrarySettings(&value); err != nil {
			return err
		}
		if err = applyLibraryPolicy(ctx, tx, id, kind, value); err != nil {
			return err
		}
		// Runtime policy lives only in domain tables. Keep this document for
		// deletion settings; never persist provider credentials here.
		value.Providers = nil
		value.Analysis = nil
		value.Navigation = GeneratedNavigation{}

		at := s.milliseconds()
		if err = writeDocument(ctx, tx, scope, revision+1, struct {
			MonitorChanges     bool                             `json:"monitorChanges"`
			AllowMediaDeletion bool                             `json:"allowMediaDeletion"`
			ContinueWatching   catalog.ContinueWatchingSettings `json:"continueWatching"`
			SegmentDetection   string                           `json:"segmentDetection"`
		}{value.MonitorChanges, value.AllowMediaDeletion, value.ContinueWatching, value.SegmentDetection}, at); err != nil {
			return err
		}
		if out, err = s.libraryDocument(ctx, tx, id); err != nil {
			return err
		}
		if err = s.librarySonicEligibility(ctx, tx, &out, sonic, false); err != nil {
			return err
		}
		return saveSettingsReceipt(ctx, tx, scope, change.OperationID, digest, out, id, out.Revision, at)
	})
	return out, err
}

// deletionPolicy answers whether a library allows media to be deleted.
func deletionPolicy(ctx context.Context, tx *sql.Tx, library string) (bool, error) {
	defaults, err := libraryDefaults(ctx, tx)
	if err != nil {
		return false, err
	}
	settings, _, err := readLibraryDocument(ctx, tx, libraryScope(library), defaults)
	if err != nil {
		return false, err
	}
	return settings.AllowMediaDeletion, nil
}

// LibrarySettingsIndex is the libraries listing the administration pages open
// with: one row per library with just enough to render the list.
type LibrarySettingsIndex struct {
	Items      []LibraryIndexEntry `json:"items"`
	NextCursor string              `json:"nextCursor"`
}

// LibraryIndexEntry is one row of that listing.
type LibraryIndexEntry struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Kind               string `json:"kind"`
	Revision           int64  `json:"revision"`
	AllowMediaDeletion bool   `json:"allowMediaDeletion"`
	AnalysisCount      int    `json:"analysisOperationCount"`
	SourceCount        int    `json:"sourceCount"`
}

// LibraryIndex pages the libraries by name.
func (s *Service) LibraryIndex(ctx context.Context, auth Authorize, token string, limit int, scopes ...func(context.Context, *sql.Tx) ([]string, error)) (LibrarySettingsIndex, error) {
	out := LibrarySettingsIndex{Items: []LibraryIndexEntry{}}
	size, err := pageSize(limit)
	if err != nil {
		return out, err
	}
	c, err := decodeCursor("library-index", token)
	if err != nil {
		return out, err
	}
	err = s.snapshot(ctx, auth, func(tx *sql.Tx) error {
		var allowed any
		if len(scopes) > 0 && scopes[0] != nil {
			ids, err := scopes[0](ctx, tx)
			if err != nil {
				return err
			}
			if ids != nil {
				raw, err := json.Marshal(ids)
				if err != nil {
					return err
				}
				allowed = string(raw)
			}
		}
		rows, err := tx.QueryContext(ctx, `SELECT l.id,l.name,l.kind,(SELECT COUNT(*) FROM library_sources s WHERE s.library_id=l.id) FROM libraries l WHERE NOT EXISTS(SELECT 1 FROM catalog_libraries gone WHERE gone.library_id=l.id AND gone.retired=1) AND (? IS NULL OR l.id IN (SELECT value FROM json_each(?))) AND (?='' OR l.id>?) ORDER BY l.id LIMIT ?`, allowed, allowed, c.ID, c.ID, size+1)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var e LibraryIndexEntry
			if err = rows.Scan(&e.ID, &e.Name, &e.Kind, &e.SourceCount); err != nil {
				return err
			}
			out.Items = append(out.Items, e)
		}
		if err = rows.Err(); err != nil {
			return err
		}
		if len(out.Items) > size {
			out.Items = out.Items[:size]
			out.NextCursor = encodeCursor("library-index", 0, out.Items[size-1].ID)
		}
		if len(out.Items) == 0 {
			return nil
		}
		// One bounded read for the whole page: each row publishes the same
		// composite fence and the same enabled-step count its own page publishes,
		// from one statement for the page instead of a read per library, in this
		// same snapshot. The stored body is decoded here with the shared rule, so
		// a body whose typed field is wrong falls back exactly as the page's own
		// read falls back, and no field is inferred from raw JSON.
		page := make([]string, len(out.Items))
		kind := make(map[string]string, len(out.Items))
		for i := range out.Items {
			page[i], kind[out.Items[i].ID] = out.Items[i].ID, out.Items[i].Kind
		}
		ids, err := json.Marshal(page)
		if err != nil {
			return err
		}
		policyRows, err := tx.QueryContext(ctx, `SELECT l.id,
			d.body, d.revision,
			p.revision,
			s.tier, s.revision, s.operations_json,
			a.revision, m.revision,
			sc.revision,
			(SELECT revision FROM screen_metadata_consent WHERE singleton=1),
			COALESCE((SELECT locale_revision FROM metadata_runtime_settings WHERE singleton=1),1)
			FROM libraries l
			LEFT JOIN admin_documents d ON d.scope='library:'||l.id
			LEFT JOIN library_preferences p ON p.library_id=l.id
			LEFT JOIN library_scan_policies s ON s.library_id=l.id
			LEFT JOIN audio_metadata_policies a ON a.library_id=l.id
			LEFT JOIN mb_provider_policies m ON m.library_id=l.id
			LEFT JOIN screen_metadata_policies sc ON sc.library_id=l.id
			WHERE l.id IN (SELECT value FROM json_each(?))`, string(ids))
		if err != nil {
			return err
		}
		defer policyRows.Close()
		read := map[string]LibraryIndexEntry{}
		for policyRows.Next() {
			var id string
			var body, tier, operations sql.NullString
			var preferences, scan, audio, musicBrainz, screen, consent, documentRevision sql.NullInt64
			var localeRevision int64
			if err = policyRows.Scan(&id, &body, &documentRevision, &preferences, &tier, &scan, &operations, &audio, &musicBrainz, &screen, &consent, &localeRevision); err != nil {
				return err
			}
			// A library whose page cannot produce a fence must not have one made
			// up here: its scan policy is required for every kind, its audio policy
			// for music and audiobooks, and its screen policy and the consent it
			// joins for video. Preferences and the MusicBrainz row are the two the
			// page read itself treats as optional.
			if !scan.Valid || tier.String == "" || operations.String == "" {
				return ErrUnavailable
			}
			revision := int64(1)
			if documentRevision.Valid {
				revision = documentRevision.Int64
			}
			settings := decodeLibraryDocument(body.String)
			preferencesRevision := int64(1)
			if preferences.Valid {
				preferencesRevision = preferences.Int64
			}
			policy, err := catalog.ScanPolicyFromStored(tier.String, scan.Int64, operations.String, catalog.TrickplaySettings{})
			if err != nil {
				return ErrUnavailable
			}
			// The runtime measures loudness for every music library outside a
			// file-list-only scan, so the steps its page reports include it.
			if catalog.AppliesLoudness(kind[id], policy) && !policy.Allows("loudness") {
				policy.Operations = append(policy.Operations, "loudness")
			}
			provider, defaults := int64(0), int64(0)
			switch kind[id] {
			case "movie", "tv", "anime":
				if !screen.Valid || !consent.Valid {
					return ErrUnavailable
				}
				provider = screen.Int64 + consent.Int64 - 2
				// A screen page offers the server's metadata language and region as
				// its Reset pair, so the defaults revision the page folds into its
				// fence is folded in here too.
				if localeRevision > 1 {
					defaults = localeRevision - 1
				}
			case "music", "audiobook":
				if !audio.Valid {
					return ErrUnavailable
				}
				if musicBrainz.Valid {
					provider = audio.Int64 + musicBrainz.Int64 - 2
				}
			}
			read[id] = LibraryIndexEntry{
				Revision:           libraryRevision(revision, policy.Revision, provider, preferencesRevision, defaults),
				AllowMediaDeletion: settings.AllowMediaDeletion,
				AnalysisCount:      len(analysisOperations(policy)),
			}
		}
		if err = policyRows.Err(); err != nil {
			return err
		}
		for i := range out.Items {
			entry, ok := read[out.Items[i].ID]
			if !ok {
				return ErrUnavailable
			}
			out.Items[i].Revision = entry.Revision
			out.Items[i].AllowMediaDeletion = entry.AllowMediaDeletion
			out.Items[i].AnalysisCount = entry.AnalysisCount
		}
		return nil
	})
	return out, err
}
