package playback

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/playback/vod"
	"github.com/PorticoMediaServer/portico/server/internal/preparedmedia"
)

// DeliveryProfileLegacy is a server policy, not a claim that every client can
// decode these formats. It preserves the existing qualified delivery choices.
const finiteDeliveryProfile = vod.Policy

const DeliveryProfileLegacy = "portico_legacy_baseline_v1"

type DeliveryStrategy string

const (
	DeliveryOriginal        DeliveryStrategy = "original"
	DeliveryCopyRemux       DeliveryStrategy = "copy_remux"
	DeliveryAudioConversion DeliveryStrategy = "audio_conversion"
	DeliveryVideoConversion DeliveryStrategy = "video_conversion"
)

// Delivery reason codes. Every plan publishes the codes that produced it, and
// every refusal names the code that caused it. These strings are part of the
// wire contract: clients key their explanations off them, so they are added to
// rather than reworded.
const (
	// Chosen-route codes.
	ReasonDirectPlayCompatible      = "direct_play_compatible"
	ReasonDirectAudioCompatible     = "direct_audio_compatible"
	ReasonRemotePreflightCompatible = "remote_preflight_compatible"
	ReasonPreparedVersionCompatible = "prepared_version_compatible"
	ReasonContainerRequiresRemux    = "container_requires_remux"
	ReasonAudioCodecRequiresConvert = "audio_codec_requires_conversion"
	ReasonVideoCodecRequiresConvert = "video_codec_requires_conversion"
	ReasonHeightExceedsPolicy       = "height_exceeds_policy"
	ReasonBitrateExceedsPolicy      = "bitrate_exceeds_policy"
	ReasonHDRNotAllowed             = "hdr_tone_mapping_required"
	ReasonQualityRungSelected       = "quality_rung_selected"
	ReasonSubtitleBurnIn            = "subtitle_burn_in"
	ReasonFiniteNormalization       = "finite_timeline_normalization"
	ReasonAlternateAudioPolicy      = "alternate_audio_fixed_output_policy"

	// Preference and owner-policy codes.
	ReasonDirectPlayPreferred     = "direct_play_preferred"
	ReasonDirectStreamPreferred   = "direct_stream_preferred"
	ReasonTranscodePreferred      = "transcode_preferred"
	ReasonPlanningMaximumFidelity = "planning_maximum_fidelity"
	ReasonPlanningCompatibility   = "planning_maximum_compatibility"
	ReasonPlanningMinimizeWork    = "planning_minimize_server_work"
	ReasonHardwareBackend         = "hardware_backend_selected"
	ReasonSoftwareFallback        = "software_encode_fallback"

	// Refusal codes. A refusal always carries exactly one of these.
	ReasonDirectPlayRequired     = "direct_play_required_but_unavailable"
	ReasonDirectStreamRequired   = "direct_stream_required_but_unavailable"
	ReasonDirectPlayRefused      = "direct_play_refused_by_preference"
	ReasonDirectStreamRefused    = "direct_stream_refused_by_preference"
	ReasonTranscodeRefused       = "transcode_refused_by_preference"
	ReasonTranscodeDisabledOwner = "transcode_disabled_by_owner"
	ReasonNoAdmissibleRoute      = "no_admissible_delivery_route"
)

// ErrDeliveryRefused is a policy refusal, not a failure. Its reason code names
// the preference or clamp that closed every route.
type ErrDeliveryRefused struct{ Code string }

func (e ErrDeliveryRefused) Error() string {
	switch e.Code {
	case ReasonDirectPlayRequired:
		return "This source cannot play directly, and direct play is required for this network."
	case ReasonDirectStreamRequired:
		return "This source cannot be repackaged without conversion, and direct stream is required for this network."
	case ReasonTranscodeRefused, ReasonTranscodeDisabledOwner:
		return "This source needs conversion, which is not permitted for this network."
	}
	return "This server cannot prepare a version of this title that this device can play."
}

type BurnInSpec struct {
	ResourceID string `json:"resourceId"`
	Revision   int64  `json:"revision"`
	Format     string `json:"format"`
}

type DeliveryPlan struct {
	AirPlay             bool                      `json:"airPlay,omitempty"`
	ConversionToolchain *decoder.ToolchainFacts   `json:"-"`
	ConversionTarget    *decoder.ConversionTarget `json:"-"`
	ConversionProbe     *decoder.HardwareProbe    `json:"-"`
	BurnIn              *BurnInSpec               `json:"burnIn,omitempty"`
	TextRenditions      []TextRenditionPlan       `json:"-"`
	Renditions          []AudioRenditionPlan      `json:"audioRenditions,omitempty"`
	Prepared            *PreparedDelivery         `json:"prepared,omitempty"`
	Policy              *ResolvedDeliveryPolicy   `json:"policy,omitempty"`
	Trace               *DecisionTrace            `json:"trace,omitempty"`
	SourceSize          int64                     `json:"-"`
	SourceModifiedNS    int64                     `json:"-"`
	Profile             string                    `json:"profile"`
	SourceID            string                    `json:"sourceId"`
	FactsRevision       int64                     `json:"factsRevision"`
	SourceContainer     string                    `json:"sourceContainer"`
	SourceVideoCodec    string                    `json:"sourceVideoCodec"`
	SourceAudioCodec    string                    `json:"sourceAudioCodec"`
	Duration            float64                   `json:"duration"`
	Mode                string                    `json:"mode"`
	Strategy            DeliveryStrategy          `json:"strategy"`
	Reason              string                    `json:"reason"`
	ReasonCodes         []string                  `json:"reasonCodes"`
	OutputContainer     string                    `json:"outputContainer"`
	VideoAction         string                    `json:"videoAction"`
	AudioAction         string                    `json:"audioAction"`
	OutputVideoCodec    string                    `json:"outputVideoCodec"`
	OutputAudioCodec    string                    `json:"outputAudioCodec"`
	AudioChannels       int                       `json:"audioChannels"`
	QualityID           string                    `json:"qualityId"`
	TargetWidth         int                       `json:"targetDisplayWidth,omitempty"`
	TargetHeight        int                       `json:"targetDisplayHeight,omitempty"`
	VideoBitrateBPS     int                       `json:"videoBitrateBps,omitempty"`
	AudioBitrateBPS     int                       `json:"audioBitrateBps,omitempty"`
	ToneMap             bool                      `json:"toneMap"`
	// ToneMapOff says the picture is an HDR one converted without tone
	// mapping because the owner turned tone mapping off.
	ToneMapOff       bool   `json:"toneMapOff,omitempty"`
	ToneMapAlgorithm string `json:"toneMapAlgorithm,omitempty"`
	HardwareBackend  string `json:"hardwareBackend"`
	// AudioStream is the absolute source stream index the session plays, -1 when
	// the source has no audio or the plan predates track choice.
	AudioStream    int     `json:"audioStream"`
	Deinterlace    bool    `json:"deinterlace,omitempty"`
	MaxFrameRate   float64 `json:"maxFrameRate,omitempty"`
	Downmix        string  `json:"downmix,omitempty"`
	SoftwarePreset string  `json:"-"`
	HardwareDevice string  `json:"-"`
	HardwareNative bool    `json:"-"`
	// Target is the ceilings a v1 presentation was planned under. Its quality
	// id names no published rung, so a later re-plan of the same presentation
	// (a subtitle burned in or turned off) plans under these again.
	Target *QualityTarget `json:"-"`
	// TargetReach is whether admission called the connection this plan was
	// made for "remote" or "local". The remote ceilings follow it on a later
	// re-plan; a v1 plan stored without it is treated as remote.
	TargetReach string `json:"-"`
	// AudioSampleRate is the explicit converted rate, including supported
	// source rates. An unknown rate selects a device-supported rate.
	AudioSampleRate int `json:"-"`
	// Engine is the player engine the plan was made for: native or mpv. The
	// device plays this presentation with that engine.
	Engine string `json:"engine"`
	// OutputBitDepth and OutputRange describe a converted picture: 8-bit "sdr"
	// today, 10-bit "hdr10" or "hlg" where the converter keeps HDR.
	OutputBitDepth int    `json:"outputBitDepth,omitempty"`
	OutputRange    string `json:"outputRange,omitempty"`
	// AudioLossless is true when converted audio loses nothing of the source.
	AudioLossless bool `json:"audioLossless,omitempty"`
	// AudioPassthrough is true when the device hands the delivered audio to a
	// receiver undecoded.
	AudioPassthrough bool `json:"audioPassthrough,omitempty"`
}

