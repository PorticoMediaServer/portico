package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/catalog"
	"github.com/PorticoMediaServer/portico/server/internal/compactcatalog"
	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/entityid"
	"github.com/PorticoMediaServer/portico/server/internal/identity"
	"github.com/PorticoMediaServer/portico/server/internal/listeningrules"
	"github.com/PorticoMediaServer/portico/server/internal/operations"
	"github.com/PorticoMediaServer/portico/server/internal/playbackdisc"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
	"github.com/PorticoMediaServer/portico/server/internal/requestinput"
	"github.com/PorticoMediaServer/portico/server/internal/segmentmarkers"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var ErrIncompatible = errors.New("source requires a playback conversion unavailable on this server")

// ErrGrantEnded refuses a media grant whose presentation is over: ended,
// stopped, failed, expired, revoked, superseded by a new sign-in, or a prepared
// track outside its audio route. A grant is a capability, not a credential, so
// this is a hidden 404 (presentation_ended) the player acts on by asking for a
// new presentation, never a 401 that sends the client to sign in (NEW-35).
var ErrGrantEnded = errors.New("This playback has ended. Start it again.")

type Session struct {
	// AudioPlan is the version 2 plan (spec §18.1: the client decodes), pinned
	// for Playback v1 audio presentations.
	AudioPlan         *AudioPlan `json:"audioPlan,omitempty"`
	PreparedVersionID string     `json:"preparedVersionId,omitempty"`
	SourceBoundary    string     `json:"sourceBoundary,omitempty"`
	Error             string     `json:"error,omitempty"`
	ID                string     `json:"id"`
	Generation        int        `json:"generation"`
	StreamURL         string     `json:"streamUrl"`
	Mode              string     `json:"mode"`
	Duration          float64    `json:"duration"`
	ResumeSeconds     float64    `json:"resumeSeconds"`
	State             string     `json:"state,omitempty"`
}
type Service struct {
	occurrences occurrenceRegistry
	discFactory *discPlayFactory
	discInputs  *discPlayInputs // exact private disc readers; no start producer

	// prefetched counts bytes served to private presentations (audio_plan_v2.go).
	prefetchMu      sync.Mutex
	prefetchOwned   map[string]*prefetchRecord
	prefetched      map[string]int64
	prefetchReaders map[string]int
	prefetchRetired map[string]bool
	db              *sql.DB
	hls             *HLS
	remote          *Remote
	StorageGuard    func(string) error
	// AuthorityTx is wired by HTTP composition, never a remote policy lookup.
	AuthorityTx         ControlAuthorityTx
	ContinueAuthorityTx ControlAuthorityTx
	// GrantReplacedTx publishes a presentation whose grant the server rotated
	// on its own (the HLS init-change fallback) to the session that plays it,
	// in the same transaction: the new media generation and its stream URL.
	GrantReplacedTx func(ctx context.Context, tx *sql.Tx, media string, generation int, streamURL string) error
	// GrantReplacingTx runs in the same transaction before the generation
	// moves, so evidence observed under the old one is written under it.
	GrantReplacingTx func(ctx context.Context, tx *sql.Tx, media string) error
	PersonalProgress func(*sql.Tx, identity.Viewer, string, string, int64, float64, float64, string, bool, float64, *bool, int, bool, time.Time, *catalog.PlayedTime) error
	Subtitles        *subtitles.Service
	Prepared         *preparedmedia.Service
	// Delivery seams. Owner administration, viewer preferences and hardware
	// detection are all injected; playback owns none of them.
	deliverySettings    DeliverySettings
	deliveryPreferences DeliveryPreferenceSource
	hardware            *decoder.HardwareDetector
	hardwareFFmpeg      string
	// adapters is the last hardware discovery, so a session start resolves the
	// owner's Device choice without discovering adapters itself.
	adapters hardwareInventory
	// hardwareSeen is the hardware state last reported (review P22): a
	// request reports only a change, never on every session.
	hardwareSeen atomic.Pointer[hardwareState]
	// probe refreshes stream detail at first play; see detail_refresh.go.
	probe      *assets.Probe
	probeSlots chan struct{}
	// MarkersTx is wired by HTTP composition to the analysis viewer projection.
	// Playback publishes segments, it never decides skip safety itself. A nil
	// hook simply leaves markers out of the offer.
	MarkersTx MarkerProjectionTx
}

// MarkerProjectionTx projects the viewer segment markers of one played source
// inside the caller's transaction, under that caller's library authorization.
type MarkerProjectionTx func(context.Context, *sql.Tx, identity.Principal, OffersScope, string) (segmentmarkers.Set, error)

func New(db *sql.DB) *Service { return &Service{db: db} }

// completeDeliveryInputTx adds what every plan for a catalogued file shares: the
// device, the viewer's languages, the file's stream facts and anything this
// device has already reported broken for it. All of it belongs to the same
// immutable decision, so first play, a rung change and an audio change read it
// through this one function.
func (s *Service) completeDeliveryInputTx(ctx context.Context, tx *sql.Tx, p identity.Principal, input *DeliveryInput, values DeliveryPreferenceReader, catalogued bool, explicitAudio *int, boundDisc ...*DeliverySource) error {
	values = preferenceReaderOrDefaults(values)
	device := capabilityDocumentFor(ctx, tx, p)
	input.Device = &device
	input.PreferredAudioLanguages = resolvePreferenceLanguages(values, "playback.preferredAudioLanguages", DeliveryContextFrom(ctx).AppLanguage)
	if values != nil {
		input.AudioTrackChoice = values.Text("playback.audioTrackChoice")
		input.PreferAudioDescription = values.Text("playback.audioDescription") == "prefer"
	}
	input.Server = decoder.CurrentToolchain()
	if prepared, ok := ctx.Value(deliveryPreflightKey{}).(*deliveryPreflight); ok && prepared.service == s {
		input.Server = prepared.toolchain
	}
	input.FMP4Output = s.hls.fragmentedOutput()
	input.NoHLS = s.hls == nil
	if explicitAudio != nil {
		input.AudioStream, input.HasAudioChoice = *explicitAudio, true
	}
	if !catalogued {
		return nil
	}
	// Only a source resolved from a checked title offer, an immutable live-session
	// pin or a STRM's measured facts may enter here. Reloading the physical
	// asset would replace its title, or a STRM's media with its descriptor.
	if len(boundDisc) > 0 && boundDisc[0] != nil {
		input.Source = boundDisc[0]
		input.Disc = !boundDisc[0].Remote
		var err error
		input.ExcludedByEngine, err = routeFailuresTx(ctx, tx, p, input.SourceID)
		return err
	}
	source, err := loadDeliverySource(ctx, tx, input.SourceID)
	if err != nil {
		return err
	}
	input.Source = &source
	if err = discPlanInputTx(ctx, tx, input, nil); err != nil {
		return err
	}
	input.ExcludedByEngine, err = routeFailuresTx(ctx, tx, p, input.SourceID)
	return err
}

// Create starts one presentation of an item at a named quality ("auto" and
// "original" are the original; a ladder id such as "720p" is that quality's
// ceilings). It is the presentation half of a Playback Protocol v1 start with
// no session resource around it, which is how fixtures and internal callers
// that only need a stream reach the same planning every viewer gets.
func (s *Service) Create(p identity.Principal, item, quality, request string) (Session, error) {
	return s.CreateContext(context.Background(), p, item, quality, request)
}
func (s *Service) CreateContext(ctx context.Context, p identity.Principal, item, quality, request string) (Session, error) {
	return s.CreateReplacingContext(ctx, p, item, quality, request, nil)
}

