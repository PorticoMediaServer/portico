package playbackv1

import (
	"context"
	"database/sql"
	"errors"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/audiofacts"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/playback"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"github.com/PorticoMediaServer/portico/server/internal/telemetry"
)

// Options is `GET /v1/items/{itemId}/playback-options` (spec §4.1).
type Options struct {
	ItemID   string         `json:"itemId"`
	Kind     string         `json:"kind"`
	Resume   *OptionsResume `json:"resume,omitempty"`
	Versions []Version      `json:"versions"`
	Chapters []Chapter      `json:"chapters"`
	Markers  []Marker       `json:"markers"`
	// Qualities is the quality menu for the preferred (or previewed) version:
	// the original, automatic, then the ladder steps no taller than its picture.
	Qualities    []QualityOption      `json:"qualities"`
	Preferred    *Preferred           `json:"preferred,omitempty"`
	Plan         *Plan                `json:"plan,omitempty"`
	Alternatives []VersionAlternative `json:"alternatives"`
	Collection   *CollectionSuccessor `json:"collection,omitempty"`
}

// VersionAlternative is a version the device can actually play under its current policy.
// The client asks before using one; a failure never silently changes the version.
type VersionAlternative struct {
	VersionID string `json:"versionId"`
	Label     string `json:"label"`
}

type OptionsResume struct {
	PositionMs int64  `json:"positionMs"`
	PartIndex  int    `json:"partIndex"`
	UpdatedAt  string `json:"updatedAt,omitempty"`
}
type Version struct {
	DiscTitle   *catalog.DiscTitleRef   `json:"discTitle,omitempty"`
	DiscTitles  *catalog.DiscTitles     `json:"discTitles,omitempty"`
	Disc        *catalog.DiscMediaFacts `json:"disc,omitempty"`
	ID          string                  `json:"id"`
	Label       string                  `json:"label"`
	Edition     string                  `json:"edition,omitempty"`
	Container   string                  `json:"container,omitempty"`
	SizeBytes   int64                   `json:"sizeBytes"`
	DurationMs  int64                   `json:"durationMs"`
	BitrateKbps int                     `json:"bitrateKbps,omitempty"`
	Parts       []Part                  `json:"parts"`
	Video       []VideoStream           `json:"video"`
	Audio       []AudioStream           `json:"audio"`
	Subtitles   []SubStream             `json:"subtitles"`
}
type Part struct {
	ID         string `json:"id"`
	Index      int    `json:"index"`
	DurationMs int64  `json:"durationMs"`
	SizeBytes  int64  `json:"sizeBytes"`
}
type VideoStream struct {
	Interlaced                 *bool   `json:"interlaced,omitempty"`
	ChromaSubsampling          string  `json:"chromaSubsampling,omitempty"`
	FrameRateNumerator         int     `json:"frameRateNumerator,omitempty"`
	FrameRateDenominator       int     `json:"frameRateDenominator,omitempty"`
	FrameRateMode              string  `json:"frameRateMode,omitempty"`
	DolbyVisionProfile         int     `json:"dolbyVisionProfile,omitempty"`
	DolbyVisionCompatibility   int     `json:"dolbyVisionCompatibility,omitempty"`
	DolbyVisionRPU             bool    `json:"dolbyVisionRpu"`
	DolbyVisionBaseLayer       bool    `json:"dolbyVisionBaseLayer"`
	DolbyVisionEnhancementType string  `json:"dolbyVisionEnhancementType,omitempty"`
	DolbyVisionCMVersion       string  `json:"dolbyVisionCmVersion,omitempty"`
	HDR10Plus                  bool    `json:"hdr10Plus"`
	MasterDisplay              string  `json:"masterDisplay,omitempty"`
	MaxCLL                     string  `json:"maxCll,omitempty"`
	ID                         string  `json:"id"`
	Codec                      string  `json:"codec"`
	Profile                    string  `json:"profile,omitempty"`
	Level                      int     `json:"level,omitempty"`
	BitDepth                   int     `json:"bitDepth,omitempty"`
	Width                      int     `json:"width,omitempty"`
	Height                     int     `json:"height,omitempty"`
	Fps                        float64 `json:"fps,omitempty"`
	HDR                        string  `json:"hdr,omitempty"`
	BitrateKbps                int     `json:"bitrateKbps,omitempty"`
}
type AudioStream struct {
	Profile        string `json:"profile,omitempty"`
	Variant        string `json:"variant,omitempty"`
	ObjectAudio    string `json:"objectAudio,omitempty"`
	SampleRate     int    `json:"sampleRate,omitempty"`
	BitDepth       int    `json:"bitDepth,omitempty"`
	DTSX           bool   `json:"dtsx"`
	ID             string `json:"id"`
	Codec          string `json:"codec"`
	Channels       int    `json:"channels,omitempty"`
	Layout         string `json:"layout,omitempty"`
	Atmos          bool   `json:"atmos"`
	Language       string `json:"language,omitempty"`
	Title          string `json:"title,omitempty"`
	Default        bool   `json:"default"`
	Commentary     bool   `json:"commentary"`
	VisualImpaired bool   `json:"visualImpaired"`
}
type SubStream struct {
	Deliveries []subtitles.Delivery `json:"deliveries"`
	ID         string               `json:"id"`
	Format     string               `json:"format"`
	Source     string               `json:"source"`
	Language   string               `json:"language,omitempty"`
	Title      string               `json:"title,omitempty"`
	Forced     bool                 `json:"forced"`
	SDH        bool                 `json:"sdh"`
	Default    bool                 `json:"default"`
}
type Chapter struct {
	StartMs int64  `json:"startMs"`
	Title   string `json:"title"`
}