// DownmixITULimited names the one downmix the server performs: the ITU-R BS.775
// matrix, normalised so it cannot clip whatever sample format the decoder used,
// three decibels of make-up gain, and a limiter for the rare full-scale passage.
const DownmixITULimited = "itu_limited"

// DeliveryInput is everything planning needs about one source, one device and
// the viewer's resolved position. Planning is a pure function of it, which is
// what makes the decision matrix testable without a database.
type DeliveryInput struct {
	Disc   bool
	BurnIn bool
	Server *decoder.ToolchainFacts
	// Source and Client are the full facts. When Source is nil the flat fields
	// below describe a source whose detail was never observed, and a zero Client
	// is the built-in baseline; both are how older call sites and tests read.
	Source *DeliverySource
	Client ClientProfile

	SourceID       string
	Container      string
	VideoCodec     string
	AudioCodec     string
	Duration       float64
	Height         int
	ColorTransfer  string
	ColorPrimaries string
	Remote         bool

	// AudioStream is the viewer's explicit track (absolute stream index), or -1.
	// The zero value also means "no explicit choice" when HasAudioChoice is false.
	AudioStream             int
	HasAudioChoice          bool
	PreferredAudioLanguages []string
	// OriginalLanguage is the title's published catalogue language, supplied
	// only by server composition; it is never a client or stream-default fact.
	OriginalLanguage       string
	AudioTrackChoice       string
	PreferAudioDescription bool
	// CarriedAudioLanguage is the language of the audio track picked in the
	// player earlier in this playback (a queue continued); it outranks the
	// preferred list.
	CarriedAudioLanguage string
	// Excluded routes were reported broken by this device for this source.
	Excluded map[DeliveryStrategy]RouteRejection
	// FMP4Output says this server build can produce fragmented MP4 HLS.
	FMP4Output bool
	// NoHLS says this server has no converter (ffmpeg was not found), so only the
	// original route exists. The zero value keeps HLS available for callers and
	// tests that never think about it.
	NoHLS bool

	Policy             ResolvedDeliveryPolicy
	Target             QualityTarget
	Config             DeliveryConfiguration
	TranscodingEnabled bool
	Hardware           decoder.HardwareProbe

	// Device is the device's whole capability document. When it is set planning
	// chooses the engine and Client is ignored; Engine pins one (the re-plan of
	// a presentation that is already playing in that engine).
	Device *CapabilityDocument
	Engine string
	// ExcludedByEngine is Excluded per engine, for a plan that chooses one.
	ExcludedByEngine map[string]map[DeliveryStrategy]RouteRejection
	// SubtitleKind is the kind of the subtitle the viewer chose for this
	// presentation (ass, pgs, vobsub, dvb), empty for none or for text that is
	// delivered beside the stream. An engine that cannot draw the kind itself
	// would need it burned into the picture, which weighs in the engine choice.
	SubtitleKind string
	// SubtitleBesideStream says the chosen subtitle can be handed to the device
	// as it is beside a stream, so an engine that draws its kind needs no
	// burn-in even when the file is repackaged or converted.
	SubtitleBesideStream bool
	// Outputs overrides what the converter can produce (tests); nil reads the
	// server's current outputs.
	Outputs *ConversionOutputs
}

type routeCost int

const (
	costDirect routeCost = iota
	costRemux
	costAudioConversion
	costVideoConversion
)

type candidate struct {
	strategy  DeliveryStrategy
	cost      routeCost
	mode      string
	transport string
	codes     []string
	rank      int
}

// assumedBitrate is what a file of unknown bitrate is taken to need, by the
// height of its picture: a generous ordinary rate, so a small ceiling converts
// the file and a roomy one does not. An unknown height is taken as 1080p.
func assumedBitrate(height int) int64 {
	switch {
	case height > 1440:
		return 40_000_000
	case height > 1080:
		return 20_000_000
	case height > 720 || height <= 0:
		return 12_000_000
	case height > 480:
		return 6_000_000
	}
	return 3_000_000
}

func bitrateOver(actual int64, ceiling int) bool {
	// Ten percent of tolerance: an average rate is not a peak, and a file a hair
	// over a round-number ceiling is not worth a conversion.
	return ceiling > 0 && actual > 0 && actual > int64(ceiling)+int64(ceiling)/10
}