// SessionReplacement names the presentation a new one takes the place of. It
// is always explicit, never inferred from a bearer.
type SessionReplacement struct {
	ID         string `json:"id"`
	Generation int    `json:"generation"`
	// Deferred keeps the old presentation live until the resource layer commits.
	Deferred bool `json:"-"`
}

func (s *Service) CreateReplacingContext(ctx context.Context, p identity.Principal, item, quality, request string, replacement *SessionReplacement) (Session, error) {
	choice, ok := namedQualityChoice(quality)
	if !ok {
		return Session{}, requestinput.New("requestId and a published quality are required")
	}
	choice.UseStoredResume = replacement == nil
	return s.create(ctx, p, item, request, replacement, &choice)
}

// namedQualityChoice is the choice a quality id stands for.
func namedQualityChoice(id string) (V1Choice, bool) {
	switch id {
	case "", "original":
		return V1Choice{QualityID: "original"}, true
	case "automatic":
		return V1Choice{QualityID: "automatic"}, true
	}
	if id == QualityCDID {
		return V1Choice{QualityID: id, AudioCDQuality: true}, true
	}
	if rest, ok := strings.CutPrefix(id, QualityLimitPrefix); ok {
		kbps, err := strconv.Atoi(rest)
		if err != nil || kbps <= 0 || kbps > 10_000 || LimitQualityID(kbps) != id {
			return V1Choice{}, false
		}
		return V1Choice{Limit: true, QualityID: id, MaxAudioBitrateBPS: kbps * 1000}, true
	}
	preset, ok := LadderPreset(id)
	if !ok {
		return V1Choice{}, false
	}
	return V1Choice{Limit: true, QualityID: preset.ID, MaxVideoBitrateBPS: preset.VideoBitrateBPS, MaxAudioBitrateBPS: preset.MaxAudioBitrateBPS, MaxVideoHeight: preset.Height}, true
}

// planInputs is everything one immutable delivery decision is made from.
type planInputs struct {
	input            DeliveryInput
	cfg              DeliveryConfiguration
	values           DeliveryPreferenceReader
	ownerTranscoding bool
}

// planInputsTx resolves owner configuration, the viewer's lane preferences, this
// host's encoder and the file's facts before planning, so one immutable plan
// records every input. Sessions (legacy and v1) and the v1 plan preview share it,
// which is what makes "the same request and profile always produce the same plan"
// true across them.
func (s *Service) planInputsTx(ctx context.Context, tx *sql.Tx, p identity.Principal, item, aid, container, video, audio string, duration float64, remote *DeliverySource, catalogued bool, explicitAudio *int, v1 *V1Choice) (planInputs, error) {
	if v1 == nil {
		v1 = &V1Choice{}
	}
	preparedDelivery, err := s.checkedDeliveryTx(ctx, tx)
	if err != nil {
		return planInputs{}, err
	}
	cfg := preparedDelivery.state.configuration
	edge := DeliveryContextFrom(ctx)
	values := s.viewerPreferences(tx, p, edge)
	policy := ResolveDeliveryPolicy(values, edge.NetworkClass, edge.ServerLocality, edge.TransportClass, cfg)
	var ownerTranscoding bool
	if e := tx.QueryRow(`SELECT transcoding_enabled FROM playback_owner_policy WHERE singleton=1`).Scan(&ownerTranscoding); e != nil {
		return planInputs{}, e
	}
	var sourceHeight int
	var transfer, primaries string
	var measured *DeliverySource
	if v1.discBorrow != nil {
		snapshot, err := v1.discBorrow.legacySnapshotTx(ctx, tx, s, p, item, aid)
		if err != nil {
			return planInputs{}, err
		}
		facts := discSnapshotMedia(snapshot.Media)
		source := discSourceFacts(aid, facts)
		measured = &source
		container, video, audio, duration, sourceHeight = facts.Container, facts.VideoCodec, facts.AudioCodec, facts.Duration, facts.Height
		for _, stream := range facts.Streams {
			if stream.Type == "video" {
				transfer, primaries = stream.ColorTransfer, stream.ColorPrimaries
				break
			}
		}
	} else if remote != nil {
		// A STRM is planned from what the server measured of its target.
		measured = remote
		if remote.Video != nil {
			sourceHeight = remote.Video.Height
		}
	} else if e := tx.QueryRow(`SELECT COALESCE(a.height,0),COALESCE((SELECT f.color_transfer FROM asset_streams f WHERE f.asset_id=a.token AND f.type='video' ORDER BY f.stream_index LIMIT 1),''),COALESCE((SELECT f.color_primaries FROM asset_streams f WHERE f.asset_id=a.token AND f.type='video' ORDER BY f.stream_index LIMIT 1),'') FROM catalog_assets a WHERE a.token=?`, aid).Scan(&sourceHeight, &transfer, &primaries); e != nil {
		return planInputs{}, e
	}
	hardware := preparedDelivery.hardware
	target := v1.target(policy, cfg)
	input := DeliveryInput{SourceID: aid, Container: container, VideoCodec: video, AudioCodec: audio, Duration: duration, Height: sourceHeight, ColorTransfer: transfer, ColorPrimaries: primaries, Remote: remote != nil, Policy: policy, Target: target, Config: cfg, TranscodingEnabled: ownerTranscoding, Hardware: hardware}
	// The caller authorized this public title in this transaction. The
	// catalogue's accepted publication is the sole original-language source.
	originalLanguage, err := publishedOriginalLanguageTx(ctx, tx, item)
	if err != nil {
		return planInputs{}, err
	}
	input.OriginalLanguage = originalLanguage
	input.Engine, input.SubtitleKind, input.SubtitleBesideStream, input.CarriedAudioLanguage = v1.Engine, v1.SubtitleKind, v1.SubtitleBesideStream, v1.CarriedAudioLanguage
	var boundDisc []*DeliverySource
	if measured != nil {
		boundDisc = []*DeliverySource{measured}
	} else if v1.DiscTitle != nil {
		if err := discPlanInputTx(ctx, tx, &input, v1.DiscTitle); err != nil {
			return planInputs{}, err
		}
		boundDisc = []*DeliverySource{input.Source}
	}
	if e := s.completeDeliveryInputTx(ctx, tx, p, &input, values, catalogued, explicitAudio, boundDisc...); e != nil {
		return planInputs{}, e
	}
	if rung, ok := LadderPreset(strings.TrimPrefix(v1.QualityID, "automatic:")); ok && input.Source != nil && input.Source.Video != nil {
		rate := input.Source.BitRate
		if input.Source.Video.BitRate > 0 {
			rate = input.Source.Video.BitRate
		}
		if rung.SameAsSource(input.Source.Video.Height, rate) {
			original := *v1
			original.Limit = false
			original.MaxVideoBitrateBPS = 0
			original.MaxAudioBitrateBPS = 0
			original.MaxVideoHeight = 0
			input.Target = original.target(policy, cfg)
		}
	}
	if input.Source != nil && input.Source.Video != nil || !catalogued && video != "" {
		ceiling, err := memberAwayCeilingsTx(ctx, tx, p.AccountID, v1.remote(policy))
		if err != nil {
			return planInputs{}, err
		}
		// Apply after same-as-source normalization, which may remove only the
		// viewer's chosen rung, never an account's independent ceiling. A STRM
		// over the ceiling is converted like any other file.
		input.Target = narrowMemberTarget(input.Target, ceiling)
	}
	if v1.AirPlay {
		input.Device = nil
		input.Client = BaselineClientProfile()
		input.Client.Transports = []ClientTransport{{Transport: TransportHLSTS, Containers: []string{"mpegts"}, Video: []string{"h264"}, Audio: []string{"aac"}}}
		input.Client.SubtitleRendering = []string{SubtitleText}
		input.Engine = EngineNative
	}
	return planInputs{input: input, cfg: cfg, values: values, ownerTranscoding: ownerTranscoding}, nil
}