// Marker is one skippable segment (spec §4.2). ID is what a skip report names;
// AutomaticSafe says the server authorizes skipping it unattended (a viewer's
// `auto` preference applies only then).
type Marker struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	StartMs       int64  `json:"startMs"`
	EndMs         int64  `json:"endMs"`
	Confidence    string `json:"confidence"`
	AutomaticSafe bool   `json:"automaticSafe"`
}

// Preferred is what a start that names nothing would play: the version this
// device plays best, the audio track (the viewer's preferred languages, else
// the file's default) and the viewer's saved quality for where the request
// comes from.
type Preferred struct {
	VersionID string `json:"versionId"`
	AudioID   string `json:"audioId,omitempty"`
	// Reason is why the audio track: "profile_language" or "default".
	Reason  string  `json:"reason"`
	Quality Quality `json:"quality"`
}
type Plan struct {
	VersionID string `json:"versionId"`
	Mode      string `json:"mode"`
	// Engine is the player engine a start would name: "native" or "mpv".
	Engine               string       `json:"engine"`
	Streams              []PlanStream `json:"streams"`
	EstimatedBitrateKbps int          `json:"estimatedBitrateKbps,omitempty"`
	Quality              Quality      `json:"quality"`
}
type PlanStream struct {
	ID      string   `json:"id"`
	Action  string   `json:"action"`
	To      string   `json:"to,omitempty"`
	Reasons []string `json:"reasons"`
}

// Preview is what the options query asks "what if" about (plan §7 item 15).
type Preview struct {
	DiscTitle      *catalog.DiscTitleRef
	VersionID      string
	PartID         string // one part of a multi-part version (plans that file)
	AudioID        string
	SubtitleID     string // "none" for off
	ThroughputKbps int
	// Quality with an empty mode is the viewer's saved default for this reach.
	Quality Quality
}

// MarkerFunc projects the viewer's segment markers for one source.
type MarkerFunc func(ctx context.Context, tx *sql.Tx, p identity.Principal, item, source string) ([]Marker, error)

// Service is the v1 resource layer over the existing delivery machinery. One
// per router: it owns the event hub and the lease sweeper.
type Service struct {
	stopCompletions     sync.Map // receipt key -> unfinished exact Stop process cleanup
	stopCompletionMu    sync.Mutex
	stopCompletionCount int

	Traffic   *telemetry.Traffic
	DB        *sql.DB
	Playback  *playback.Service
	Subtitles *subtitles.Service
	Now       func() time.Time
	// ControlsAuthorityTx revalidates the original live device family inside
	// the own-account count snapshot. Nil refuses the capacity projection.
	ControlsAuthorityTx func(context.Context, *sql.Tx, Caller) error
	// StopAuthorityTx revalidates the original current playback family and
	// actual device in the conditional-stop writer. Nil refuses the operation.
	StopAuthorityTx func(context.Context, *sql.Tx, Caller) error
	durations       queueDurationCache
	// LeaseDuration and ReportEvery override the spec defaults (tests).
	LeaseDuration time.Duration
	ReportEvery   time.Duration
	// PausedLimit is how long a video session may stay paused before the
	// sweeper ends it (operations pausedSessionTimeoutMinutes: Plex "Terminate
	// Sessions Paused for Longer Than"). Nil or non-positive: the sweeper is
	// off. Wired from console settings; a read error ends nothing on that tick.
	PausedLimit func(context.Context) time.Duration
	// OutsideHours reports whether an account is outside the hours its access
	// schedule allows. The sweeper ends that account's running sessions with
	// EndReasonOutsideHours, at most a minute after the hours close. Nil: off.
	OutsideHours func(ctx context.Context, account string) bool
	// ReadyWait bounds how long a start waits for its presentation before 202.
	ReadyWait time.Duration
	// ReadyCheck replaces the presentation readiness probe (tests).
	ReadyCheck func(context.Context, playback.Session) (bool, error)
	// QueueKeyLimit overrides MaxQueueKeys (tests).
	QueueKeyLimit int64
	// QueueScanBudget and QueueCountBudget override how many keys a request
	// snapshots itself and how long a large shuffle's count may take (tests);
	// PauseQueueBuilds holds background builds until ResumeQueueBuilds (tests);
	// set it before the first request, or call HoldQueueBuilds once requests run.
	QueueScanBudget  int64
	QueueCountBudget time.Duration
	PauseQueueBuilds bool
	// SaveReadCheck runs after a large queue save closed its read snapshot and
	// before the write that appends it, so a test can change the queue in that
	// gap and prove the copy refuses the new order (tests). Nil: no hook.
	SaveReadCheck func()
	// LibraryCheck and Visibility authorize queue sources and windows (library
	// access, restrictions and member limits); HTTP composition installs them.
	// CastQueueAuthority rechecks the paired screen's actual adopted session
	// in the queue writer. Nil refuses narrowed Cast queue access.
	CastQueueAuthority func(context.Context, *sql.Tx, Caller, string) error
	// CastQueueStartAuthority admits a paired screen's initial queue start before
	// any adopted occurrence exists: the current family/device and active pairing,
	// the same-viewer live device queue, and the selected entry and resolved item
	// bound by the caller. Nil refuses narrowed Cast starts.
	CastQueueStartAuthority func(context.Context, *sql.Tx, Caller, string, string, string) error
	CastQueueTransferCheck  func(context.Context, *sql.Tx, Caller, string, string, string, string) error
	CastQueueTransferCommit func(context.Context, *sql.Tx, Caller, string, string, string, string, string, string, string) error
	CastQueueTransferReplay func(context.Context, *sql.Tx, Caller, string, string, string, string) error

	QueryKeys    func(context.Context, *sql.Tx, identity.Principal, Selector) (catalog.QueueSelection, error)
	RadioKeys    func(context.Context, *sql.Tx, identity.Principal, catalog.RadioBatchRequest) (catalog.RadioBatch, error)
	MixKeys      func(context.Context, *sql.Tx, identity.Principal, string) ([]string, error)
	JourneyKeys  func(context.Context, *sql.Tx, identity.Principal, JourneySelector) ([]string, error)
	SmoothOrder  func(context.Context, *sql.Tx, identity.Principal, []string) (catalog.MusicSmoothOrder, error)
	LibraryCheck LibraryCheck
	Visibility   Visibility
	// Unrestricted says the caller's item fence admits every item (every
	// library, no restrictions): such a caller sees a container as it is, so a
	// large shuffle can use the container's cached size (queue_counts.go).
	// Nil: never.
	Unrestricted func(ctx context.Context, tx *sql.Tx, p identity.Principal) (bool, error)
	// CreatePlaylist is the catalog's playlist create (review P32): a new
	// playlist of the caller's holding items in order, under the operation key.
	CreatePlaylist func(ctx context.Context, p identity.Principal, key, name, summary string, items []string) (id string, revision int64, err error)
	// PostPlay reads the viewer's post-play policy (autoplay, countdown) for
	// QueueView.postPlay (spec §18.5).
	PostPlay func(ctx context.Context, tx *sql.Tx, p identity.Principal) (PostPlayPolicy, error)
	// Channels plays Live TV and Library Channels as v1 sessions (§18.6);
	// ChannelWake starts the linear runtime's work after a change; TunerAvailable
	// says whether a live source has a tuner for one more viewer (excluding the
	// session being replaced). Nil Channels: channel starts are unavailable.
	Channels       ChannelEngine
	ChannelWake    func()
	TunerAvailable func(ctx context.Context, tx *sql.Tx, sourceID, channelID, replacing string) (bool, error)
	// MeasureAudio measures an audio asset's facts (spec §18.7) when a play finds
	// them missing; nil: plays without facts are unavailable for client decoding.
	MeasureAudio audiofacts.Measure
	// AudioMeasureBudget bounds that measurement (default 5 s).
	AudioMeasureBudget time.Duration
	hub                eventHub
	sweep              sweeper
	build              builder
	admission          admissionGates
	queueSweep         queueSweeper
	obsOnce            sync.Once
	obs                *observations
	// Hooks other resources (transfers, queues, groups) attach to sessions.
	// SkipEvidence records a marker skip in the console's diagnostics lane
	// (wired by HTTP composition); nil records the skip without diagnostics.
	SkipEvidence func(ctx context.Context, code string, fields map[string]int64)
	onEnded      func(context.Context, *sql.Tx, row, time.Time) error
}