// planDelivery chooses one route for a source, for one device, under a resolved
// policy, and records why every cheaper route was not taken.
//
// The order of preference is the fewest conversions that satisfies the source,
// the device and the policy. Planning policy only breaks ties: maximum_fidelity
// and minimize_server_work both take the cheapest admissible route;
// maximum_compatibility deliberately takes the most converted admissible route,
// because the most normalized output is the most widely playable.
func planDelivery(in DeliveryInput) (p DeliveryPlan, err error) {
	if in.Device != nil {
		return planDevice(in)
	}
	cfg := in.Config.Normalized()
	outputs := currentConversionOutputs()
	if in.Server != nil {
		outputs = decoder.AvailableConversionOutputsForHardware(in.Server, in.Hardware)
	}
	if in.Outputs != nil {
		outputs = *in.Outputs
	}
	if cfg.OutputFormat == "h264" {
		filtered := []ConversionVideoOutput{}
		for _, o := range outputs.Video {
			if o.Codec == "h264" {
				filtered = append(filtered, o)
			}
		}
		outputs.Video = filtered
	}
	source := legacySource(in)
	if in.Source != nil {
		source = *in.Source
	}
	if in.Remote || source.Remote {
		// The server opens a STRM, never the player (JUSTIN #7). It is planned
		// from the facts the server measured like any other file; an original
		// it can relay as it is goes through the remote relay, and every other
		// route reads the remote window input, which the source container
		// "strm" selects.
		defer func() {
			p.SourceContainer = "strm"
			if err == nil && p.Mode == "direct" {
				p.Mode = "remote"
				p.ReasonCodes = append(p.ReasonCodes, ReasonRemotePreflightCompatible)
			}
		}()
	}
	client := in.Client
	if client.Version == 0 {
		client = BaselineClientProfile()
	}
	p = DeliveryPlan{
		Profile: DeliveryProfileLegacy, SourceID: source.ID, SourceContainer: source.Container, Duration: source.Duration,
		Mode: "direct", Strategy: DeliveryOriginal, Reason: ReasonDirectPlayCompatible,
		OutputContainer: source.Container, VideoAction: "original", AudioAction: "original",
		QualityID: in.Target.RungID, SoftwarePreset: cfg.SoftwarePreset,
		ToneMapAlgorithm: cfg.ToneMapAlgorithm, HardwareBackend: string(decoder.BackendSoftware), AudioStream: -1,
		Engine: client.Engine,
	}
	if p.Engine == "" {
		p.Engine = EngineNative
	}
	if p.QualityID == "" {
		p.QualityID = "original"
	}
	defer func() {
		if err == nil {
			if p.Trace != nil && p.Trace.Video != nil && source.Video != nil {
				v := source.Video
				p.Trace.Video.OutputRange = effectiveRange(v, client.videoCodec(v.Codec), client)
				if p.VideoAction == "copy" && v.DolbyVisionProfile == 7 && !profile7CopyThrough(v, client, p.OutputContainer) {
					p.Trace.Video.OutputRange = assets.RangeHDR10
				}
				if p.VideoAction == "convert" {
					p.Trace.Video.OutputRange = assets.RangeSDR
					if p.OutputRange != "" {
						p.Trace.Video.OutputRange = p.OutputRange
					}
				}
				// The engine, not the display, turns this HDR picture into
				// what the screen shows.
				if shown := p.Trace.Video.OutputRange; shown != assets.RangeSDR && client.ToneMapsHDR && len(client.Display.DynamicRanges) > 0 && !containsString(client.Display.DynamicRanges, shown) {
					p.Trace.Notes = append(p.Trace.Notes, NoteDeviceToneMaps)
				}
			}
			// Conversion drops HDR10+ metadata. A copied picture keeps it
			// only on a device that declares HDR10+ (VIDEO.md VS-2b).
			if v := videoOf(source); v != nil && v.HDR10Plus && (p.VideoAction == "convert" || !declaresHDR10Plus(client, p.OutputVideoCodec)) {
				p.Trace.Notes = append(p.Trace.Notes, NoteHDR10PlusLost)
			}
			// A track that reaches the device as it is in the file, and that
			// the device hands on to a receiver rather than decoding.
			if selected := sourceAudioAt(source.Audio, p.AudioStream); selected != nil && p.AudioAction != "convert" && client.audioCodec(selected.Codec) == nil && audioPassedThrough(selected, client) {
				p.AudioPassthrough = true
				if p.Trace != nil {
					p.Trace.Notes = append(p.Trace.Notes, NoteAudioPassthrough)
				}
			}
			// A track copied to an engine that decodes the codec but not its
			// objects plays only the channel bed. The report must say so rather
			// than claim Atmos or DTS:X on the delivered track. When the engine
			// could also pass the track to a receiver intact, which path it takes
			// is unknown until the renderer reports it, so no loss is claimed.
			if selected := sourceAudioAt(source.Audio, p.AudioStream); selected != nil && selected.ObjectAudio != "" && p.AudioAction != "convert" && !p.AudioPassthrough && !audioPassedThrough(selected, client) {
				if c := client.audioCodec(p.OutputAudioCodec); c == nil || !containsString(c.ObjectAudio, selected.ObjectAudio) {
					if p.Trace != nil {
						p.Trace.Notes = append(p.Trace.Notes, NoteObjectAudioDecoded)
					}
				}
			}
			if p.VideoAction == "convert" || p.AudioAction == "convert" || p.VideoAction == "copy" && videoOf(source) != nil && videoOf(source).DynamicRange == assets.RangeDolbyVision && p.Trace.Video.OutputRange != assets.RangeDolbyVision {
				err = finishConversionTarget(&p, videoOf(source), sourceAudioAt(source.Audio, p.AudioStream), client, in.Hardware, in.Server)
			}
			// A copied picture is delivered at the source's own depth and
			// range, which is what the manifest and the panel report.
			if err == nil && p.VideoAction != "convert" && p.Trace != nil && p.Trace.Video != nil && p.OutputRange == "" {
				p.OutputRange, p.OutputBitDepth = p.Trace.Video.OutputRange, p.Trace.Video.BitDepth
			}
			if err == nil {
				p.Renditions = planAudioRenditions(p, source, client, in.Target, cfg.AudioConversionAllowed(in.TranscodingEnabled) && in.Policy.Transcode != "never")
			}
		}
	}()
	policy := in.Policy
	p.Policy = &policy
	trace := &DecisionTrace{Version: 1, Client: client.Summary(), Container: source.Container, BitRate: source.BitRate, Routes: []RouteEvaluation{}, Notes: []string{}}
	p.Trace = trace
	if client.Evidence == EvidenceBuiltin {
		trace.Notes = append(trace.Notes, NoteBaselineProfile)
	}
	if client.Overrides.ForceStereo {
		trace.Notes = append(trace.Notes, NoteOverrideStereo)
	}
	if client.Overrides.DisablePassthrough {
		trace.Notes = append(trace.Notes, NoteOverrideNoPassthrough)
	}
	if client.Overrides.DisableHDR {
		trace.Notes = append(trace.Notes, NoteOverrideNoHDR)
	}
	if in.Source != nil && !source.DetailKnown {
		trace.Notes = append(trace.Notes, NoteDetailUnobserved)
	}

	explicit := -1
	if in.HasAudioChoice {
		explicit = in.AudioStream
	}
	audio, chosenBy := chooseAudioWithPolicy(source.Audio, explicit, in.CarriedAudioLanguage, in.PreferredAudioLanguages, in.OriginalLanguage, in.AudioTrackChoice, in.PreferAudioDescription, client)
	var defaultAudio *SourceAudio
	if len(source.Audio) > 0 {
		defaultAudio = &source.Audio[0]
		for i := range source.Audio {
			if source.Audio[i].Default {
				defaultAudio = &source.Audio[i]
				break
			}
		}
	}
	video := source.Video
	if video != nil {
		p.SourceVideoCodec, p.OutputVideoCodec = video.Codec, video.Codec
		trace.Video = traceVideoSource(video)
	}
	if audio != nil {
		p.SourceAudioCodec, p.OutputAudioCodec = audio.Codec, audio.Codec
		p.AudioStream = audio.Index
		trace.Audio = traceAudioSource(audio, chosenBy, len(source.Audio))
	}
	audioOnly := video == nil
	height := 0
	if video != nil {
		height = video.Height
	}
	hdr := video != nil && video.DynamicRange != "" && video.DynamicRange != assets.RangeSDR
	if video != nil && video.DynamicRange == assets.RangeDolbyVision && dolbyVisionFallback(video) == assets.RangeSDR {
		hdr = false
	}
	overHeight := in.Target.MaxVideoHeight > 0 && height > in.Target.MaxVideoHeight
	ceiling := in.Target.MaxVideoBitrateBPS
	if client.MaxBitrateBPS > 0 && (ceiling == 0 || client.MaxBitrateBPS < ceiling) {
		ceiling = client.MaxBitrateBPS
		trace.Notes = append(trace.Notes, NoteDeviceBitrate)
	}
	sourceRate := max(source.BitRate, source.MaxBitRate)
	if sourceRate == 0 && ceiling > 0 && video != nil && source.DetailKnown {
		// A file whose streams were read but whose bitrate was never observed (no
		// size or no duration) must not pass every ceiling on that account: it is
		// taken to be an ordinary file of its picture size. A file not analysed
		// yet is left alone: its picture is itself a guess, and it may be a song.
		sourceRate = assumedBitrate(height)
		trace.Notes = append(trace.Notes, NoteBitrateAssumed)
	}
	overBitrate := bitrateOver(sourceRate, ceiling)

	// Rules that close every route which leaves the picture as it is.
	var pictureRules []RouteRejection
	if in.BurnIn {
		pictureRules = append(pictureRules, RouteRejection{ReasonSubtitleBurnIn, ""})
	}

	if overHeight {
		pictureRules = append(pictureRules, RouteRejection{ReasonHeightExceedsPolicy, fmt.Sprintf("%d>%d", height, in.Target.MaxVideoHeight)})
	}
	if overBitrate {
		pictureRules = append(pictureRules, RouteRejection{ReasonBitrateExceedsPolicy, fmt.Sprintf("%d>%d", sourceRate, ceiling)})
	}

	candidates := []candidate{}
	evaluate := func(route DeliveryStrategy, transport string, rejections []RouteRejection) bool {
		if rejections == nil {
			rejections = []RouteRejection{}
		}
		if in.NoHLS {
			// The device may well take HLS; it is this server that cannot make it.
			for i := range rejections {
				if rejections[i].Code == RejectTransport {
					rejections[i] = RouteRejection{RejectServerOutput, "no converter on this server"}
				}
			}
		}
		trace.Routes = append(trace.Routes, RouteEvaluation{Route: string(route), Transport: transport, Admissible: len(rejections) == 0, Rejections: rejections})
		return len(rejections) == 0
	}
	excluded := func(route DeliveryStrategy) []RouteRejection {
		if r, ok := in.Excluded[route]; ok {
			return []RouteRejection{{RejectClientFailure, r.Code}}
		}
		return nil
	}

	var audioRules []RouteRejection
	if audio != nil && bitrateOver(max(audio.BitRate, audio.MaxBitRate), in.Target.MaxAudioBitrateBPS) {
		audioRules = append(audioRules, RouteRejection{ReasonBitrateExceedsPolicy, fmt.Sprintf("audio %d>%d", max(audio.BitRate, audio.MaxBitRate), in.Target.MaxAudioBitrateBPS)})
	}

	// Route 1: the original file.
	{
		rejections := excluded(DeliveryOriginal)
		if in.Disc {
			rejections = append(rejections, RouteRejection{RejectContainer, "disc_reader_required"})
		}
		rejections = append(rejections, audioRules...)
		rejections = append(rejections, pictureRules...)
		if policy.DirectPlay == "never" {
			rejections = append(rejections, RouteRejection{ReasonDirectPlayRefused, ""})
		}
		var best []RouteRejection
		matched := false
		for _, t := range client.Transports {
			if t.Transport != TransportDirect || !containersAllow(t.Containers, source.Container) {
				continue
			}
			var r []RouteRejection
			if video != nil {
				r = append(r, videoRejections(video, client, t, in.Target.AllowHDR, false)...)
			}
			if audio != nil {
				r = append(r, audioRejections(audio, client, t)...)
				// Without embedded switching, the original plays its engine-default track.
				if len(source.Audio) > 1 && !client.EmbeddedAudioSwitching && audio.Index != defaultAudio.Index {
					r = append(r, RouteRejection{RejectAudioTrack, fmt.Sprintf("track %d", audio.Ordinal+1)})
				}
			}
			if !matched || len(r) < len(best) {
				best, matched = r, true
			}
			if len(r) == 0 {
				break
			}
		}
		if !matched {
			rejections = append(rejections, RouteRejection{RejectContainer, source.Container})
		} else {
			rejections = append(rejections, best...)
		}
		if evaluate(DeliveryOriginal, TransportDirect, rejections) {
			code := ReasonDirectPlayCompatible
			if audioOnly {
				code = ReasonDirectAudioCompatible
			}
			candidates = append(candidates, candidate{DeliveryOriginal, costDirect, "direct", TransportDirect, []string{code}, 0})
		} else if policy.DirectPlay == "require" {
			p.ReasonCodes = rejectionCodes(rejections)
			return p, ErrDeliveryRefused{Code: ReasonDirectPlayRequired}
		}
	}

	transports := hlsTransports(client, in.FMP4Output, video)
	if in.NoHLS {
		// This server has no converter at all: every HLS route closes with the
		// same reason instead of being planned and then failing to start.
		transports = nil
	}
	conversionAllowed := cfg.AudioConversionAllowed(in.TranscodingEnabled) && policy.Transcode != "never"

	// Route 2: both streams copied into HLS.
	remuxable := false
	if !audioOnly {
		rejections := excluded(DeliveryCopyRemux)
		rejections = append(rejections, audioRules...)
		rejections = append(rejections, pictureRules...)
		switch {
		case policy.DirectStream == "never" && !cfg.RemuxEnabled:
			rejections = append(rejections, RouteRejection{RejectOwnerRemuxDisabled, ""})
		case policy.DirectStream == "never":
			rejections = append(rejections, RouteRejection{ReasonDirectStreamRefused, ""})
		case !cfg.RemuxEnabled:
			rejections = append(rejections, RouteRejection{RejectOwnerRemuxDisabled, ""})
		}
		transport, r := bestSourceCopyTransport(transports, client, video, audio, in.Target.AllowHDR, in.Server)
		rejections = append(rejections, r...)
		// remuxable describes the source and the device, not the preferences:
		// it is what decides which refusal a closed policy is reported as.
		remuxable = len(r) == 0 && len(pictureRules) == 0 && len(audioRules) == 0
		if evaluate(DeliveryCopyRemux, transport, rejections) {
			candidates = append(candidates, candidate{DeliveryCopyRemux, costRemux, "hls", transport, []string{ReasonContainerRequiresRemux}, 0})
		} else if policy.DirectStream == "require" && len(candidates) == 0 {
			p.ReasonCodes = rejectionCodes(rejections)
			return p, ErrDeliveryRefused{Code: ReasonDirectStreamRequired}
		}
	}

	// Route 3: video copied, audio converted.
	if !audioOnly {
		rejections := excluded(DeliveryAudioConversion)
		transport, r := bestSourceAudioConversionTransport(transports, client, video, audio, in.Target, outputs, in.Server)
		converted := audioOutputFor(audio, client, in.Target, transportNamed(transports, transport), outputs)
		rejections = append(rejections, serverConversionRejections(in.Server, in.Hardware, nil, audio, "", converted.Codec, false)...)
		if audio != nil && converted.NoRate {
			// Nothing this server converts audio to is at a rate the device takes.
			rejections = append(rejections, RouteRejection{RejectAudioSampleRate, strconv.Itoa(audio.SampleRate)})
		}
		if t := transportNamed(transports, transport); audio != nil && t != nil && !containsString(t.Audio, converted.Codec) {
			// The device takes none of this server's audio outputs on the
			// transport that carries the picture.
			rejections = append(rejections, RouteRejection{RejectAudioCodec, converted.Codec + " in " + transport})
		}
		rejections = append(rejections, pictureRules...)
		if !conversionAllowed {
			rejections = append(rejections, RouteRejection{RejectTranscodingDisabled, ""})
		}
		if audio == nil {
			rejections = append(rejections, RouteRejection{RejectNoAudio, ""})
		}
		rejections = append(rejections, r...)
		if evaluate(DeliveryAudioConversion, transport, rejections) {
			candidates = append(candidates, candidate{DeliveryAudioConversion, costAudioConversion, "hls", transport, []string{ReasonAudioCodecRequiresConvert}, 0})
		}
	}

	// Route 4: full conversion (or, for an audio file, audio conversion).
	var picture videoOutput
	{
		var rejections []RouteRejection
		transport := ""
		if audioOnly {
			transport, _ = bestAudioConversionTransport(transports, client, nil, audio, in.Target, outputs)
		} else if chosen, ok := chooseVideoOutput(video, client, transports, in.Target.AllowHDR, outputs, audio, in.Target); ok {
			picture, transport = chosen, chosen.Transport
		}
		carrier := transportNamed(transports, transport)
		audioToDecode := audio
		if audio != nil && carrier != nil && len(audioRejections(audio, client, *carrier)) == 0 && serverCopyAudio(transport, audio.Codec) && !bitrateOver(audio.BitRate, in.Target.MaxAudioBitrateBPS) && in.Source != nil && !audioOnly {
			audioToDecode = nil
		}
		hardware := in.Hardware
		if in.BurnIn {
			hardware.NativeFilters = false
		}
		converted := audioOutputFor(audio, client, in.Target, carrier, outputs)
		// The owner's tone mapping switch does not take a title away: with it
		// off an HDR picture is still converted for a device that needs a
		// standard one, unmapped, and the plan's trace says why it looks flat.
		if video != nil && video.DynamicRange == assets.RangeDolbyVision && dolbyVisionFallback(video) == "" && (in.Server == nil || !in.Server.VulkanAvailable) {
			rejections = append(rejections, RouteRejection{RejectDolbyVision, "no qualified reshape route"})
		}
		rejections = append(rejections, serverConversionRejections(in.Server, hardware, video, audioToDecode, picture.Codec, converted.Codec, hdr && picture.Range == assets.RangeSDR && cfg.ToneMapping)...)
		if audioToDecode != nil && converted.NoRate {
			rejections = append(rejections, RouteRejection{RejectAudioSampleRate, strconv.Itoa(audioToDecode.SampleRate)})
		}
		if audioToDecode != nil && carrier != nil && !containsString(carrier.Audio, converted.Codec) {
			rejections = append(rejections, RouteRejection{RejectAudioCodec, converted.Codec + " in " + transport})
		}
		if !conversionAllowed || !audioOnly && !cfg.VideoConversionAllowed(in.TranscodingEnabled) {
			rejections = append(rejections, RouteRejection{RejectTranscodingDisabled, ""})
		}
		if transport == "" {
			for _, t := range transports {
				out := audioOutputFor(audio, client, in.Target, &t, outputs)
				if audio != nil && out.NoRate && !out.NoOutput {
					rejections = append(rejections, RouteRejection{RejectAudioSampleRate, strconv.Itoa(audio.SampleRate)})
					break
				}
			}
			rejections = append(rejections, RouteRejection{RejectTransport, TransportHLSTS})
		}
		route := DeliveryVideoConversion
		if audioOnly {
			route = DeliveryAudioConversion
		}
		if evaluate(route, transport, rejections) {
			// Every applicable reason is published, not only the first: a 4K HDR
			// HEVC title on a cellular lane is converted for several separate
			// reasons, and a client that shows one of them misexplains the picture.
			codes := []string{}
			if in.BurnIn {
				codes = append(codes, ReasonSubtitleBurnIn)
			}
			if audioOnly {
				codes = append(codes, ReasonAudioCodecRequiresConvert)
			} else {
				copyRoute := routeEvaluation(trace, DeliveryAudioConversion)
				if copyRoute != nil && hasRejection(copyRoute.Rejections, RejectVideoCodec, RejectVideoProfile, RejectVideoLevel, RejectVideoBitDepth, RejectResolution, RejectFrameRate, RejectVideoBitrate, RejectInterlaced, RejectRotation, RejectDynamicRange, RejectDolbyVision) {
					codes = append(codes, ReasonVideoCodecRequiresConvert)
				}
				if hdr && (!in.Target.AllowHDR || (copyRoute != nil && hasRejection(copyRoute.Rejections, ReasonHDRNotAllowed))) {
					codes = append(codes, ReasonHDRNotAllowed)
				}
			}
			if overHeight {
				codes = append(codes, ReasonHeightExceedsPolicy)
			}

			if overBitrate {
				codes = append(codes, ReasonBitrateExceedsPolicy)
			}
			if len(codes) == 0 {
				codes = append(codes, ReasonVideoCodecRequiresConvert)
			}
			candidates = append(candidates, candidate{route, costVideoConversion, "hls", transport, codes, 0})
		}
	}

	// The audio-track rule is a preference, not an impossibility: when no
	// other route is admissible, play the engine-default track if the matching
	// direct transport can decode it. The trace reports the substituted track.
	if len(candidates) == 0 {
		if original := routeEvaluation(trace, DeliveryOriginal); original != nil && len(original.Rejections) == 1 && original.Rejections[0].Code == RejectAudioTrack && defaultAudio != nil {
			for _, t := range client.Transports {
				if t.Transport != TransportDirect || !containersAllow(t.Containers, source.Container) || len(audioRejections(defaultAudio, client, t)) > 0 || bitrateOver(max(defaultAudio.BitRate, defaultAudio.MaxBitRate), in.Target.MaxAudioBitrateBPS) {
					continue
				}
				if video != nil && len(videoRejections(video, client, t, in.Target.AllowHDR, false)) > 0 {
					continue
				}
				original.Admissible = true
				trace.Notes = append(trace.Notes, NoteFirstAudioTrackPlayed)
				audio = defaultAudio
				p.SourceAudioCodec, p.OutputAudioCodec, p.AudioStream = audio.Codec, audio.Codec, audio.Index
				trace.Audio = traceAudioSource(audio, "only_reachable_track", len(source.Audio))
				candidates = append(candidates, candidate{DeliveryOriginal, costDirect, "direct", TransportDirect, []string{ReasonDirectPlayCompatible}, 0})
				break
			}
		}
	}
	if len(candidates) == 0 {
		code := ReasonTranscodeRefused
		if !cfg.AudioConversionAllowed(in.TranscodingEnabled) || !audioOnly && !cfg.VideoConversionAllowed(in.TranscodingEnabled) {
			code = ReasonTranscodeDisabledOwner
		} else if policy.DirectStream == "never" && remuxable {
			code = ReasonDirectStreamRefused
		} else if policy.Transcode != "never" {
			code = ReasonNoAdmissibleRoute
		}
		return p, ErrDeliveryRefused{Code: code}
	}

	// Ranking. Explicit preferences outrank the planning policy, because a
	// viewer's "prefer" is a statement about this device, not about this server.
	for i := range candidates {
		c := &candidates[i]
		switch {
		case policy.DirectPlay == "prefer" && c.cost == costDirect:
			c.rank, c.codes = -1, append(c.codes, ReasonDirectPlayPreferred)
		case policy.DirectStream == "prefer" && c.cost == costRemux:
			c.rank, c.codes = -1, append(c.codes, ReasonDirectStreamPreferred)
		case (policy.Transcode == "prefer" || policy.Transcode == "require") && c.cost == costVideoConversion:
			c.rank, c.codes = -1, append(c.codes, ReasonTranscodePreferred)
		}
	}
	planning := cfg.PlanningPolicy
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].rank != candidates[j].rank {
			return candidates[i].rank < candidates[j].rank
		}
		if planning == PlanningMaximumCompatible {
			return candidates[i].cost > candidates[j].cost
		}
		return candidates[i].cost < candidates[j].cost
	})
	chosen := candidates[0]
	switch planning {
	case PlanningMaximumCompatible:
		chosen.codes = append(chosen.codes, ReasonPlanningCompatibility)
	case PlanningMinimizeServerWork:
		chosen.codes = append(chosen.codes, ReasonPlanningMinimizeWork)
	default:
		chosen.codes = append(chosen.codes, ReasonPlanningMaximumFidelity)
	}
	// A refused preference is part of the explanation even when another route
	// was admissible.
	if policy.DirectPlay == "never" {
		p.ReasonCodes = append(p.ReasonCodes, ReasonDirectPlayRefused)
	}
	if !audioOnly && (policy.DirectStream == "never" || !cfg.RemuxEnabled) && remuxable {
		p.ReasonCodes = append(p.ReasonCodes, ReasonDirectStreamRefused)
	}
	p.ReasonCodes = append(p.ReasonCodes, chosen.codes...)
	p.Mode, p.Strategy, p.Reason = chosen.mode, chosen.strategy, chosen.codes[0]
	if p.Mode == "hls" && p.Duration > 0 && !hlsDurationSupported(p.Duration) {
		return p, ErrDeliveryRefused{Code: "hls_duration_unsupported"}
	}
	for i := range trace.Routes {
		if trace.Routes[i].Route == string(chosen.strategy) && trace.Routes[i].Admissible {
			trace.Routes[i].Chosen = true
			break
		}
	}

	output := "mpegts_hls"
	if chosen.transport == TransportHLSFMP4 {
		output = "fmp4_hls"
	}
	convertAudio := func() {
		target := audioOutputFor(audio, client, in.Target, transportNamed(transports, chosen.transport), outputs)
		p.AudioAction, p.OutputAudioCodec, p.AudioChannels, p.AudioBitrateBPS = "convert", target.Codec, target.Channels, target.Bitrate
		p.AudioSampleRate, p.AudioLossless = target.SampleRate, target.Lossless
		if target.Downmix {
			p.Downmix = DownmixITULimited
		}
	}
	switch chosen.strategy {
	case DeliveryOriginal:
		return p, nil
	case DeliveryCopyRemux:
		p.OutputContainer = output
		p.VideoAction, p.AudioAction = "copy", "copy"
		if audio == nil {
			p.AudioAction = "none"
		}
		p.AudioChannels = 0
		return p, nil
	case DeliveryAudioConversion:
		p.OutputContainer = output
		convertAudio()
		if audioOnly {
			p.VideoAction, p.OutputVideoCodec = "none", ""
		} else {
			p.VideoAction = "copy"
		}
		return p, nil
	}
	p.OutputContainer = output
	p.VideoAction, p.OutputVideoCodec = "convert", picture.Codec
	p.OutputBitDepth, p.OutputRange = picture.BitDepth, picture.Range
	// The audio of a converted picture is still copied when the device takes it:
	// re-encoding a track the device can already decode only loses quality.
	if audio == nil {
		p.AudioAction, p.OutputAudioCodec = "none", ""
	} else if t := transportNamed(transports, chosen.transport); t != nil && len(audioRejections(audio, client, *t)) == 0 && serverCopyAudio(chosen.transport, audio.Codec) && !bitrateOver(audio.BitRate, in.Target.MaxAudioBitrateBPS) && in.Source != nil {
		p.AudioAction = "copy"
	} else {
		convertAudio()
	}
	encoded := client.videoCodec(picture.Codec)
	if encoded != nil && video != nil {
		point := encoded.forConversion(picture.BitDepth, video.Width, video.Height, video.FrameRate)
		encoded = &point
	}
	p.TargetHeight = in.Target.MaxVideoHeight
	if encoded != nil {
		p.TargetWidth = encoded.MaxWidth
	}
	if encoded != nil && encoded.MaxHeight > 0 && (p.TargetHeight == 0 || encoded.MaxHeight < p.TargetHeight) && height > encoded.MaxHeight {
		p.TargetHeight = encoded.MaxHeight
	}
	outHeight := height
	if p.TargetHeight > 0 {
		outHeight = min(outHeight, p.TargetHeight)
	}
	p.VideoBitrateBPS = decoder.CodecVideoBitrate("h264", outHeight)
	if ceiling > 0 {
		p.VideoBitrateBPS = min(p.VideoBitrateBPS, ceiling)
	}
	if picture.Codec == "hevc" {
		p.VideoBitrateBPS = p.VideoBitrateBPS * 7 / 10
	}
	if picture.Codec == "av1" {
		p.VideoBitrateBPS = p.VideoBitrateBPS * 6 / 10
	}
	if encoded != nil && encoded.MaxBitrateBPS > 0 {
		p.VideoBitrateBPS = min(p.VideoBitrateBPS, encoded.MaxBitrateBPS)
	}
	if !overHeight && in.Target.MaxVideoHeight == 0 && (p.QualityID == "original" || p.QualityID == "automatic" || p.QualityID == "") {
		p.VideoBitrateBPS = ceiling
		if encoded != nil {
			p.VideoBitrateBPS = narrowCeiling(p.VideoBitrateBPS, encoded.MaxBitrateBPS)
		}
	}
	// A conversion that keeps the source's HDR needs no tone mapping. One that
	// writes a standard picture from an HDR source does: left unmapped it is
	// simply wrong — grey and washed out — so the owner's switch is the only
	// thing that turns it off, and the trace says so.
	if hdr && picture.Range == assets.RangeSDR {
		if cfg.ToneMapping {
			p.ToneMap = true
		} else {
			p.ToneMapOff = true
			trace.Notes = append(trace.Notes, NoteToneMapOwnerOff)
		}
	}
	if video != nil {
		p.Deinterlace = video.Interlaced
		limit := 60.0
		if encoded != nil && encoded.MaxFrameRate > 0 {
			limit = encoded.MaxFrameRate
		}
		if video.FrameRate > limit+0.01 {
			p.MaxFrameRate = limit
		}
	}
	selectedHardware := measuredConversionProbe(in.Server, in.Hardware, picture.Codec, picture.BitDepth)
	p.HardwareDevice = selectedHardware.Device
	if selectedHardware.Available {
		p.HardwareBackend, p.HardwareNative = string(selectedHardware.Backend), selectedHardware.NativeFilters
		if !p.ToneMap {
			p.HardwareNative = p.HardwareNative || selectedHardware.ScaleFilters
		}
		p.ReasonCodes = append(p.ReasonCodes, ReasonHardwareBackend)
	} else {
		p.ReasonCodes = append(p.ReasonCodes, ReasonSoftwareFallback)
	}
	return p, nil
}