func (s *Service) create(ctx context.Context, p identity.Principal, item, request string, replacement *SessionReplacement, v1 *V1Choice) (Session, error) {
	if replacement != nil && (!validControlID(replacement.ID) || replacement.Generation < 1) {
		return Session{}, errControlJSON
	}
	if v1 == nil {
		v1 = &V1Choice{}
	}
	if request == "" || len(request) > 128 {
		return Session{}, requestinput.New("requestId is required")
	}
	var e error
	p, e = s.authorizeSourcePreparation(ctx, p, item)
	if e != nil {
		return Session{}, e
	}
	legacyRequest := request
	request = sessionRequestKey(p, request)
	// Resolve the viewer's version before any remote inspection. Preparation and
	// acceptance must describe the same linked asset, including a STRM alternate.
	var selectedAsset string
	if e := s.selectAsset(ctx, item, v1).Scan(&selectedAsset); e != nil {
		return Session{}, e
	}
	var prepared *remotePreparation
	if s.remote != nil && v1.discBorrow == nil {
		var exists int
		err := s.db.QueryRow(`SELECT 1 FROM playback_requests WHERE account_id=? AND profile_id=? AND request_id IN(?,?) LIMIT 1`, p.AccountID, p.ProfileID, request, legacyRequest).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			var prepErr error
			prepared, prepErr = s.remote.prepare(ctx, item, selectedAsset)
			if prepErr != nil {
				return Session{}, prepErr
			}
		} else if err != nil {
			return Session{}, err
		}
	}
	if v1.discBorrow == nil {
		s.refreshStreamDetail(ctx, selectedAsset)
	}
	ctx, e = s.prepareDelivery(ctx)
	if e != nil {
		return Session{}, e
	}
	gated, e := dbwork.Begin(ctx, s.db, dbwork.ClassEstablishedPlayback)
	if e != nil {
		return Session{}, e
	}
	tx := gated.Tx()
	defer gated.Rollback()
	ctx = gated.Context()
	authorize := s.AuthorityTx
	if authorize != nil {
		if p, e = authorize(ctx, tx, p, item); e != nil {
			return Session{}, e
		}
	}
	// This item fence belongs above receipt replay AND every original/prepared
	// source branch. A derivative must not resurrect a deleted recording.
	if e = recordingAdmittedTx(ctx, tx, item); e != nil {
		return Session{}, e
	}
	if v1.discBorrow != nil {
		if e = v1.discBorrow.checkLegacyRequestTx(ctx, tx, s, p, item, selectedAsset, legacyRequest, v1, replacement); e != nil {
			return Session{}, e
		}
	}
	// playback_sessions, playback_requests and progress hold the integer
	// entity id; callers pass the public id, resolved once here.
	entityID, e := entityid.Resolve(ctx, tx, item)
	if errors.Is(e, entityid.ErrNotFound) {
		return Session{}, sql.ErrNoRows
	}
	if e != nil {
		return Session{}, e
	}
	var receipt, originalItem, storedRequest string
	e = tx.QueryRow(`SELECT response,(SELECT pid(public_id) FROM catalog_entities WHERE id=playback_requests.item_id),request_id FROM playback_requests WHERE account_id=? AND profile_id=? AND request_id IN(?,?) ORDER BY request_id=? DESC LIMIT 1`, p.AccountID, p.ProfileID, request, legacyRequest, request).Scan(&receipt, &originalItem, &storedRequest)
	if e == nil {
		if v1.discBorrow != nil {
			// V1 accepted-result replay happens before this writer. A prepared
			// borrow cannot attach itself to an existing legacy receipt/session.
			return Session{}, playbackdisc.ErrCurrent
		}
		if originalItem != item {
			return Session{}, requestinput.New("requestId already belongs to another item")
		}
		var existing Session
		if e = json.Unmarshal([]byte(receipt), &existing); e != nil {
			return Session{}, e
		}
		var state string
		ownerErr := tx.QueryRow(`SELECT state FROM playback_sessions WHERE id=? AND account_id=? AND profile_id=? AND `+sessionFamilyMatch, existing.ID, p.AccountID, p.ProfileID, p.Hash, p.Hash).Scan(&state)
		if errors.Is(ownerErr, sql.ErrNoRows) {
			var found int
			if e = tx.QueryRow(`SELECT count(*) FROM playback_sessions WHERE id=?`, existing.ID).Scan(&found); e != nil {
				return Session{}, e
			}
			// Never disclose another device's active grant or replay an ambiguous
			// pre-migration receipt as a fresh create.
			if found != 0 || storedRequest == legacyRequest {
				return Session{}, identity.ErrUnauthorized
			}
			state = "stopped"
			existing.StreamURL = ""
		} else if ownerErr != nil {
			return Session{}, ownerErr
		}
		var oldID string
		var oldGeneration int
		bindErr := tx.QueryRowContext(ctx, `SELECT old_session_id,old_generation FROM playback_legacy_replacement_requests WHERE account_id=? AND profile_id=? AND request_id=?`, p.AccountID, p.ProfileID, request).Scan(&oldID, &oldGeneration)
		if bindErr != nil && !errors.Is(bindErr, sql.ErrNoRows) {
			return Session{}, bindErr
		}
		if replacement == nil && bindErr == nil || replacement != nil && (bindErr != nil || oldID != replacement.ID || oldGeneration != replacement.Generation) {
			return Session{}, requestinput.New("requestId belongs to a different replacement")
		}
		existing.State = state
		if state == "failed" {
			existing.Error = "Playback stopped because the source or conversion failed."
		}
		return existing, nil
	}
	if !errors.Is(e, sql.ErrNoRows) {
		return Session{}, e
	}

	var aid, container, video, audio, sourcePath string
	var assetSize, assetModified int64
	var duration float64
	var derivative *preparedmedia.Version
	var measuredDisc playbackdisc.Snapshot
	{
		e = tx.QueryRow(`SELECT a.token,a.container,a.video_codec,a.audio_codec,a.duration,a.path,a.size,a.modified_ns FROM catalog_entities e JOIN catalog_asset_links l ON l.entity_id=e.id JOIN catalog_assets a ON a.id=l.asset_id WHERE e.public_id=pid_blob(?) AND a.token=? AND a.available=1 AND NOT EXISTS(SELECT 1 FROM dvr_catalog_retirements gone WHERE gone.item_id=e.id)`, item, selectedAsset).Scan(&aid, &container, &video, &audio, &duration, &sourcePath, &assetSize, &assetModified)
		if e != nil {
			return Session{}, e
		}
	}
	if v1.discBorrow != nil {
		measuredDisc, e = v1.discBorrow.legacySnapshotTx(ctx, tx, s, p, item, aid)
		if e != nil {
			return Session{}, e
		}
		if measuredDisc.Source.Size != assetSize || measuredDisc.Source.ModifiedNS != assetModified {
			return Session{}, playbackdisc.ErrCurrent
		}
		facts := measuredDisc.Media
		container, video, audio, duration = facts.Container, facts.VideoCodec, facts.AudioCodec, float64(facts.DurationMs)/1000
	}
	if derivative == nil && s.StorageGuard != nil {
		if e = s.StorageGuard(sourcePath); e != nil {
			return Session{}, e
		}
	}
	mode := "direct"
	if container == "strm" {
		if prepared == nil || prepared.assetID != aid {
			return Session{}, ErrIncompatible
		}
		var library string
		if e = tx.QueryRow(`SELECT cl.library_id FROM catalog_entities e JOIN catalog_libraries cl ON cl.id=e.library_id WHERE e.public_id=pid_blob(?)`, item).Scan(&library); e != nil {
			return Session{}, e
		}
		var revision int64
		err := tx.QueryRow(`SELECT revision FROM library_network_policy WHERE library_id=?`, library).Scan(&revision)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Session{}, err
		}
		if revision != prepared.networkRevision {
			return Session{}, identity.ErrUnauthorized
		}
		var width, height int
		video, audio, width, height, duration = prepared.facts.summary()
		mode = "remote"
		if _, _, e = compactcatalog.UpsertAssetTx(ctx, tx, compactcatalog.Asset{Path: sourcePath, Size: assetSize, ModifiedNS: assetModified, Container: container, VideoCodec: video, AudioCodec: audio, Width: width, Height: height, Duration: duration}); e != nil {
			return Session{}, e
		}
		// The probe refreshed the asset's facts; re-derive availability from it.
		if assetID, e := compactcatalog.AssetByTokenTx(ctx, tx, aid); e != nil {
			return Session{}, e
		} else if assetID == 0 {
			return Session{}, sql.ErrNoRows
		} else if e = compactcatalog.SetAssetAvailableTx(ctx, tx, assetID, true); e != nil {
			return Session{}, e
		}
	}
	explicitAudio := v1.AudioStream
	var remoteSource *DeliverySource
	if mode == "remote" {
		measured := prepared.facts.source(aid)
		remoteSource = &measured
	}
	inputs, e := s.planInputsTx(ctx, tx, p, item, aid, container, video, audio, duration, remoteSource, derivative == nil, explicitAudio, v1)
	if e != nil {
		return Session{}, e
	}
	cfg, values, ownerTranscoding := inputs.cfg, inputs.values, inputs.ownerTranscoding
	delivery, e := planDelivery(inputs.input)
	if e != nil {
		return Session{}, e
	}
	delivery.AirPlay = v1.AirPlay
	if v1.AirPlay {
		delivery.Renditions = nil
	}
	{
		target := inputs.input.Target
		delivery.Target, delivery.TargetReach = &target, reachLocal
		if v1.remote(inputs.input.Policy) {
			delivery.TargetReach = reachRemote
		}
	}
	if v1.DiscTitle != nil || v1.discBorrow != nil {
		duration = delivery.Duration
	}
	mode = delivery.Mode
	if v1.discBorrow != nil && mode != "hls" {
		// A private disc input is decoded through its retained wrapper, never
		// by serving the catalogue's original path directly.
		return Session{}, ErrIncompatible
	}
	if mode == "hls" {
		if s.hls == nil {
			return Session{}, ErrIncompatible
		}
		if remoteSource != nil && (s.hls.WindowInputs == nil || prepared.subtitleEvidence == "") {
			// A STRM's HLS reads the retained remote version, nothing else.
			return Session{}, ErrIncompatible
		}
	}

	replacing := ""
	deferred := replacement != nil && replacement.Deferred
	private := v1.PrivateFor != "" || deferred || v1.PendingActivation
	if private && !deferred && !v1.PendingActivation {
		// A private presentation shares its owner's slot, and the owner plays on.
		var valid int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM playback_sessions WHERE id=? AND account_id=? AND profile_id=? AND state NOT IN('stopped','ended','failed')`, v1.PrivateFor, p.AccountID, p.ProfileID).Scan(&valid); e != nil {
			return Session{}, e
		}
		if valid != 1 {
			return Session{}, identity.ErrUnauthorized
		}
		replacing = v1.PrivateFor
	} else if replacement != nil {
		var valid int
		query := `SELECT count(*) FROM playback_sessions WHERE id=? AND generation=? AND account_id=? AND profile_id=? AND state NOT IN('stopped','ended','failed')`
		args := []any{replacement.ID, replacement.Generation, p.AccountID, p.ProfileID}
		if !deferred {
			query += ` AND ` + sessionFamilyMatch
			args = append(args, p.Hash, p.Hash)
		}
		if e = tx.QueryRowContext(ctx, query, args...).Scan(&valid); e != nil {
			return Session{}, e
		}
		if valid != 1 {
			return Session{}, identity.ErrUnauthorized
		}
		replacing = replacement.ID
	}

	if e = checkSessionPolicy(ctx, tx, p, replacing); e != nil {
		return Session{}, e
	}

	if !private && replacing != "" {
		if e = terminateControlOccurrence(ctx, tx, replacing, "stopped"); e != nil {
			return Session{}, e
		}
	}

	if mode == "hls" && !inputs.input.Disc && s.hls.finite != nil && delivery.ConversionTarget == nil {
		delivery.Profile = finiteDeliveryProfile
		delivery.Strategy = DeliveryVideoConversion
		delivery.Reason = ReasonFiniteNormalization
		delivery.ReasonCodes = append(delivery.ReasonCodes, ReasonFiniteNormalization)
		delivery.VideoAction = "convert"
		delivery.OutputVideoCodec = "h264"
	}
	// Enforce policy against the final plan, including finite-timeline conversion.
	if delivery.AudioAction == "convert" && !cfg.AudioConversionAllowed(ownerTranscoding) || delivery.VideoAction == "convert" && !cfg.VideoConversionAllowed(ownerTranscoding) {
		return Session{}, ErrTranscodingDisabled
	}
	if v1.discBorrow != nil {
		delivery.SourceSize, delivery.SourceModifiedNS = measuredDisc.Source.Size, measuredDisc.Source.ModifiedNS
		// The real immutable facts revision is on the exact private snapshot.
		// No integer global inventory revision is invented for these facts.
		delivery.FactsRevision = 0
	} else if e = tx.QueryRow(`SELECT a.size,a.modified_ns,COALESCE((SELECT f.revision FROM asset_stream_facts f WHERE f.asset_id=a.token AND f.size=a.size AND f.modified_ns=a.modified_ns),0) FROM catalog_assets a WHERE a.token=?`, aid).Scan(&delivery.SourceSize, &delivery.SourceModifiedNS, &delivery.FactsRevision); e != nil {
		return Session{}, e
	}
	id, grant := identity.Token(), identity.Token()
	if mode == "hls" {
		if e = reserveHLS(tx, id, replacing, &delivery, cfg); e != nil {
			return Session{}, e
		}
	}
	generation := 1
	if e = tx.QueryRow(`SELECT COALESCE(max(generation),0)+1 FROM playback_sessions WHERE session_hash=? AND account_id=? AND profile_id=?`, p.Hash, p.AccountID, p.ProfileID).Scan(&generation); e != nil {
		return Session{}, e
	}
	expires := time.Now().UTC().Add(12 * time.Hour).Format(time.RFC3339)
	_, e = tx.Exec(`INSERT INTO playback_sessions(id,session_hash,account_id,profile_id,item_id,asset_id,generation,state,grant_hash,grant_token,expires_at,request_id,duration,mode) VALUES(?,?,?,?,?,?,?,'starting',?,?,?,?,?,?)`, id, p.Hash, p.AccountID, p.ProfileID, entityID, aid, generation, identity.Digest(grant), grant, expires, request, duration, mode)
	if e != nil {
		return Session{}, e
	}
	pinResult, e := tx.Exec(`INSERT INTO playback_source_pins(session_id,asset_id,size,modified_ns) SELECT ?,token,size,modified_ns FROM catalog_assets WHERE token=?`, id, aid)
	if e != nil {
		return Session{}, e
	}
	pinRows, e := pinResult.RowsAffected()
	if e != nil {
		return Session{}, e
	}
	if pinRows != 1 {
		return Session{}, ErrStaleOffer
	}
	if v1.discBorrow == nil {
		if e = pinDiscSessionTx(ctx, tx, id, aid, v1.DiscTitle); e != nil {
			return Session{}, e
		}
	}
	if remoteSource != nil && prepared.subtitleEvidence != "" {
		if _, e = tx.Exec(`INSERT INTO subtitle_remote_sessions VALUES(?,?)`, id, prepared.subtitleEvidence); e != nil {
			return Session{}, e
		}
	}
	if v1.discBorrow == nil {
		if e = subtitles.AutoSelectTx(ctx, tx, id, generation, item, aid, subtitles.ViewerKey(p.Authority, p.AccountID, p.ProfileID, p.ServerID), automaticSubtitles(values, delivery.Trace, DeliveryContextFrom(ctx).AppLanguage)); e != nil {
			return Session{}, e
		}
	}

	if mode == "hls" && v1.discBorrow == nil {
		if e = subtitles.PinManifestTextTx(ctx, tx, id, item, aid, subtitles.ViewerKey(p.Authority, p.AccountID, p.ProfileID, p.ServerID)); e != nil {
			return Session{}, e
		}
	}
	if e = persistDeliveryPlan(tx, id, &delivery); e != nil {
		return Session{}, e
	}
	if remoteSource != nil {
		// Relayed, remuxed or converted, every STRM session keeps the facts it
		// was planned from; its re-plans read them, never the catalogue row.
		facts, e := prepared.facts.encode()
		if e != nil {
			return Session{}, e
		}
		if _, e = tx.Exec(`INSERT INTO remote_playback(session_id,descriptor_digest,network_revision,facts_json) VALUES(?,?,?,?)`, id, prepared.descriptorDigest, prepared.networkRevision, facts); e != nil {
			return Session{}, e
		}
	}
	resume := 0.0
	var resumeMS int64
	_ = tx.QueryRow(`SELECT position FROM progress WHERE profile_id=? AND item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?))`, identity.PersonalKey(p.Viewer), item).Scan(&resumeMS)
	resume = float64(resumeMS) / 1000
	var itemKindNum int64
	if e = tx.QueryRow(`SELECT kind FROM catalog_entities WHERE public_id=pid_blob(?)`, item).Scan(&itemKindNum); e != nil {
		return Session{}, e
	}
	itemKind, kindErr := compactcatalog.Kind(itemKindNum).Name()
	if kindErr != nil {
		// New playable kinds follow the generic playback path until they gain
		// kind-specific behavior.
		itemKind = ""
	}
	if itemKind == "audiobook_file" && v1.UseStoredResume {
		preferences, _, _, err := operations.EffectivePreferences(tx, p.Viewer, "")
		if err != nil {
			return Session{}, err
		}
		resume, e = listeningrules.Resume(resume, preferences.Int("audiobooks.resumeRewindMs"))
		if e != nil {
			return Session{}, e
		}
	}
	if duration > 0 && itemKind != "audiobook_file" && resume >= duration-3 {
		resume = 0
	}
	if !private {
		if e = claimSessionProgress(tx, p, item, id, resume); e != nil {
			return Session{}, e
		}
	}
	streamURL := "/v1/media/" + grant
	if mode == "hls" {
		streamURL += "/master.m3u8"
	}
	responseMode := mode
	if responseMode == "remote" {
		responseMode = "direct"
	}
	result := Session{ID: id, Generation: generation, StreamURL: streamURL, Mode: responseMode, Duration: duration, ResumeSeconds: resume, State: "starting"}
	if private {
		owner, until := v1.PrivateFor, v1.PrivateUntilMs
		if v1.PendingActivation {
			until = time.Now().Add(time.Minute).UnixMilli()
		}
		if deferred {
			owner, until = replacement.ID, time.Now().Add(time.Minute).UnixMilli()
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO playback_private_presentations(session_id,owner_session_id,expires_ms) VALUES(?,?,?)`, id, owner, until); e != nil {
			return Session{}, e
		}
	}
	if itemKind == "song" || itemKind == "audiobook_file" {
		if v1.AudioPlanV2 {
			// The audio renderer must obey the same narrowed ceiling as the
			// delivery planner, including when measured render facts know a
			// bitrate that the catalogue stream facts do not yet carry.
			audioChoice := *v1
			audioChoice.MaxAudioBitrateBPS = inputs.input.Target.MaxAudioBitrateBPS
			audioChoice.Limit = audioChoice.Limit || audioChoice.MaxAudioBitrateBPS > 0
			if result.AudioPlan, e = s.pinAudioPlan(ctx, tx, p, result, item, aid, grant, &audioChoice, ownerTranscoding); e != nil {
				return Session{}, e
			}
		}
	}
	var boundary string
	err := tx.QueryRow(`SELECT status FROM episode_asset_boundaries WHERE item_id=(SELECT id FROM catalog_entities WHERE public_id=pid_blob(?)) AND asset_id=?`, item, aid).Scan(&boundary)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Session{}, err
	}
	result.SourceBoundary = boundary
	encoded, e := json.Marshal(result)
	if e != nil {
		return Session{}, e
	}
	if _, e = tx.Exec(`INSERT INTO playback_requests VALUES(?,?,?,?,?)`, p.AccountID, p.ProfileID, request, entityID, string(encoded)); e != nil {
		return Session{}, e
	}
	if replacement != nil {
		if _, e = tx.ExecContext(ctx, `INSERT INTO playback_legacy_replacement_requests VALUES(?,?,?,?,?)`, p.AccountID, p.ProfileID, request, replacement.ID, replacement.Generation); e != nil {
			return Session{}, e
		}
	}
	discSession := v1.discBorrow != nil
	if !discSession {
		if discSession, e = hasDiscSessionTx(ctx, tx, id); e != nil {
			return Session{}, e
		}
	}
	if v1.discBorrow != nil {
		if _, e = tx.ExecContext(ctx, `INSERT INTO playback_physical_source_pins(session_id,source_id,incarnation,configuration_generation) VALUES(?,?,?,?)`, id, measuredDisc.Source.LibrarySourceID, measuredDisc.Source.Incarnation, measuredDisc.Source.LibraryGeneration); e != nil {
			return Session{}, e
		}
		if _, e = v1.discBorrow.bindPendingTx(ctx, tx, playbackdisc.Key{SessionID: id, Generation: generation}); e != nil {
			return Session{}, e
		}
		automatic := automaticSubtitles(values, delivery.Trace, DeliveryContextFrom(ctx).AppLanguage)
		if v1.SubtitleOff {
			automatic.Off = true
		}
		if e = v1.discBorrow.bindSubtitleResourcesTx(ctx, tx, s, p, item, automatic); e != nil {
			return Session{}, e
		}
	} else if _, e = tx.ExecContext(ctx, `INSERT INTO playback_physical_source_pins SELECT ?,source.id,source.incarnation,source.generation FROM inventory_objects object JOIN library_sources source ON source.id=object.source_id JOIN catalog_entities item ON item.id=? JOIN catalog_libraries cl ON cl.id=item.library_id AND cl.library_id=source.library_id WHERE object.asset_id=? AND object.retired=0 AND object.state='available' AND object.root_incarnation=source.incarnation AND source.enabled=1
 AND (? OR (object.size=(SELECT size FROM catalog_assets WHERE token=object.asset_id) AND object.modified_ns=(SELECT modified_ns FROM catalog_assets WHERE token=object.asset_id)
 AND source.kind IN('local','remote') AND source.identity_confirmed=1 AND source.health NOT IN('offline','root_changed','removing') AND source.root_identity!='' AND object.object_identity!=''
 AND rtrim(source.root,?)||?||object.relative_path=(SELECT path FROM catalog_assets WHERE token=object.asset_id)))
 ORDER BY source.id,object.id LIMIT 1`, id, entityID, aid, discSession, string(filepath.Separator), string(filepath.Separator)); e != nil {
		return Session{}, e
	}
	if !discSession {
		if e = assets.PinPlaybackBindingTx(ctx, tx, id); e != nil {
			return Session{}, e
		}
	}
	if e = gated.Commit(); e != nil {
		return Session{}, e
	}
	return result, nil
}