// New returns the service for one router.
func New(db *sql.DB, player *playback.Service, subs *subtitles.Service) *Service {
	s := &Service{DB: db, Playback: player, Subtitles: subs}
	if player != nil {
		player.GrantReplacingTx = s.grantReplacingTx
		player.GrantReplacedTx = s.grantReplacedTx
	}
	return s
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

var kindNames = map[string]string{"movie": "movie", "episode": "episode", "song": "track", "track": "track", "audiobook_file": "audiobook", "audiobook": "audiobook", "book": "audiobook", "music_video": "musicVideo", "extra": "extra", "trailer": "extra", "recording": "recording"}

// v1Kind is the item's kind in v1 vocabulary; unknown kinds pass through.
func v1Kind(kind string) string {
	if k, ok := kindNames[kind]; ok {
		return k
	}
	return kind
}

type assetRow struct {
	id, container, videoCodec string
	// edition is the version's own edition label, when its name or local
	// metadata gave one.
	edition             string
	size                int64
	duration            float64
	width, height, part int
}

func streamID(prefix string, index int) string { return prefix + strconv.Itoa(index) }

// ParseStreamID reads "a3" → 3 for the given prefix.
func ParseStreamID(prefix, id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, prefix)
	if !ok || rest == "" || len(rest) > 4 {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil && n >= 0
}

func resolutionLabel(height int) string {
	switch {
	case height >= 2000:
		return "4K"
	case height >= 1000:
		return "1080p"
	case height >= 700:
		return "720p"
	case height > 0:
		return "SD"
	}
	return ""
}

func hdrName(transfer, detail string) string {
	var stored assets.Stream
	stored.DecodeDetail(detail)
	if stored.Detail.DynamicRange == assets.RangeDolbyVision {
		return "dolbyvision"
	}
	switch transfer {
	case "smpte2084":
		if stored.Detail.HDR10Plus {
			return "hdr10plus"
		}
		return "hdr10"
	case "arib-std-b67":
		return "hlg"
	}
	return ""
}

// readAssets lists the item's available files, the best version first within
// each part (compactcatalog.BestVersionOrder), so the first version listed is
// the one played when none is named.
func readAssets(ctx context.Context, tx *sql.Tx, item string) ([]assetRow, error) {
	rows, err := tx.QueryContext(ctx, `SELECT a.token,a.container,a.video_codec,a.size,a.duration,a.width,a.height,link.part_index,link.edition
 FROM catalog_entities item JOIN catalog_asset_links link ON link.entity_id=item.id JOIN catalog_assets a ON a.id=link.asset_id
 WHERE item.public_id=pid_blob(?) AND a.available=1
 ORDER BY `+compactcatalog.BestVersionOrder("link", "a"), item)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []assetRow
	for rows.Next() {
		var a assetRow
		if err = rows.Scan(&a.id, &a.container, &a.videoCodec, &a.size, &a.duration, &a.width, &a.height, &a.part, &a.edition); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// versionsOf groups files: distinct part indexes are one multi-part version;
// otherwise files of the same shape (container, video codec, height) whose
// part indexes are distinct are one version's parts (two copies of a two-part
// title are two versions of two parts, not four), and every other file is its
// own version (a 4K remux next to a 1080p encode). Parts keep their order.
func versionsOf(assets []assetRow) [][]assetRow {
	distinct := func(g []assetRow) bool {
		seen := map[int]bool{}
		for _, a := range g {
			if seen[a.part] {
				return false
			}
			seen[a.part] = true
		}
		return true
	}
	if len(assets) > 1 && distinct(assets) {
		return [][]assetRow{assets}
	}
	type shape struct {
		container, codec string
		height           int
	}
	order := []shape{}
	groups := map[shape][]assetRow{}
	for _, a := range assets {
		k := shape{a.container, a.videoCodec, a.height}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], a)
	}
	out := make([][]assetRow, 0, len(assets))
	for _, k := range order {
		if g := groups[k]; len(g) > 1 && distinct(g) {
			out = append(out, g)
			continue
		}
		for _, a := range groups[k] {
			out = append(out, []assetRow{a})
		}
	}
	return out
}

// partAsset resolves a version's part (its part index, as Options lists it)
// to that part's file. Part 0, or the version's first part, is the version
// itself; a part the version doesn't have is a field error. A version that
// isn't a group's first file is left as it is (the planner checks it).
func (s *Service) partAsset(ctx context.Context, item, version string, part int) (string, error) {
	if part == 0 {
		return version, nil
	}
	tx, done, err := dbwork.BeginRead(ctx, s.DB)
	if err != nil {
		return "", err
	}
	defer done()
	assets, err := readAssets(ctx, tx, item)
	if err != nil {
		return "", err
	}
	for i, group := range versionsOf(assets) {
		if version != "" && group[0].id != version || version == "" && i > 0 {
			continue
		}
		for _, a := range group {
			if a.part == part {
				return a.id, nil
			}
		}
		if group[0].part == part {
			return group[0].id, nil
		}
		return "", &FieldError{Path: "partIndex"}
	}
	return "", &FieldError{Path: "partIndex"}
}

func (s *Service) streams(ctx context.Context, tx *sql.Tx, asset string, v *Version) error {
	breadth, err := assets.ReadStreamBreadth(ctx, tx, asset)
	if err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT stream_index,type,codec,language,title,channels,channel_layout,is_default,is_forced,color_transfer,detail_json FROM asset_streams WHERE asset_id=? ORDER BY stream_index`, asset)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var index, channels int
		var kind, codec, language, title, layout, transfer, detail string
		var isDefault, forced bool
		if err = rows.Scan(&index, &kind, &codec, &language, &title, &channels, &layout, &isDefault, &forced, &transfer, &detail); err != nil {
			return err
		}
		var stored assets.Stream
		stored.DecodeDetail(detail)
		facts := stored.Detail
		facts.StreamBreadth = breadth[index]

		stored.Index, stored.Type, stored.Codec, stored.Language, stored.Title, stored.Channels, stored.ChannelLayout, stored.Default, stored.Forced, stored.ColorTransfer, stored.Detail = index, kind, codec, language, title, channels, layout, isDefault, forced, transfer, facts
		s.appendStream(v, stored)
	}
	return rows.Err()
}

func (s *Service) appendStream(v *Version, st assets.Stream) {
	index, kind, codec, language, title, channels, layout, isDefault, forced, transfer, facts := st.Index, st.Type, st.Codec, st.Language, st.Title, st.Channels, st.ChannelLayout, st.Default, st.Forced, st.ColorTransfer, st.Detail
	detail := st.EncodeDetail()

	lower := strings.ToLower(title)
	switch kind {
	case "video":
		v.Video = append(v.Video, VideoStream{Interlaced: facts.ObservedInterlaced(), ChromaSubsampling: facts.ChromaSubsampling, FrameRateNumerator: facts.FrameRateNumerator, FrameRateDenominator: facts.FrameRateDenominator, FrameRateMode: facts.FrameRateMode, DolbyVisionProfile: facts.DolbyVisionProfile, DolbyVisionCompatibility: facts.DolbyVisionCompatibility, DolbyVisionRPU: facts.DolbyVisionRPU, DolbyVisionBaseLayer: facts.DolbyVisionBaseLayer, DolbyVisionEnhancementType: facts.DolbyVisionEnhancementType, DolbyVisionCMVersion: facts.DolbyVisionCMVersion, HDR10Plus: facts.HDR10Plus, MasterDisplay: facts.MasterDisplay, MaxCLL: facts.MaxCLL, ID: streamID("v", index), Codec: codec, Profile: strings.ToLower(facts.Profile), Level: facts.Level, BitDepth: facts.BitDepth, Width: facts.Width, Height: facts.Height, Fps: facts.FrameRate, HDR: hdrName(transfer, detail), BitrateKbps: int(facts.BitRate / 1000)})
	case "audio":
		v.Audio = append(v.Audio, AudioStream{Profile: facts.Profile, Variant: facts.AudioVariant, ObjectAudio: facts.ObjectAudio, SampleRate: facts.SampleRate, BitDepth: facts.BitsPerSample, DTSX: facts.ObjectAudio == "dtsx", ID: streamID("a", index), Codec: codec, Channels: channels, Layout: layout, Atmos: facts.ObjectAudio == "atmos", Language: language, Title: title, Default: isDefault, Commentary: strings.Contains(lower, "commentary"), VisualImpaired: facts.AudioDescription})
	case "subtitle":
		if s.Subtitles == nil {
			v.Subtitles = append(v.Subtitles, SubStream{ID: streamID("s", index), Format: codec, Source: "embedded", Language: language, Title: title, Forced: forced, SDH: strings.Contains(lower, "sdh") || strings.Contains(lower, "hearing"), Default: isDefault})
		}
	}
}

func subtitleSource(origin string) string {
	switch origin {
	case "embedded":
		return "embedded"
	case "sidecar", "local":
		return "sidecar"
	}
	return "downloaded"
}

// Options reads what an item offers and what this device would get (§4.1). No
// side effects. The caller has already admitted the item for this viewer.
// Reach is where a request comes from, for remote caps: a preview is planned
// exactly as a start from the same place would be (review P4).
type Reach struct {
	Remote                  bool
	AdminMaxVideoBitrateBPS int
	// SavedQuality is the viewer's saved default quality for this reach
	// ("original", "automatic" or a ladder id); empty applies the link/kind default.
	SavedQuality      string
	SavedMusicQuality string
	TransportClass    string
	// DeviceID is the device asking, whose capability document decides whether
	// a previewed subtitle would be drawn by the device or burned in.
	DeviceID string
}

func (s *Service) Options(ctx context.Context, p identity.Principal, item string, preview Preview, markersFor MarkerFunc, reach Reach) (Options, error) {
	tx, done, err := dbwork.BeginRead(ctx, s.DB)
	if err != nil {
		return Options{}, err
	}
	defer done()
	// Facts are synchronous; the deleted CheckItemReadiness only guarded the
	// old projection.
	var kind string
	var edition string
	if err = tx.QueryRowContext(ctx, `SELECT k.name,COALESCE(d.edition,'') FROM catalog_entities e JOIN catalog_kinds k ON k.id=e.kind AND k.playable=1 LEFT JOIN catalog_item_details d ON d.entity_id=e.id WHERE e.public_id=pid_blob(?)`, item).Scan(&kind, &edition); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Options{}, ErrNotFound
		}
		return Options{}, err
	}
	out := Options{ItemID: item, Kind: v1Kind(kind), Versions: []Version{}, Chapters: []Chapter{}, Markers: []Marker{}, Qualities: []QualityOption{}, Alternatives: []VersionAlternative{}}
	audioOnly := kind == "song" || kind == "track" || kind == "audiobook_file"
	assets, err := readAssets(ctx, tx, item)
	if err != nil {
		return Options{}, err
	}
	var subtitleCatalog subtitles.Catalog
	if s.Subtitles != nil && len(assets) > 0 {
		if subtitleCatalog, err = s.Subtitles.ListTx(ctx, tx, p, item, ""); err != nil {
			return Options{}, err
		}
	}
	if preview.DiscTitle != nil && (preview.VersionID == "" || preview.PartID != "") {
		return Options{}, &FieldError{Path: "discTitleId"}
	}
	var discChapters []Chapter
	renderers, formats := map[string]string{}, map[string]string{}
	for _, group := range versionsOf(assets) {
		first := group[0]
		// A version's own edition label wins over the title's.
		versionEdition := first.edition
		if versionEdition == "" {
			versionEdition = edition
		}
		v := Version{ID: first.id, Edition: versionEdition, Container: first.container, Parts: []Part{}, Video: []VideoStream{}, Audio: []AudioStream{}, Subtitles: []SubStream{}}
		if !audioOnly && len(group) == 1 {
			if v.Disc, err = catalog.ReadDiscMediaFacts(ctx, tx, first.id); err != nil {
				return Options{}, err
			}
			if v.Disc != nil {
				choices, _, e := catalog.DiscTitlesTx(ctx, tx, item, first.id)
				if e != nil {
					return Options{}, e
				}
				v.DiscTitles = &choices
			}
		}
		for _, a := range group {
			v.Parts = append(v.Parts, Part{ID: a.id, Index: a.part, DurationMs: int64(a.duration * 1000), SizeBytes: a.size})
			v.SizeBytes += a.size
			v.DurationMs += int64(a.duration * 1000)
		}
		if v.DurationMs > 0 {
			v.BitrateKbps = int(v.SizeBytes * 8 / v.DurationMs)
		}
		if err = s.streams(ctx, tx, first.id, &v); err != nil {
			return Options{}, err
		}
		if v.Disc != nil {
			v.Subtitles = []SubStream{}
		}
		for _, r := range subtitleCatalog.Resources {
			renderers[r.ID], formats[r.ID] = r.Renderer, r.Format
			if r.SourceID == first.id && r.Enabled {
				lower := strings.ToLower(r.Title)
				v.Subtitles = append(v.Subtitles, SubStream{Deliveries: r.Deliveries, ID: r.ID, Format: r.Format, Source: subtitleSource(r.Origin), Language: r.Language, Title: r.Title, Forced: r.Forced, SDH: r.HearingImpaired || strings.Contains(lower, "sdh") || strings.Contains(lower, "hearing"), Default: r.Default})
			}
		}
		if preview.DiscTitle != nil && v.ID == preview.VersionID {
			discChapters, err = s.discVersion(ctx, tx, &v, *preview.DiscTitle)
			if err != nil {
				return Options{}, err
			}
			discSubtitles, discErr := s.discSubtitleCatalog(ctx, tx, p, item, v.ID, *preview.DiscTitle)
			if discErr != nil {
				return Options{}, discErr
			}
			for _, r := range discSubtitles.Resources {
				renderers[r.ID], formats[r.ID] = r.Renderer, r.Format
				if r.Enabled {
					lower := strings.ToLower(r.Title)
					v.Subtitles = append(v.Subtitles, SubStream{Deliveries: r.Deliveries, ID: r.ID, Format: r.Format, Source: subtitleSource(r.Origin), Language: r.Language, Title: r.Title, Forced: r.Forced, SDH: r.HearingImpaired || strings.Contains(lower, "sdh") || strings.Contains(lower, "hearing"), Default: r.Default})
				}
			}
			first.container = v.Container
			if len(v.Video) > 0 {
				first.height = v.Video[0].Height
			}
		}
		label := []string{}
		if l := resolutionLabel(first.height); l != "" {
			label = append(label, l)
		}
		if len(v.Video) > 0 && v.Video[0].HDR != "" {
			if v.Video[0].HDR == "dolbyvision" {
				label = append(label, "Dolby Vision")
			} else {
				label = append(label, "HDR")
			}
		}
		if first.container != "" && len(label) > 0 {
			label = append(label, strings.ToUpper(first.container))
		}
		v.Label = strings.Join(label, " · ")
		out.Versions = append(out.Versions, v)
	}
	var positionMS int64
	if err = tx.QueryRowContext(ctx, `SELECT position FROM progress WHERE profile_id=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, identity.PersonalKey(p.Viewer), item).Scan(&positionMS); err == nil && positionMS > 0 {
		out.Resume = &OptionsResume{PositionMs: positionMS}
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Options{}, err
	}
	if preview.DiscTitle != nil {
		out.Resume = nil
	}
	if len(out.Versions) == 0 {
		return out, nil
	}
	if !audioOnly {
		out.Collection, err = s.collectionSuccessor(ctx, tx, p, item)
		if err != nil {
			return Options{}, err
		}
	}
	if preview.Quality.Mode == "" {
		preview.Quality = defaultQuality(reach.SavedQuality, reach.SavedMusicQuality, reach.TransportClass, kind)
	}
	preview.Quality.Step = ""
	if preview.VersionID == "" && !audioOnly && len(out.Versions) > 1 {
		// Released before planning: each candidate version is planned in a
		// read of its own.
		base := playback.V1Choice{Remote: reach.Remote}
		if reach.Remote {
			base.AdminMaxVideoBitrateBPS = reach.AdminMaxVideoBitrateBPS
		}
		preview.Quality.apply(&base)
		done()
		preview.VersionID = s.bestVersion(ctx, p, item, base, preview.ThroughputKbps)
		if tx, done, err = dbwork.BeginRead(ctx, s.DB); err != nil {
			return Options{}, err
		}
		defer done()
	}
	choice, version, err := s.choice(out, preview, reach)
	if err != nil {
		return Options{}, err
	}
	// The preview's subtitle, when there is one: a known track of this version,
	// drawn by the device or burned into the video. Without one, the viewer's
	// Settings decide, as they do for a start.
	subtitleID := preview.SubtitleID
	burnable := false
	if subtitleID != "" && subtitleID != "none" {
		known := false
		for _, sub := range version.Subtitles {
			known = known || sub.ID == subtitleID
		}
		if !known {
			return Options{}, &FieldError{Path: "subtitleId"}
		}
		burnable = renderers[subtitleID] == "burn_in"
		if burnable && subtitleKindOf(formats[subtitleID]) == playback.SubtitleASS && s.Playback != nil && !s.Playback.KeepsSubtitleStylingTx(ctx, tx, p) {
			// "Styled subtitles" off: the words beside the stream, never a burn.
			burnable = false
		}
		if burnable {
			choice.SubtitleKind = subtitleKindOf(formats[subtitleID])
			choice.SubtitleBesideStream = subtitles.RenderingFormat(formats[subtitleID]) != "text"
			if choice.SubtitleKind == playback.SubtitleText {
				choice.SubtitleKind = playback.SubtitleBurnAlways
			}
		}
	}
	facts := SourceFacts{BitrateKbps: version.BitrateKbps}
	if musicKind(kind) && version.SizeBytes > 0 && version.DurationMs > 0 {
		// Round the whole-file upper bound up: truncation must not label a
		// file just above a music ceiling as source-equivalent.
		facts.BitrateKbps = int((version.SizeBytes*8 + version.DurationMs - 1) / version.DurationMs)
	}
	if len(version.Video) > 0 {
		facts.Height = version.Video[0].Height
		if version.Video[0].BitrateKbps > 0 {
			facts.BitrateKbps = version.Video[0].BitrateKbps
		}
	}
	out.Qualities = qualityOptions(facts, audioOnly || len(version.Video) == 0)
	if musicKind(kind) {
		out.Qualities = musicQualityOptions(facts)
	}
	// Chapters and markers are the chosen version's, part after part on one
	// timeline (each part's starts offset by the parts before it).
	if preview.DiscTitle != nil {
		out.Chapters = discChapters
	} else {
		offset := int64(0)
		for _, part := range version.Parts {
			rows, err := tx.QueryContext(ctx, `SELECT title,start_seconds FROM asset_chapters WHERE asset_id=? ORDER BY chapter_index`, part.ID)
			if err != nil {
				return Options{}, err
			}
			for rows.Next() {
				var c Chapter
				var start float64
				if err = rows.Scan(&c.Title, &start); err != nil {
					rows.Close()
					return Options{}, err
				}
				c.StartMs = offset + int64(start*1000)
				out.Chapters = append(out.Chapters, c)
			}
			if err = rows.Err(); err != nil {
				rows.Close()
				return Options{}, err
			}
			rows.Close()
			if markersFor != nil {
				markers, err := markersFor(ctx, tx, p, item, part.ID)
				if err != nil {
					return Options{}, err
				}
				for _, m := range markers {
					m.StartMs, m.EndMs = m.StartMs+offset, m.EndMs+offset
					out.Markers = append(out.Markers, m)
				}
			}
			offset += part.DurationMs
		}
	}
	done()
	out.Preferred = &Preferred{VersionID: version.ID, Reason: "default", Quality: defaultQuality(reach.SavedQuality, reach.SavedMusicQuality, reach.TransportClass, kind)}
	if s.Playback != nil {
		plan, err := s.Playback.PlanV1(ctx, p, item, choice)
		switch {
		case err == nil:
			view := planView(plan, version, choice)
			if subtitleID != "" && subtitleID != "none" {
				burn := burnable && playback.SubtitleNeedsBurnIn(s.deviceDocument(ctx, reach.DeviceID).Profile(plan.Engine), choice.SubtitleKind, plan.Mode, choice.SubtitleBesideStream)
				view.withSubtitle(subtitleID, burn, burnable)
			}
			view.Quality = preview.Quality
			out.Plan = &view
			if plan.AudioStream >= 0 {
				out.Preferred.AudioID = streamID("a", plan.AudioStream)
			}
			if plan.Trace != nil && plan.Trace.Audio != nil {
				if plan.Trace.Audio.ChosenBy == "preferred_language" {
					out.Preferred.Reason = "profile_language"
				}
			}
		case errors.Is(err, playback.ErrTranscodingDisabled):
			// Options still describe the title; starting it will be refused.
		default:
			// A refused choice (no permitted route for this preview) has no plan.
			var refused playback.ErrDeliveryRefused
			if !errors.As(err, &refused) {
				return Options{}, err
			}
		}
	}
	if out.Preferred.AudioID == "" {
		for _, a := range version.Audio {
			if a.Default {
				out.Preferred.AudioID = a.ID
				break
			}
		}
	}
	if s.Playback != nil {
		for _, candidate := range out.Versions {
			probe := preview
			probe.VersionID = candidate.ID
			probe.PartID = ""
			probe.AudioID = ""
			probe.SubtitleID = "none"
			choice, _, e := s.choice(out, probe, reach)
			if e != nil {
				continue
			}
			if plan, e := s.Playback.PlanV1(ctx, p, item, choice); e == nil && (preview.ThroughputKbps <= 0 || planKbps(plan) <= 0 || preview.ThroughputKbps*5 >= planKbps(plan)*4) {
				out.Alternatives = append(out.Alternatives, VersionAlternative{VersionID: candidate.ID, Label: candidate.Label})
			}
		}
	}
	return out, nil
}