func traceAudioSource(audio *SourceAudio, chosenBy string, tracks int) *TraceAudio {
	if audio == nil {
		return nil
	}
	return &TraceAudio{Lossless: copiedBool(audio.Lossless), StreamIndex: audio.Index, Codec: audio.Codec, Channels: audio.Channels, Language: audio.Language, ObjectAudio: audio.ObjectAudio, Profile: audio.Profile, Variant: audio.AudioVariant, Layout: audio.Layout, SampleRate: audio.SampleRate, BitDepth: audio.BitsPerSample, ChosenBy: chosenBy, Tracks: tracks}
}

// sourceAudioAt is the source track with the given absolute stream index.
func sourceAudioAt(tracks []SourceAudio, index int) *SourceAudio {
	for i := range tracks {
		if tracks[i].Index == index {
			return &tracks[i]
		}
	}
	return nil
}

func transportNamed(transports []ClientTransport, name string) *ClientTransport {
	for i := range transports {
		if transports[i].Transport == name {
			return &transports[i]
		}
	}
	return nil
}

func routeEvaluation(t *DecisionTrace, route DeliveryStrategy) *RouteEvaluation {
	for i := range t.Routes {
		if t.Routes[i].Route == string(route) {
			return &t.Routes[i]
		}
	}
	return nil
}