func (s *Service) Get(p identity.Principal, id string) (Session, error) {
	var out Session
	var grant string
	gated2, e := dbwork.BeginSnapshot(context.Background(), s.db)
	if e != nil {
		return out, e
	}
	tx := gated2.Tx()
	defer gated2.Rollback()
	if s.ContinueAuthorityTx != nil {
		if p, e = s.ContinueAuthorityTx(context.Background(), tx, p, ""); e != nil {
			return out, e
		}
	}
	e = tx.QueryRow(`SELECT id,generation,grant_token,duration,state,mode,COALESCE((SELECT json_extract(r.response,'$.sourceBoundary') FROM playback_requests r WHERE r.account_id=playback_sessions.account_id AND r.profile_id=playback_sessions.profile_id AND r.request_id=playback_sessions.request_id),'') FROM playback_sessions WHERE id=? AND account_id=? AND profile_id=? AND `+sessionFamilyMatch, id, p.AccountID, p.ProfileID, p.Hash, p.Hash).Scan(&out.ID, &out.Generation, &grant, &out.Duration, &out.State, &out.Mode, &out.SourceBoundary)

	if e != nil {
		return out, e
	}
	if s.AuthorityTx != nil {
		var item string
		if e = tx.QueryRow(`SELECT pid(e.public_id) FROM playback_sessions ps JOIN catalog_entities e ON e.id=ps.item_id WHERE ps.id=?`, id).Scan(&item); e != nil {
			return out, e
		}
		if _, e = s.AuthorityTx(context.Background(), tx, p, item); e != nil {
			return out, e
		}
	}
	_ = tx.QueryRow(`SELECT version_id FROM prepared_media_session_pins WHERE session_id=?`, id).Scan(&out.PreparedVersionID)
	out.StreamURL, out.Mode = streamURLTx(tx, id, out.Generation, grant, out.Mode)
	if out.State == "failed" {
		out.Error = "Playback stopped because the source or conversion failed."
	}
	out.AudioPlan, e = readAudioPlan(tx, out.ID, out.Generation)
	return out, e
}
func (s *Service) Progress(p identity.Principal, id string, generation int, sequence int64, position float64, state string) error {
	return s.ProgressContext(context.Background(), p, id, generation, sequence, position, state)
}