// choice turns a preview (or a start request) into the planner's choice.
func (s *Service) choice(o Options, preview Preview, reach Reach) (playback.V1Choice, Version, error) {
	version := o.Versions[0]
	if preview.VersionID != "" {
		found := false
		for _, v := range o.Versions {
			if v.ID == preview.VersionID {
				version, found = v, true
			}
		}
		if !found {
			return playback.V1Choice{}, Version{}, &FieldError{Path: "versionId"}
		}
	}
	c := playback.V1Choice{DiscTitle: preview.DiscTitle, AssetID: version.ID, Remote: reach.Remote}
	if preview.PartID != "" {
		found := false
		for _, part := range version.Parts {
			found = found || part.ID == preview.PartID
		}
		if !found {
			return playback.V1Choice{}, Version{}, &FieldError{Path: "partId"}
		}
		c.AssetID = preview.PartID
	}
	if reach.Remote {
		c.AdminMaxVideoBitrateBPS = reach.AdminMaxVideoBitrateBPS
	}
	if preview.AudioID != "" {
		index, ok := ParseStreamID("a", preview.AudioID)
		known := false
		for _, a := range version.Audio {
			known = known || a.ID == preview.AudioID
		}
		if !ok || !known {
			return playback.V1Choice{}, Version{}, &FieldError{Path: "audioId"}
		}
		c.AudioStream = &index
	}
	if len(version.Video) == 0 && preview.Quality.Mode == QualityFixed {
		return playback.V1Choice{}, Version{}, &FieldError{Path: "quality"}
	}
	preview.Quality.apply(&c)
	return c, version, nil
}