func hasRejection(rejections []RouteRejection, codes ...string) bool {
	for _, r := range rejections {
		for _, c := range codes {
			if r.Code == c {
				return true
			}
		}
	}
	return false
}

func rejectionCodes(rejections []RouteRejection) []string {
	out := []string{}
	for _, r := range rejections {
		if !containsString(out, r.Code) {
			out = append(out, r.Code)
		}
	}
	return out
}

// bestCopyTransport finds the HLS transport that can carry the video (and, when
// audio is given, that audio track) untouched, both for the device and for this
// server's producers. It returns the transport with the fewest objections, so
// the trace names the nearest miss rather than an arbitrary one.
func bestCopyTransport(transports []ClientTransport, client ClientProfile, video *SourceVideo, audio *SourceAudio, allowHDR bool, copying bool) (string, []RouteRejection) {
	if len(transports) == 0 {
		return "", []RouteRejection{{RejectTransport, "hls"}}
	}
	name, best, found := "", []RouteRejection(nil), false
	for _, t := range transports {
		var r []RouteRejection
		if video != nil {
			r = append(r, videoRejections(video, client, t, allowHDR, copying)...)
			if !serverCopyVideo(t.Transport, video.Codec) && !hasRejection(r, RejectVideoCodec) {
				r = append(r, RouteRejection{RejectServerOutput, video.Codec + " in " + t.Transport})
			}
		}
		if audio != nil {
			r = append(r, audioRejections(audio, client, t)...)
			if !serverCopyAudio(t.Transport, audio.Codec) && !hasRejection(r, RejectAudioCodec) {
				r = append(r, RouteRejection{RejectServerOutput, audio.Codec + " in " + t.Transport})
			}
		}
		if !found || len(r) < len(best) {
			name, best, found = t.Transport, r, true
		}
		if len(r) == 0 {
			break
		}
	}
	return name, best
}