// ProgressContext abandons report work when its request or playback timeline
// deadline expires, without stopping the live presentation. A caller that does
// not know the player's rate reports none, so the interval cannot be proven.
func (s *Service) ProgressContext(ctx context.Context, p identity.Principal, id string, generation int, sequence int64, position float64, state string) error {
	return s.ProgressReportedContext(ctx, p, id, generation, sequence, position, state, 0)
}

// ProgressReportedContext is ProgressContext with the player's own reported
// rate. It is the same accepted evidence path; the rate only lets a reader tell
// a played interval from a jump.
func (s *Service) ProgressReportedContext(ctx context.Context, p identity.Principal, id string, generation int, sequence int64, position float64, state string, rate float64) error {
	return s.ProgressEvidenceContext(ctx, p, id, generation, sequence, position, state, rate, nil, state == "playing")
}

// ProgressEvidenceContext adds whether this report follows a seek. Nil means
// the caller cannot say, which withholds chapter completion for the interval;
// a report that follows a seek still moves the resume point but no played
// interval crosses it.
func (s *Service) ProgressEvidenceContext(ctx context.Context, p identity.Principal, id string, generation int, sequence int64, position float64, state string, rate float64, seeked *bool, playing bool) error {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 {
		return requestinput.New("invalid playback rate")
	}
	if math.IsNaN(position) || math.IsInf(position, 0) || position < 0 || sequence < 0 {
		return requestinput.New("invalid progress")
	}
	if state != "playing" && state != "paused" && state != "ended" {
		return requestinput.New("invalid playback state")
	}
	gated3, e := dbwork.Begin(ctx, s.db, dbwork.ClassEstablishedPlayback)
	if e != nil {
		return e
	}
	tx := gated3.Tx()
	defer gated3.Rollback()
	if e = s.progressTx(gated3.Context(), tx, p, id, generation, sequence, position, state, rate, seeked, playing, time.Time{}, nil); e != nil {
		return e
	}
	return gated3.Commit()
}