// reasonNames maps the planner's reason codes to the v1 vocabulary clients
// localise (spec §2 "delivery decision"); unknown codes pass through.
var reasonNames = map[string]string{
	playback.ReasonContainerRequiresRemux:    "container_unsupported",
	playback.ReasonAudioCodecRequiresConvert: "audio_codec_unsupported",
	playback.ReasonVideoCodecRequiresConvert: "video_codec_unsupported",
	playback.ReasonHeightExceedsPolicy:       "height_above_request",
	playback.ReasonBitrateExceedsPolicy:      "bitrate_above_request",
	playback.ReasonHDRNotAllowed:             "hdr_tone_mapping_required",
	playback.ReasonSubtitleBurnIn:            "subtitle_image_needs_burn",
	playback.ReasonFiniteNormalization:       "stream_normalized",
}

// chosen-route codes that explain nothing to a viewer.
var quietReasons = map[string]bool{
	playback.ReasonDirectPlayCompatible: true, playback.ReasonDirectAudioCompatible: true, playback.ReasonRemotePreflightCompatible: true,
	playback.ReasonPreparedVersionCompatible: true, playback.ReasonDirectPlayPreferred: true, playback.ReasonDirectStreamPreferred: true,
	playback.ReasonTranscodePreferred: true, playback.ReasonPlanningMaximumFidelity: true, playback.ReasonPlanningCompatibility: true,
	playback.ReasonPlanningMinimizeWork: true, playback.ReasonHardwareBackend: true, playback.ReasonSoftwareFallback: true,
	playback.ReasonQualityRungSelected: true,
}