func audioBudget(t QualityTarget) int {
	if t.MaxAudioBitrateBPS > 0 {
		return t.MaxAudioBitrateBPS
	}
	return 192_000
}

// CodecArgs is consumed by every HLS producer.
func (p DeliveryPlan) CodecArgs() ([]string, []string, []decoder.HardwareStage, error) {
	graph, err := p.conversionGraph(nil)
	if err != nil {
		return nil, nil, nil, err
	}
	return graph.Input, append(append([]string{}, graph.Video...), graph.Audio...), graph.Stages, nil
}

func (p DeliveryPlan) conversionGraph(burn *decoder.BurnIn) (decoder.ConversionGraph, error) {
	if p.Profile != DeliveryProfileLegacy || p.Mode != "hls" {
		return decoder.ConversionGraph{}, errors.New("delivery plan does not describe ordinary HLS")
	}
	request := decoder.ConversionRequest{
		Target:           p.ConversionTarget,
		BurnIn:           burn,
		Toolchain:        decoder.CurrentToolchain(),
		Probe:            decoder.HardwareProbe{Backend: decoder.HardwareBackend(p.HardwareBackend), Available: p.HardwareBackend != "" && p.HardwareBackend != string(decoder.BackendSoftware), NativeFilters: p.HardwareNative, Device: p.HardwareDevice},
		ToneMap:          p.ToneMap,
		ToneMapAlgorithm: p.ToneMapAlgorithm,
		MaxHeight:        p.TargetHeight,
		MaxWidth:         p.TargetWidth,
		VideoBitrateBPS:  p.VideoBitrateBPS,
		AudioBitrateBPS:  p.AudioBitrateBPS,
		AudioChannels:    p.AudioChannels,
		AudioCodec:       p.OutputAudioCodec,
		SoftwarePreset:   p.SoftwarePreset,
		Deinterlace:      p.Deinterlace,
		MaxFrameRate:     p.MaxFrameRate,
		Downmix:          p.Downmix == DownmixITULimited,
		AudioSampleRate:  p.AudioSampleRate,
		SourceVideoCodec: p.SourceVideoCodec,
	}
	if p.ConversionToolchain != nil {
		request.Toolchain = p.ConversionToolchain
	}
	if p.ConversionProbe != nil && p.HardwareBackend == string(p.ConversionProbe.Backend) && p.HardwareBackend != string(decoder.BackendSoftware) {
		request.Probe = *p.ConversionProbe
	}
	switch p.VideoAction {
	case "copy":
		request.CopyVideo = true
	case "convert":
		request.ConvertVideo = true
	case "none":
	default:
		return decoder.ConversionGraph{}, errors.New("invalid delivery video action")
	}
	switch p.AudioAction {
	case "copy":
		request.CopyAudio = true
	case "convert":
		if p.ConversionTarget == nil && ((p.OutputAudioCodec != "aac" && p.OutputAudioCodec != "ac3" && p.OutputAudioCodec != "eac3") || (p.AudioChannels != 2 && p.AudioChannels != 6)) {
			return decoder.ConversionGraph{}, errors.New("invalid delivery audio action")
		}
		request.ConvertAudio = true
	case "none":
	default:
		return decoder.ConversionGraph{}, errors.New("invalid delivery audio action")
	}
	if request.ConvertVideo && p.Trace != nil && p.Trace.Video != nil && p.Trace.Video.DolbyVisionProfile > 0 {
		t := request.Target
		if t == nil {
			t = &decoder.ConversionTarget{VideoCodec: p.OutputVideoCodec, BitDepth: 8, DynamicRange: "sdr", ToneMap: p.ToneMap, Unmapped: p.ToneMapOff, Width: p.TargetWidth, Height: p.TargetHeight, FrameRate: p.MaxFrameRate, VideoBitrateBPS: p.VideoBitrateBPS, AudioCodec: p.OutputAudioCodec, AudioChannels: p.AudioChannels, AudioSampleRate: p.AudioSampleRate, AudioBitrateBPS: p.AudioBitrateBPS, Packaging: p.OutputContainer}
		}
		copy := *t
		copy.DolbyVisionProfile = p.Trace.Video.DolbyVisionProfile
		copy.DolbyVisionCompatibility = p.Trace.Video.DolbyVisionCompatibility
		request.Target = &copy
	}
	if request.ConvertVideo && p.OutputVideoCodec == "h264" && request.Target == nil && p.Trace != nil && p.Trace.Video != nil {
		v := p.Trace.Video
		w, h := convertedDimensions(p)
		rate := v.FrameRate
		if p.MaxFrameRate > 0 {
			rate = min(rate, p.MaxFrameRate)
		}
		request.Target = &decoder.ConversionTarget{VideoCodec: "h264", BitDepth: 8, DynamicRange: "sdr", ToneMap: p.ToneMap, Unmapped: p.ToneMapOff, Width: p.TargetWidth, Height: p.TargetHeight, FrameRate: p.MaxFrameRate, VideoBitrateBPS: p.VideoBitrateBPS, VideoLevel: decoder.H264OutputLevel(w, h, rate), AudioCodec: p.OutputAudioCodec, AudioChannels: p.AudioChannels, AudioSampleRate: p.AudioSampleRate, AudioBitrateBPS: p.AudioBitrateBPS, Packaging: p.OutputContainer}
	}
	if request.Target != nil && (request.Target.VideoCodec == "hevc" || request.Target.VideoCodec == "h264") && request.Target.VideoLevel == 0 {
		target := *request.Target
		w, h := convertedDimensions(p)
		rate := target.FrameRate
		if p.Trace != nil && p.Trace.Video != nil {
			rate = p.Trace.Video.FrameRate
			if target.FrameRate > 0 {
				rate = min(rate, target.FrameRate)
			}
		}
		target.VideoLevel = decoder.HEVCOutputLevel(w, h, rate)
		if target.VideoCodec == "h264" {
			target.VideoLevel = decoder.H264OutputLevel(w, h, rate)
		}
		request.Target = &target
	}
	if p.Trace != nil && p.Trace.Video != nil {
		v := p.Trace.Video
		request.SourceBitDepth = v.BitDepth
		request.DisableHardwareDecode = v.Rotation != 0 || v.SampleAspectRatio != "" && v.SampleAspectRatio != "1:1" && v.SampleAspectRatio != "0:1"
		if request.Target != nil && request.Target.SourceRange == "" {
			target := *request.Target
			target.SourceRange = v.DynamicRange
			request.Target = &target
		}
	}
	return decoder.BuildConversion(request)
}