// ObservationEvidenceTx accepts one applied playback observation inside the
// caller's writer transaction (playbackv1 observations): the same fences,
// resume, history and chapter evidence as a timeline report, with the time the
// sample was received and the played time the observation already proved.
func (s *Service) ObservationEvidenceTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id string, generation int, sequence int64, position float64, state string, rate float64, seeked *bool, playing bool, received time.Time, proved *catalog.PlayedTime) error {
	if math.IsNaN(rate) || math.IsInf(rate, 0) || rate < 0 || math.IsNaN(position) || math.IsInf(position, 0) || position < 0 || sequence < 0 {
		return requestinput.New("invalid progress")
	}
	if state != "playing" && state != "paused" && state != "ended" {
		return requestinput.New("invalid playback state")
	}
	return s.progressTx(ctx, tx, p, id, generation, sequence, position, state, rate, seeked, playing, received, proved)
}

func (s *Service) progressTx(ctx context.Context, tx *sql.Tx, p identity.Principal, id string, generation int, sequence int64, position float64, state string, rate float64, seeked *bool, playing bool, received time.Time, proved *catalog.PlayedTime) error {
	var e error
	if e = s.checkSessionOccurrenceModeTx(ctx, tx, id, false); e != nil {
		return e
	}
	// Fence authentication and current item policy before accepting any evidence.
	// Live/Library occurrences are in another table and cannot enter this path.
	var observedItem string
	e = tx.QueryRowContext(ctx, `SELECT pid(e.public_id) FROM playback_sessions JOIN catalog_entities e ON e.id=playback_sessions.item_id WHERE playback_sessions.id=? AND playback_sessions.account_id=? AND playback_sessions.profile_id=? AND playback_sessions.generation=? AND `+sessionFamilyMatch, id, p.AccountID, p.ProfileID, generation, p.Hash, p.Hash).Scan(&observedItem)
	if errors.Is(e, sql.ErrNoRows) {
		return nil // Stale/foreign occurrence; no observation or history is changed.
	}
	if e != nil {
		return e
	}
	if s.AuthorityTx != nil {
		if p, e = s.AuthorityTx(ctx, tx, p, observedItem); e != nil {
			return e
		}
	}
	r, e := tx.ExecContext(ctx, `UPDATE playback_sessions SET sequence=?,state=?,session_hash=? WHERE id=? AND generation=? AND sequence<? AND state NOT IN ('stopped','ended','failed')`, sequence, state, p.Hash, id, generation, sequence)
	if e != nil {
		return e
	}
	n, _ := r.RowsAffected()
	if n == 0 {
		return nil
	}
	_, e = tx.ExecContext(ctx, `UPDATE playback_observations SET reported_at=?,position=CASE WHEN (SELECT duration FROM playback_sessions WHERE id=?)>0 THEN min(?,(SELECT duration FROM playback_sessions WHERE id=?)) ELSE ? END WHERE session_id=?`, time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), id, position, id, position, id)
	if e != nil {
		return e
	}
	var item string
	var duration float64
	var canonical bool
	if e = tx.QueryRowContext(ctx, `SELECT pid(e.public_id),ps.duration,EXISTS(SELECT 1 FROM progress WHERE profile_id=? AND item_id=ps.item_id AND playback_id=ps.id) FROM playback_sessions ps JOIN catalog_entities e ON e.id=ps.item_id WHERE ps.id=?`, identity.PersonalKey(p.Viewer), id).Scan(&item, &duration, &canonical); e != nil {
		return e
	}
	if s.PersonalProgress != nil {
		if e = s.PersonalProgress(tx, p.Viewer, id, item, sequence, position, duration, state, canonical, rate, seeked, generation, playing, received, proved); e != nil {
			return e
		}
	} else {
		positionMS := int64(math.Round(position * 1000))
		_, e = tx.ExecContext(ctx, `UPDATE progress SET position=min(?,CAST((SELECT duration FROM playback_sessions WHERE id=?)*1000 AS INTEGER)),unit=0 WHERE profile_id=? AND playback_id=?`, positionMS, id, identity.PersonalKey(p.Viewer), id)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO progress_activity(profile_id,library_id,item_id,updated_at,state) SELECT ?,cl.library_id,p.item_id,?,? FROM playback_sessions p JOIN catalog_entities i ON i.id=p.item_id JOIN catalog_libraries cl ON cl.id=i.library_id WHERE p.id=? ON CONFLICT(profile_id,item_id) DO UPDATE SET updated_at=excluded.updated_at,state=excluded.state`, identity.PersonalKey(p.Viewer), time.Now().UTC().Format("2006-01-02T15:04:05.000Z"), state, id)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `UPDATE book_resume SET position=min(?,CAST((SELECT duration FROM playback_sessions WHERE id=?)*1000 AS INTEGER)),unit=0 WHERE profile_id=? AND playback_id=?`, positionMS, id, identity.PersonalKey(p.Viewer), id)
		if e != nil {
			return e
		}

	}
	return nil
}
func (s *Service) Stop(p identity.Principal, id string) error {
	gated4, e := dbwork.Begin(context.Background(), s.db, dbwork.ClassEstablishedPlayback)
	if e != nil {
		return e
	}
	tx := gated4.Tx()
	defer gated4.Rollback()
	var own int
	if e = tx.QueryRow(`SELECT count(*) FROM playback_sessions WHERE id=? AND account_id=? AND profile_id=? AND `+sessionFamilyMatch, id, p.AccountID, p.ProfileID, p.Hash, p.Hash).Scan(&own); e != nil {
		return e
	}
	if own != 1 {
		return nil
	}
	if e = terminateControlOccurrence(context.Background(), tx, id, "stopped"); e != nil {
		return e
	}
	if e = gated4.Commit(); e != nil {
		return e
	}
	s.stopHLS(id)
	return nil
}

// Only callers that have durably stopped this exact occurrence may cancel its
// worker. Internal queue cleanup already proved ownership in its transaction;
// reauthenticating an old request token after family renewal would leak a worker.
func (s *Service) stopHLS(id string) {
	s.ForgetPrefetch(id)
	if s.hls == nil {
		return
	}
	s.hls.mu.Lock()
	cancel := s.hls.active[id]
	s.hls.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (s *Service) ResolveGrant(grant string) (string, identity.Principal, string, error) {
	return s.ResolveGrantContext(context.Background(), grant)
}

// ResolveGrantContext keeps disconnects and request deadlines attached to the
// grant and continuation checks. It never changes the grant's lifetime.
func (s *Service) ResolveGrantContext(ctx context.Context, grant string) (string, identity.Principal, string, error) {
	return s.resolveGrant(ctx, grant, false)
}
func (s *Service) resolveGrant(ctx context.Context, grant string, initialAudio bool) (_ string, _ identity.Principal, _ string, err error) {
	var aid, item, expires, sessionExpires, state string
	var revoked int
	p := identity.Principal{}
	gated5, e := dbwork.BeginSnapshot(ctx, s.db)
	if e != nil {
		return "", p, "", e
	}
	ctx = gated5.Context()
	tx := gated5.Tx()
	defer gated5.Rollback()
	defer func() {
		// database/sql may finish its cancellation rollback before a query
		// reaches the transaction. Preserve the cancellation cause instead of
		// reporting that race as an invalid or ended presentation.
		if err != nil && ctx.Err() != nil {
			err = ctx.Err()
		}
	}()
	var sid string
	if e = tx.QueryRowContext(ctx, `SELECT id FROM playback_sessions WHERE grant_hash=?`, identity.Digest(grant)).Scan(&sid); e != nil {
		return "", p, "", e
	}
	if e = s.checkSessionOccurrenceModeTx(ctx, tx, sid, initialAudio); e != nil {
		if errors.Is(e, identity.ErrUnauthorized) {
			e = ErrGrantEnded
		}
		return "", p, "", e
	}
	e = tx.QueryRowContext(ctx, `SELECT ps.asset_id,pid(e.public_id),ps.expires_at,ps.state,s.hash,s.account_id,s.profile_id,s.authority,s.role,s.epoch,s.expires_at,s.revoked FROM playback_sessions ps JOIN catalog_entities e ON e.id=ps.item_id JOIN authorization_access s ON s.hash=ps.session_hash WHERE ps.grant_hash=?`, identity.Digest(grant)).Scan(&aid, &item, &expires, &state, &p.Hash, &p.AccountID, &p.ProfileID, &p.Authority, &p.Role, &p.Epoch, &sessionExpires, &revoked)
	if e == nil && s.ContinueAuthorityTx != nil {
		e = tx.QueryRowContext(ctx, `SELECT value FROM configuration WHERE key='id'`).Scan(&p.ServerID)
	}
	if e == nil && s.ContinueAuthorityTx != nil {
		p, e = s.ContinueAuthorityTx(ctx, tx, p, item)
		if e == nil {
			e = tx.QueryRowContext(ctx, `SELECT expires_at,revoked FROM authorization_access WHERE hash=?`, p.Hash).Scan(&sessionExpires, &revoked)
		}
	}
	now := time.Now().UTC().Format(time.RFC3339)
	ended := state == "stopped" || state == "ended" || state == "failed" || revoked != 0 || expires <= now || sessionExpires <= now
	if errors.Is(e, compactcatalog.ErrBuilding) && !ended {
		// Authentication held; the item has an unpublished catalogue change:
		// retry, don't refuse the grant.
		return "", p, "", e
	}
	if e != nil || ended {
		if cause := ctx.Err(); cause != nil {
			return "", p, "", cause
		}
		return "", p, "", ErrGrantEnded
	}
	if p.Authority == "local" {
		var epoch int
		if tx.QueryRowContext(ctx, `SELECT epoch FROM accounts WHERE id=?`, p.AccountID).Scan(&epoch) != nil || epoch != p.Epoch {
			return "", p, "", ErrGrantEnded
		}
	}
	// Existing open descriptors may finish; a new grant/media open is not proof
	// that those readers retired and must not bypass the logical tombstone.
	if e = recordingAdmittedTx(ctx, tx, item); e != nil {
		return "", p, "", e
	}
	var changed int
	if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM playback_source_pins pin JOIN playback_sessions ps ON ps.id=pin.session_id JOIN catalog_assets a ON a.token=pin.asset_id WHERE ps.grant_hash=? AND NOT EXISTS(SELECT 1 FROM prepared_media_session_pins pp WHERE pp.session_id=ps.id) AND (ps.asset_id!=pin.asset_id OR a.size!=pin.size OR a.modified_ns!=pin.modified_ns)`, identity.Digest(grant)).Scan(&changed); e != nil {
		return "", p, "", e
	}
	if changed != 0 {
		return "", p, "", ErrStaleChapter
	}
	var isPrepared bool
	if e = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM prepared_media_session_pins pp JOIN playback_sessions ps ON ps.id=pp.session_id WHERE ps.grant_hash=?)`, identity.Digest(grant)).Scan(&isPrepared); e != nil {
		return "", p, "", e
	}
	if !isPrepared && s.StorageGuard != nil {
		var path string
		if e = tx.QueryRowContext(ctx, `SELECT path FROM catalog_assets WHERE token=?`, aid).Scan(&path); e != nil {
			return "", p, "", e
		}
		if e = s.StorageGuard(path); e != nil {
			return "", p, "", e
		}
	}
	return aid, p, item, nil
}
func (s *Service) Cleanup() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.discFactory != nil {
		_ = s.discFactory.sweepExpired(ctx)
	}
	if s.discInputs != nil {
		s.discInputs.cleanup.Lock()
		defer s.discInputs.cleanup.Unlock()
	}
	gate, err := dbwork.Begin(ctx, s.db, dbwork.ClassEstablishedPlayback)
	if err != nil {
		return err
	}
	defer gate.Rollback()
	next, err := s.cleanupDiscInputsTx(gate.Context(), gate.Tx())
	if err != nil {
		return err
	}
	// A private snapshot's FK intentionally retains its legacy occurrence
	// until its own readers/references have retired. Missing optional reader
	// composition cannot turn this sweep into a cascade or discard custody.
	_, err = gate.Tx().ExecContext(gate.Context(), `DELETE FROM playback_sessions WHERE expires_at<? AND NOT EXISTS(SELECT 1 FROM playback_disc_snapshots d WHERE d.session_id=playback_sessions.id)`, time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	if err = gate.Commit(); err != nil {
		return err
	}
	if s.discInputs != nil {
		s.discInputs.after = next
	}
	return nil
}
func (s Session) String() string { return fmt.Sprintf("playback %s generation %d", s.ID, s.Generation) }

// streamURLTx is a presentation's stream URL and wire mode for its grant: the
// grant's own media, or the burned-subtitle rendition published for this
// generation.
func streamURLTx(tx *sql.Tx, id string, generation int, grant, mode string) (string, string) {
	url := "/v1/media/" + grant
	if mode == "hls" {
		url += "/master.m3u8"
	}
	if mode == "remote" {
		mode = "direct"
	}
	var renderID string
	if err := tx.QueryRow(`SELECT render_id FROM playback_subtitle_presentations WHERE session_id=? AND generation=?`, id, generation).Scan(&renderID); err == nil && renderID != "" {
		url, mode = "/v1/media/"+grant+"/subtitle-video/"+renderID+"/master.m3u8", "hls"
	}
	return url, mode
}

func (s *Service) ConfigureHLS(h *HLS) {
	s.hls = h
	if h != nil {
		h.subtitleService = s.Subtitles
		h.discPlayback = s
	}
}
func (s *Service) Ready(ctx context.Context, out Session) error {
	if out.PreparedVersionID != "" {
		if s.Prepared == nil {
			return ErrIncompatible
		}
		reader, handled, e := s.Prepared.OpenSession(ctx, out.ID)
		if e != nil {
			return e
		}
		if !handled {
			return ErrStaleOffer
		}
		return reader.Close()
	}
	if strings.Contains(out.StreamURL, "/subtitle-video/") {
		return nil
	}
	if out.Mode != "hls" || out.State == "stopped" || out.State == "ended" {
		return nil
	}
	if s.hls == nil {
		return ErrIncompatible
	}
	return s.hls.Ready(ctx, out.ID)
}

// Conversions reports conversion admission against the host's capacity, for
// diagnostics. A zero capacity means this service has no converter.
func (s *Service) Conversions() ConversionCapacity {
	if s == nil {
		return ConversionCapacity{}
	}
	return s.hls.Conversions()
}

func (s *Service) HLSFile(grant, name string) (string, error) {
	if s.hls == nil {
		return "", ErrIncompatible
	}
	return s.hls.File(grant, name)
}

func (s *Service) RemoteReadFailed(grant string) {
	_, _ = dbwork.ExecWrite(context.Background(), s.db, dbwork.ClassEstablishedPlayback, `UPDATE playback_sessions SET state='failed' WHERE grant_hash=? AND mode='remote' AND state NOT IN ('stopped','ended')`, identity.Digest(grant))
}

func (s *Service) HLSFileContext(ctx context.Context, grant, name string) (string, error) {
	if s.hls == nil {
		return "", ErrIncompatible
	}
	var id, profile string
	err := s.db.QueryRowContext(ctx, `SELECT p.id,d.profile FROM playback_sessions p JOIN playback_delivery_plans d ON d.session_id=p.id WHERE p.grant_hash=?`, identity.Digest(grant)).Scan(&id, &profile)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if profile == finiteDeliveryProfile {
		if s.hls.finite == nil {
			return "", ErrFiniteUnsupported
		}
		return s.hls.finite.file(ctx, id, name)
	}
	return s.hls.FileContext(ctx, grant, name)
}

// Preparing Off retains the occurrence and original source pin. This is not a
// new Create request; original HLS work is made attachable before grant rotation.