// Decision is one stream family's delivery in a presentation (spec §5.1).
type Decision struct {
	Action   string   `json:"action"`
	To       string   `json:"to,omitempty"`
	Channels int      `json:"channels,omitempty"`
	Reasons  []string `json:"reasons"`
}
type Decisions struct {
	Video     *Decision `json:"video,omitempty"`
	Audio     *Decision `json:"audio,omitempty"`
	Subtitles *Decision `json:"subtitles,omitempty"`
}

func reasons(plan playback.DeliveryPlan, family string, choice playback.V1Choice) []string {
	out := []string{}
	seen := map[string]bool{}
	add := func(r string) {
		if !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, code := range plan.ReasonCodes {
		if quietReasons[code] {
			continue
		}
		if family == "audio" && code != playback.ReasonAudioCodecRequiresConvert && code != playback.ReasonContainerRequiresRemux && code != playback.ReasonAlternateAudioPolicy {
			continue
		}
		if family == "video" && code == playback.ReasonAudioCodecRequiresConvert {
			continue
		}
		if name, ok := reasonNames[code]; ok {
			code = name
		}
		if code == "bitrate_above_request" && choice.AdminCapped() {
			code = "admin_cap_remote_bitrate"
		}
		add(code)
	}
	return out
}

func action(mode, a string) string {
	switch {
	case mode == "direct" || mode == "remote":
		return "direct"
	case a == "convert":
		return "transcode"
	}
	return "copy"
}

// decisions is the per-family delivery of a plan.
func decisions(plan playback.DeliveryPlan, choice playback.V1Choice, hasVideo bool) Decisions {
	var d Decisions
	if hasVideo {
		video := &Decision{Action: action(plan.Mode, plan.VideoAction), Reasons: reasons(plan, "video", choice)}
		if plan.BurnIn != nil {
			video.Action = "burn"
		}
		if video.Action == "transcode" {
			video.To = plan.OutputVideoCodec
		}
		d.Video = video
	}
	if plan.AudioStream >= 0 || plan.SourceAudioCodec != "" {
		audio := &Decision{Action: action(plan.Mode, plan.AudioAction), Reasons: reasons(plan, "audio", choice)}
		if audio.Action == "transcode" {
			audio.To, audio.Channels = plan.OutputAudioCodec, plan.AudioChannels
		}
		d.Audio = audio
	}
	return d
}

func planView(plan playback.DeliveryPlan, version Version, choice playback.V1Choice) Plan {
	mode := "stream"
	if plan.Mode == "direct" || plan.Mode == "remote" {
		mode = "direct"
	}
	view := Plan{VersionID: version.ID, Mode: mode, Engine: plan.Engine, Streams: []PlanStream{}, EstimatedBitrateKbps: (plan.VideoBitrateBPS + plan.AudioBitrateBPS) / 1000}
	if view.Engine == "" {
		view.Engine = playback.EngineNative
	}
	d := decisions(plan, choice, len(version.Video) > 0)
	if d.Video != nil && len(version.Video) > 0 {
		view.Streams = append(view.Streams, PlanStream{ID: version.Video[0].ID, Action: d.Video.Action, To: d.Video.To, Reasons: d.Video.Reasons})
	}
	if d.Audio != nil {
		id := ""
		if plan.AudioStream >= 0 {
			id = streamID("a", plan.AudioStream)
		} else if len(version.Audio) > 0 {
			id = version.Audio[0].ID
		}
		if id != "" {
			view.Streams = append(view.Streams, PlanStream{ID: id, Action: d.Audio.Action, To: d.Audio.To, Reasons: d.Audio.Reasons})
		}
	}
	return view
}

// withSubtitle adds the preview's subtitle to a plan: a text file beside the
// stream (sidecar), a styled or image track the engine draws itself (device),
// or burned in, which converts the video (as a start with it would: decision
// video "burn").
func (v *Plan) withSubtitle(id string, burn, styledOrImage bool) {
	if !burn {
		action := "sidecar"
		if styledOrImage {
			action = "device"
		}
		v.Streams = append(v.Streams, PlanStream{ID: id, Action: action, Reasons: []string{}})
		return
	}
	v.Mode = "stream"
	for i := range v.Streams {
		if strings.HasPrefix(v.Streams[i].ID, "v") {
			v.Streams[i].Action = "burn"
			v.Streams[i].Reasons = append(v.Streams[i].Reasons, "subtitle_burn_in")
		}
	}
	v.Streams = append(v.Streams, PlanStream{ID: id, Action: "burn", Reasons: []string{}})
}

func fmtMs(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