// planRowQuery is the narrow read port for one immutable plan load. Every lookup in
// this load carries the caller's context so cancellation and deadlines reach the
// database instead of waiting on a background context for a pool slot.
type planRowQuery interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Legacy sessions without a snapshot return nil; callers must choose an explicit
// legacy compatibility path rather than invent a new immutable decision.
func loadDeliveryPlan(ctx context.Context, q planRowQuery, session string) (*DeliveryPlan, error) {
	p := &DeliveryPlan{}
	var codes, policy, trace, renditions, burn string
	err := q.QueryRowContext(ctx, `SELECT profile,source_id,source_size,source_modified_ns,facts_revision,source_container,source_video,source_audio,duration,mode,strategy,reason,output_container,video_action,audio_action,output_video,output_audio,audio_channels,reason_codes,quality_id,target_height,target_width,video_bitrate,audio_bitrate,tone_map,tone_map_algorithm,hardware_backend,hardware_device,hardware_native,software_preset,policy_json,trace_json,audio_stream,deinterlace,max_frame_rate,downmix,renditions_json,burn_in_json FROM playback_delivery_plans WHERE session_id=?`, session).Scan(&p.Profile, &p.SourceID, &p.SourceSize, &p.SourceModifiedNS, &p.FactsRevision, &p.SourceContainer, &p.SourceVideoCodec, &p.SourceAudioCodec, &p.Duration, &p.Mode, &p.Strategy, &p.Reason, &p.OutputContainer, &p.VideoAction, &p.AudioAction, &p.OutputVideoCodec, &p.OutputAudioCodec, &p.AudioChannels, &codes, &p.QualityID, &p.TargetHeight, &p.TargetWidth, &p.VideoBitrateBPS, &p.AudioBitrateBPS, &p.ToneMap, &p.ToneMapAlgorithm, &p.HardwareBackend, &p.HardwareDevice, &p.HardwareNative, &p.SoftwarePreset, &policy, &trace, &p.AudioStream, &p.Deinterlace, &p.MaxFrameRate, &p.Downmix, &renditions, &burn)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if burn != "" {
		if err = json.Unmarshal([]byte(burn), &p.BurnIn); err != nil {
			return nil, err
		}
	}
	if renditions != "" {
		if err = json.Unmarshal([]byte(renditions), &p.Renditions); err != nil || len(p.Renditions) > 16 {
			return nil, errors.New("invalid stored audio renditions")
		}
	}
	p.ReasonCodes = []string{}
	if codes != "" {
		p.ReasonCodes = strings.Split(codes, ",")
	}
	if policy != "" {
		var stored storedPolicy
		if err = json.Unmarshal([]byte(policy), &stored); err == nil {
			p.AirPlay = stored.AirPlay
			p.Policy, p.Target, p.TargetReach, p.AudioSampleRate = stored.ResolvedDeliveryPolicy, stored.SessionTarget, stored.SessionReach, stored.AudioSampleRate
			p.Engine, p.OutputBitDepth, p.OutputRange, p.AudioLossless, p.AudioPassthrough = stored.Engine, stored.OutputBitDepth, stored.OutputRange, stored.AudioLossless, stored.AudioPassthrough
		}
	}
	if p.Engine == "" {
		p.Engine = EngineNative
	}
	if trace != "" {
		var decoded DecisionTrace
		if err = json.Unmarshal([]byte(trace), &decoded); err == nil {
			p.Trace = &decoded
		}
	}
	var valid bool
	err = q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM playback_source_pins pin JOIN playback_sessions s ON s.id=pin.session_id JOIN catalog_assets a ON a.token=pin.asset_id WHERE s.id=? AND s.asset_id=? AND s.mode=? AND pin.asset_id=s.asset_id AND (EXISTS(SELECT 1 FROM prepared_media_session_pins pp WHERE pp.session_id=s.id) OR (a.size=pin.size AND a.modified_ns=pin.modified_ns)) AND pin.size=? AND pin.modified_ns=?)`, session, p.SourceID, p.Mode, p.SourceSize, p.SourceModifiedNS).Scan(&valid)
	if err != nil {
		return nil, err
	}
	if !valid {
		return nil, ErrStaleOffer
	}
	var prepared PreparedDelivery
	var raw string
	err = q.QueryRowContext(ctx, `SELECT v.id,v.revision,pin.digest,pin.size,v.source_revision,v.facts_json FROM prepared_media_session_pins pin JOIN prepared_media_versions v ON v.id=pin.version_id WHERE pin.session_id=? AND v.state IN('published','deleting')`, session).Scan(&prepared.VersionID, &prepared.Revision, &prepared.Digest, &prepared.Size, &prepared.SourceRevision, &raw)
	if err == nil {
		if err = prepared.Facts.DecodeStored([]byte(raw)); err != nil {
			return nil, err
		}
		p.Prepared = &prepared
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if err = loadConversionTarget(ctx, q, session, p); err != nil {
		return nil, err
	}
	for i := range p.Renditions {
		p.Renditions[i].Packaging = p.OutputContainer
	}
	return p, nil
}

// storedPolicy is a plan's policy column: the resolved policy and the parts of
// the plan that have no column of their own (a v1 presentation's target and
// the reach it was admitted with, the rate converted audio is resampled to).
type storedPolicy struct {
	AirPlay bool `json:"airPlay,omitempty"`
	*ResolvedDeliveryPolicy
	SessionTarget    *QualityTarget `json:"sessionTarget,omitempty"`
	SessionReach     string         `json:"sessionReach,omitempty"`
	AudioSampleRate  int            `json:"audioSampleRate,omitempty"`
	Engine           string         `json:"engine,omitempty"`
	OutputBitDepth   int            `json:"outputBitDepth,omitempty"`
	OutputRange      string         `json:"outputRange,omitempty"`
	AudioLossless    bool           `json:"audioLossless,omitempty"`
	AudioPassthrough bool           `json:"audioPassthrough,omitempty"`
}

func persistDeliveryPlan(tx *sql.Tx, session string, p *DeliveryPlan) error {
	policy := ""
	if p.Policy != nil || p.Target != nil || p.TargetReach != "" || p.AudioSampleRate != 0 || p.Engine != "" {
		raw, err := json.Marshal(storedPolicy{p.AirPlay, p.Policy, p.Target, p.TargetReach, p.AudioSampleRate, p.Engine, p.OutputBitDepth, p.OutputRange, p.AudioLossless, p.AudioPassthrough})
		if err != nil {
			return err
		}
		policy = string(raw)
	}
	trace := ""
	if p.Trace != nil {
		if raw, err := json.Marshal(p.Trace); err == nil && len(raw) <= 32<<10 {
			trace = string(raw)
		}
	}
	burn, err := json.Marshal(p.BurnIn)
	if err != nil {
		return err
	}
	renditions, err := json.Marshal(p.Renditions)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO playback_delivery_plans(session_id,profile,source_id,source_size,source_modified_ns,facts_revision,source_container,source_video,source_audio,duration,mode,strategy,reason,output_container,video_action,audio_action,output_video,output_audio,audio_channels,reason_codes,quality_id,target_height,target_width,video_bitrate,audio_bitrate,tone_map,tone_map_algorithm,hardware_backend,hardware_device,hardware_native,software_preset,policy_json,trace_json,audio_stream,deinterlace,max_frame_rate,downmix,renditions_json,burn_in_json) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		session, p.Profile, p.SourceID, p.SourceSize, p.SourceModifiedNS, p.FactsRevision, p.SourceContainer, p.SourceVideoCodec, p.SourceAudioCodec, p.Duration, p.Mode, p.Strategy, p.Reason, p.OutputContainer, p.VideoAction, p.AudioAction, p.OutputVideoCodec, p.OutputAudioCodec, p.AudioChannels,
		strings.Join(p.ReasonCodes, ","), p.QualityID, p.TargetHeight, p.TargetWidth, p.VideoBitrateBPS, p.AudioBitrateBPS, p.ToneMap, p.ToneMapAlgorithm, p.HardwareBackend, p.HardwareDevice, p.HardwareNative, p.SoftwarePreset, policy, trace, p.AudioStream, p.Deinterlace, p.MaxFrameRate, p.Downmix, string(renditions), string(burn))
	if err != nil {
		return err
	}
	if p.ConversionTarget != nil {
		if p.ConversionProbe != nil {
			return StoreConversionTarget(context.Background(), tx, session, *p.ConversionTarget, *p.ConversionProbe)
		}
		return StoreConversionTarget(context.Background(), tx, session, *p.ConversionTarget)
	}
	return nil
}

// Prepared names actual delivery bytes separately from the original source pin.
type PreparedDelivery struct {
	VersionID      string              `json:"versionId"`
	Revision       int64               `json:"revision"`
	Digest         string              `json:"digest"`
	Size           int64               `json:"size"`
	SourceRevision string              `json:"sourceRevision"`
	Facts          preparedmedia.Facts `json:"facts"`
}

func serverConversionRejections(f *decoder.ToolchainFacts, hardware decoder.HardwareProbe, video *SourceVideo, audio *SourceAudio, videoOutput, audioEncoder string, toneMap bool) []RouteRejection {
	if f == nil {
		return nil
	}
	out := []RouteRejection{}
	if video != nil {
		encoder := map[string]string{"h264": "libx264", "hevc": "libx265", "av1": "libsvtav1"}[videoOutput]
		if videoOutput == "av1" {
			for _, name := range []string{"libsvtav1", "libaom-av1", "librav1e"} {
				if f.Encoders[name] {
					encoder = name
					break
				}
			}
		}
		if videoOutput == "h264" && hardware.Available && hardware.Encoder != "" {
			encoder = hardware.Encoder
		}
		if !f.Encoders[encoder] {
			out = append(out, RouteRejection{RejectServerOutput, videoOutput + " encoder unavailable"})
		}
	}
	for _, codec := range []string{func() string {
		if video != nil {
			return video.Codec
		}
		return ""
	}(), func() string {
		if audio != nil {
			if audio.NativeCodec != "" {
				return audio.NativeCodec
			}
			return audio.Codec
		}
		return ""
	}()} {
		if codec == "" {
			continue
		}
		if codec == "dts" {
			codec = "dca"
		}
		if !f.Decoders[codec] {
			out = append(out, RouteRejection{"source_codec_not_decodable", codec})
		}
	}
	if audioEncoder == "opus" {
		audioEncoder = "libopus"
	}
	if audio != nil && !f.Encoders[audioEncoder] {
		out = append(out, RouteRejection{RejectServerOutput, audioEncoder + " encoder unavailable"})
	}
	_, toneErr := decoder.BuildConversion(decoder.ConversionRequest{ConvertVideo: video != nil, ToneMap: toneMap, Probe: hardware, Toolchain: f})
	if toneMap && toneErr != nil {
		out = append(out, RouteRejection{"hdr_tone_mapping_filter_unavailable", ""})
	}
	return out
}
