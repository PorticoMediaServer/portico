package playback

import (
	"fmt"
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
	"github.com/PorticoMediaServer/portico/server/internal/subtitles"
)

// Rejection codes. One is recorded for every rule that closed a route, so the
// trace of a plan reads as "why not the cheaper thing" without re-deriving it.
// They are wire strings: added to, never reworded.
const (
	RejectContainer           = "container_unsupported"
	RejectChroma              = "chroma_subsampling_unsupported"
	RejectFrameRateMode       = "frame_rate_mode_unsupported"
	RejectAudioVariant        = "audio_variant_unsupported"
	RejectVideoCodec          = "video_codec_unsupported"
	RejectVideoProfile        = "video_profile_unsupported"
	RejectVideoLevel          = "video_level_exceeds_device"
	RejectVideoBitDepth       = "video_bit_depth_unsupported"
	RejectResolution          = "resolution_exceeds_device"
	RejectFrameRate           = "frame_rate_exceeds_device"
	RejectVideoBitrate        = "video_bitrate_exceeds_device"
	RejectInterlaced          = "interlaced_unsupported"
	RejectDynamicRange        = "dynamic_range_unsupported"
	RejectDolbyVision         = "dolby_vision_profile_unsupported"
	RejectDolbyVisionEntry    = "dolby_vision_sample_entry_unsupported"
	RejectCodecTag            = "video_sample_entry_unsupported"
	RejectDisplayRange        = "dynamic_range_not_shown_by_display"
	RejectPassthrough         = "audio_passthrough_unavailable"
	RejectRotation            = "rotation_requires_conversion"
	RejectAudioCodec          = "audio_codec_unsupported"
	RejectAudioChannels       = "audio_channels_exceed_device"
	RejectAudioSampleRate     = "audio_sample_rate_exceeds_device"
	RejectAudioBitDepth       = "audio_bit_depth_unsupported"
	RejectAudioTrack          = "audio_track_not_selectable_in_original"
	RejectTransport           = "transport_unsupported"
	RejectServerOutput        = "server_output_unavailable"
	RejectClientFailure       = "client_reported_failure"
	RejectOwnerRemuxDisabled  = "owner_remux_disabled"
	RejectTranscodingDisabled = "transcoding_disabled"
	RejectNoAudio             = "source_has_no_audio_to_convert"

	NoteDetailUnobserved = "source_detail_unobserved"
	NoteBaselineProfile  = "client_published_no_profile"
	NoteToneMapOwnerOff  = "hdr_tone_mapping_disabled_by_owner"
	NoteDeviceBitrate    = "device_bitrate_ceiling_applied"
	NoteBitrateAssumed   = "source_bitrate_unobserved_assumed_from_picture_size"
	// NoteFirstAudioTrackPlayed marks a plan that plays the original although the
	// wanted audio track is not the first, because no other route existed.
	NoteFirstAudioTrackPlayed = "wanted_audio_track_unreachable_first_track_played"
	// Notes about the engine and the viewer's overrides behind a plan.
	NoteEngineAutomatic  = "engine_chosen_automatically"
	NoteEngineFallback   = "engine_setting_unavailable_other_engine_used"
	NoteDeviceToneMaps   = "device_tone_maps_hdr"
	NoteAudioPassthrough = "audio_passed_through_to_receiver"
	// NoteObjectAudioDecoded marks a copied object-audio track that the engine
	// decodes without its objects: the channel bed plays, the objects do not.
	NoteObjectAudioDecoded = "object_audio_decoded_without_objects"
	// NoteHDR10PlusLost marks dynamic metadata dropped by conversion or not
	// supported by the device. The delivered picture reports its actual range.
	NoteHDR10PlusLost                   = "hdr10plus_dynamic_metadata_lost"
	NoteOverrideStereo                  = "viewer_override_force_stereo"
	NoteOverrideNoPassthrough           = "viewer_override_disable_passthrough"
	NoteOverrideNoHDR                   = "viewer_override_disable_hdr"
	NoteDolbyVisionEnhancementDiscarded = "dolby_vision_enhancement_layer_discarded"
	NoteDolbyVisionFELDiscarded         = "dolby_vision_fel_residual_discarded"
)

type RouteRejection struct {
	Code   string `json:"code"`
	Detail string `json:"detail,omitempty"`
}

// RouteEvaluation is one route's verdict. A route is admissible when nothing
// rejected it; exactly one admissible route is chosen.
type RouteEvaluation struct {
	Route      string           `json:"route"`
	Transport  string           `json:"transport,omitempty"`
	Admissible bool             `json:"admissible"`
	Chosen     bool             `json:"chosen"`
	Rejections []RouteRejection `json:"rejections"`
}

type TraceVideo struct {
	Rotation                   int     `json:"rotation,omitempty"`
	SampleAspectRatio          string  `json:"sampleAspectRatio,omitempty"`
	OutputRange                string  `json:"outputRange,omitempty"`
	DolbyVisionLevel           int     `json:"dolbyVisionLevel,omitempty"`
	DolbyVisionCompatibility   int     `json:"dolbyVisionCompatibility,omitempty"`
	Codec                      string  `json:"codec"`
	Profile                    string  `json:"profile,omitempty"`
	Level                      int     `json:"level,omitempty"`
	BitDepth                   int     `json:"bitDepth,omitempty"`
	Width                      int     `json:"width,omitempty"`
	Height                     int     `json:"height,omitempty"`
	FrameRate                  float64 `json:"frameRate,omitempty"`
	Interlaced                 *bool   `json:"interlaced,omitempty"`
	DynamicRange               string  `json:"dynamicRange,omitempty"`
	DolbyVisionProfile         int     `json:"dolbyVisionProfile,omitempty"`
	ChromaSubsampling          string  `json:"chromaSubsampling,omitempty"`
	FrameRateNumerator         int     `json:"frameRateNumerator,omitempty"`
	FrameRateDenominator       int     `json:"frameRateDenominator,omitempty"`
	FrameRateMode              string  `json:"frameRateMode,omitempty"`
	DolbyVisionRPU             *bool   `json:"dolbyVisionRpu,omitempty"`
	DolbyVisionBaseLayer       *bool   `json:"dolbyVisionBaseLayer,omitempty"`
	DolbyVisionEnhancementType string  `json:"dolbyVisionEnhancementType,omitempty"`
	DolbyVisionCMVersion       string  `json:"dolbyVisionCmVersion,omitempty"`
	HDR10Plus                  bool    `json:"hdr10Plus,omitempty"`
	MasterDisplay              string  `json:"masterDisplay,omitempty"`
	MaxCLL                     string  `json:"maxCll,omitempty"`
	BitRate                    int64   `json:"bitRate,omitempty"`
}

type TraceAudio struct {
	Lossless    *bool  `json:"lossless,omitempty"`
	StreamIndex int    `json:"streamIndex"`
	Codec       string `json:"codec"`
	Channels    int    `json:"channels,omitempty"`
	Language    string `json:"language,omitempty"`
	ObjectAudio string `json:"objectAudio,omitempty"`
	Profile     string `json:"profile,omitempty"`
	Variant     string `json:"variant,omitempty"`
	Layout      string `json:"layout,omitempty"`
	SampleRate  int    `json:"sampleRate,omitempty"`
	BitDepth    int    `json:"bitDepth,omitempty"`
	// Why this track: explicit, carried, preferred_language, default, first.
	ChosenBy string `json:"chosenBy"`
	Tracks   int    `json:"tracks"`
}

// DecisionTrace is the stored explanation of one plan. It names no path and no
// source identifier; it is safe to show to the viewer who owns the session.
type DecisionTrace struct {
	Version   int               `json:"version"`
	Client    ClientSummary     `json:"client"`
	Container string            `json:"container"`
	BitRate   int64             `json:"bitRate,omitempty"`
	Video     *TraceVideo       `json:"video,omitempty"`
	Audio     *TraceAudio       `json:"audio,omitempty"`
	Routes    []RouteEvaluation `json:"routes"`
	Notes     []string          `json:"notes"`
}

func containsString(values []string, v string) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

func containsInt(values []int, v int) bool {
	for _, x := range values {
		if x == v {
			return true
		}
	}
	return false
}

func profileName(s string) string {
	return strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", "_"))
}

// dolbyVisionFallback is the range a device without Dolby Vision sees when it
// decodes the base layer. Profile 5 has no such layer: its base is IPT-PQ-C2,
// which an HDR10 pipeline shows in the wrong colours.
func dolbyVisionFallback(v *SourceVideo) string {
	return decoder.DolbyVisionBaseRange(v.DolbyVisionProfile, v.DolbyVisionCompatibility)
}

// effectiveRange is the dynamic range the device will actually be shown for this
// source: Dolby Vision where the engine has the profile and the picture can be
// presented as Dolby Vision, the fallback layer otherwise.
func effectiveRange(v *SourceVideo, c *ClientVideoCodec, client ClientProfile) string {
	r := v.DynamicRange
	if r == "" {
		return assets.RangeSDR
	}
	if r == assets.RangeDolbyVision && !nativeDolbyVision(v, c, client) {
		return dolbyVisionFallback(v)
	}
	return r
}

// nativeDolbyVision reports whether the engine plays this picture as Dolby
// Vision: it decodes the profile, and the display shows Dolby Vision or the
// engine maps it to what the display shows.
func nativeDolbyVision(v *SourceVideo, c *ClientVideoCodec, client ClientProfile) bool {
	if v.DolbyVisionProfile == 8 && c != nil && len(c.DolbyVisionProfile8Compatibility) > 0 && !containsInt(c.DolbyVisionProfile8Compatibility, v.DolbyVisionCompatibility) {
		return false
	}
	return v.DynamicRange == assets.RangeDolbyVision && (!v.DolbyVisionMetadataKnown || v.DolbyVisionRPU) && c != nil && containsInt(c.DolbyVisionProfiles, v.DolbyVisionProfile) && client.presents(assets.RangeDolbyVision)
}

// videoRejections checks one source video stream against one transport of the
// device. copying is true for the HLS copy routes, where rotation metadata and
// the Dolby Vision sample entry do not survive the repackaging the same way.
func videoRejections(v *SourceVideo, client ClientProfile, t ClientTransport, allowHDR, copying bool) []RouteRejection {
	var out []RouteRejection
	c := client.videoCodec(v.Codec)
	if c == nil || !containsString(t.Video, v.Codec) {
		return append(out, RouteRejection{RejectVideoCodec, v.Codec})
	}
	// A decoder that states operating points is held to the one that serves
	// this picture's depth, size and rate.
	point, decoded := c.forStream(v.BitDepth, v.Width, v.Height, v.FrameRate)
	if !decoded {
		return append(out, RouteRejection{RejectVideoBitDepth, fmt.Sprintf("%d-bit", v.BitDepth)})
	}
	c = &point
	if len(c.Profiles) > 0 && v.Profile != "" && !containsString(c.Profiles, profileName(v.Profile)) {
		out = append(out, RouteRejection{RejectVideoProfile, v.Profile})
	}
	if c.MaxLevel > 0 && v.Level > c.MaxLevel {
		out = append(out, RouteRejection{RejectVideoLevel, fmt.Sprintf("%d>%d", v.Level, c.MaxLevel)})
	}
	if len(c.BitDepths) > 0 && v.BitDepth > 0 && !containsInt(c.BitDepths, v.BitDepth) {
		out = append(out, RouteRejection{RejectVideoBitDepth, fmt.Sprintf("%d-bit", v.BitDepth)})
	}
	if (c.MaxWidth > 0 && v.Width > c.MaxWidth) || (c.MaxHeight > 0 && v.Height > c.MaxHeight) {
		out = append(out, RouteRejection{RejectResolution, fmt.Sprintf("%dx%d", v.Width, v.Height)})
	}
	if c.MaxFrameRate > 0 && v.FrameRate > c.MaxFrameRate+0.01 {
		out = append(out, RouteRejection{RejectFrameRate, fmt.Sprintf("%.3f", v.FrameRate)})
	}
	if c.MaxBitrateBPS > 0 && max(v.BitRate, v.MaxBitRate) > int64(c.MaxBitrateBPS) {
		out = append(out, RouteRejection{RejectVideoBitrate, fmt.Sprintf("%d", max(v.BitRate, v.MaxBitRate))})
	}
	if v.ChromaSubsampling != "" && len(c.ChromaSubsampling) > 0 && !containsString(c.ChromaSubsampling, v.ChromaSubsampling) {
		out = append(out, RouteRejection{RejectChroma, v.ChromaSubsampling})
	}
	if v.FrameRateMode != "" && len(c.FrameRateModes) > 0 && !containsString(c.FrameRateModes, v.FrameRateMode) {
		out = append(out, RouteRejection{RejectFrameRateMode, v.FrameRateMode})
	}
	if v.Interlaced && !c.Interlaced {
		out = append(out, RouteRejection{RejectInterlaced, ""})
	}
	if copying && v.Rotation != 0 {
		out = append(out, RouteRejection{RejectRotation, fmt.Sprintf("%d", v.Rotation)})
	}
	if !copying && len(c.CodecTags) > 0 && v.CodecTag != "" && !containsString(c.CodecTags, strings.ToLower(v.CodecTag)) {
		// The engine plays this codec only under other sample entries; the
		// server writes one it takes when it repackages the stream.
		out = append(out, RouteRejection{RejectCodecTag, v.CodecTag})
	}
	if v.DynamicRange != "" && v.DynamicRange != assets.RangeSDR {
		native := nativeDolbyVision(v, c, client)
		shown := effectiveRange(v, c, client)
		// An HDR10+ picture is a plain HDR10 picture with dynamic metadata on
		// top, so a decoder or display that declares HDR10+ covers the base
		// picture (VIDEO.md VS-2a). The reverse is not true: a plain HDR10
		// picture needs HDR10, not only an HDR10+ claim.
		plus := shown == assets.RangeHDR10 && v.HDR10Plus
		decoded := containsString(c.DynamicRanges, shown) || plus && containsString(c.DynamicRanges, "hdr10plus")
		presented := client.presents(shown) || plus && client.presents("hdr10plus")
		switch {
		case !allowHDR && shown != assets.RangeSDR:
			out = append(out, RouteRejection{ReasonHDRNotAllowed, v.DynamicRange})
		case shown == "":
			out = append(out, RouteRejection{RejectDolbyVision, fmt.Sprintf("profile %d", v.DolbyVisionProfile)})
		case shown != assets.RangeSDR && !decoded:
			out = append(out, RouteRejection{RejectDynamicRange, shown})
		case shown != assets.RangeSDR && !presented:
			// The engine decodes the range, but neither the display nor the
			// engine's own tone mapping can show it.
			out = append(out, RouteRejection{RejectDisplayRange, shown})
		case !copying && !native && v.DynamicRange == assets.RangeDolbyVision && (v.CodecTag == "dvh1" || v.CodecTag == "dvhe" || v.CodecTag == "dva1" || v.CodecTag == "dvav"):
			// A Dolby Vision sample entry is refused outright by decoders that do
			// not know it, even though the base layer underneath is ordinary HEVC.
			out = append(out, RouteRejection{RejectDolbyVisionEntry, v.CodecTag})
		}
	}
	return out
}

// audioRejections checks one source audio track against one transport of the
// device. A track is taken when the engine decodes it, or when the engine can
// hand it undecoded to an output that carries bitstreams.
func audioRejections(a *SourceAudio, client ClientProfile, t ClientTransport) []RouteRejection {
	if !containsString(t.Audio, a.Codec) {
		return []RouteRejection{{RejectAudioCodec, a.Codec}}
	}
	if audioPassedThrough(a, client) {
		return nil
	}
	c := client.audioCodec(a.Codec)
	if c == nil {
		if client.passthroughDeclared(a.Codec) {
			return []RouteRejection{{RejectPassthrough, a.Codec}}
		}
		return []RouteRejection{{RejectAudioCodec, a.Codec}}
	}
	var out []RouteRejection
	if !acceptsAudioVariant(c.Variants, a, c.ObjectAudio, false) {
		out = append(out, RouteRejection{RejectAudioVariant, sourceVariant(a)})
	}
	if c.MaxChannels > 0 && a.Channels > c.MaxChannels {
		out = append(out, RouteRejection{RejectAudioChannels, fmt.Sprintf("%d>%d", a.Channels, c.MaxChannels)})
	}
	if c.MaxBitDepth > 0 && sourceAudioDepth(a) > c.MaxBitDepth {
		out = append(out, RouteRejection{RejectAudioBitDepth, fmt.Sprint(sourceAudioDepth(a))})
	}
	if c.MaxSampleRate > 0 && a.SampleRate > c.MaxSampleRate {
		out = append(out, RouteRejection{RejectAudioSampleRate, fmt.Sprintf("%d", a.SampleRate)})
	}
	return out
}

// audioPassedThrough reports whether the engine would hand this track to the
// audio output undecoded: the codec is on its passthrough list, the route
// carries bitstreams, and the track fits the channels the receiver takes.
func audioPassedThrough(a *SourceAudio, client ClientProfile) bool {
	p := client.passthroughCodec(a.Codec)
	return p != nil && (p.MaxChannels == 0 || a.Channels <= p.MaxChannels) && acceptsAudioVariant(p.Variants, a, p.ObjectAudio, true)
}

// passthroughDeclared reports whether the engine lists the codec for
// passthrough at all, whatever the output route is now.
func (p ClientProfile) passthroughDeclared(codec string) bool {
	for _, c := range p.AudioPassthrough {
		if c.Codec == codec {
			return true
		}
	}
	return false
}

// chooseAudio picks the programme audio for a session: the viewer's explicit
// choice, else the language picked earlier in this playback, else
// the first track in a preferred language, else the container's default, else
// the first track. Original language comes only from published catalogue facts;
// when unknown, @original keeps the file default, then an ordinary track.
func chooseAudio(tracks []SourceAudio, explicit int, carried string, preferred []string, originalLanguage string) (*SourceAudio, string) {
	if len(tracks) == 0 {
		return nil, ""
	}
	if explicit >= 0 {
		for i := range tracks {
			if tracks[i].Index == explicit {
				return &tracks[i], "explicit"
			}
		}
	}
	if want := languageKey(carried); want != "" {
		for i := range tracks {
			if languageKey(tracks[i].Language) == want && !tracks[i].Commentary {
				return &tracks[i], "carried"
			}
		}
	}
	if len(preferred) == 0 {
		preferred = []string{"@original"}
	}
	original := languageKey(originalLanguage)
	for _, want := range preferred {
		if want == "@original" {
			if original != "" {
				for i := range tracks {
					if !tracks[i].Commentary && languageKey(tracks[i].Language) == original {
						return &tracks[i], "original_language"
					}
				}
				continue
			}
			for i := range tracks {
				if tracks[i].Default {
					return &tracks[i], "original_default"
				}
			}
			for i := range tracks {
				if !tracks[i].Commentary {
					return &tracks[i], "original_first"
				}
			}
			continue
		}
		want = languageKey(want)
		if want == "" {
			continue
		}
		for pass := 0; pass < 2; pass++ {
			for i := range tracks {
				if languageKey(tracks[i].Language) == want && (pass == 1 || !tracks[i].Commentary) {
					return &tracks[i], "preferred_language"
				}
			}
		}
	}
	for i := range tracks {
		if tracks[i].Default {
			return &tracks[i], "default"
		}
	}
	return &tracks[0], "first"
}

// languageKey folds the spellings a file and a preference use for one language:
// ISO 639-1, 639-2/B, 639-2/T and a BCP 47 tag with a region all compare equal.
func languageKey(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexAny(s, "-_"); i > 0 {
		s = s[:i]
	}
	if s == "cn" {
		return "yue"
	}
	if len(s) == 3 {
		if two, ok := subtitles.TwoLetterLanguage(s); ok {
			return two
		}
	}
	if s == "und" || s == "unknown" || s == "xx" {
		return ""
	}
	return s
}

// Server output limits. They describe what this server's producers can write,
// independent of any device: which codecs can be copied into each HLS container.
var (
	tsCopyVideo   = []string{"h264", "hevc", "mpeg2video"}
	tsCopyAudio   = []string{"aac", "mp3", "mp2", "ac3", "eac3"}
	fmp4CopyVideo = []string{"h264", "hevc", "av1", "vp9"}
	fmp4CopyAudio = []string{"aac", "mp3", "ac3", "eac3", "flac", "opus", "alac", "truehd", "dts"}
)

// hlsTransports lists the device's HLS transports this server can produce, in
// the order they are tried. MPEG-TS comes first for H.264 and MPEG-2: it is the
// older, more forgiving container. Fragmented MP4 comes first for every newer
// codec and for any HDR picture, because that is the container HLS specifies
// for them and the only one that carries their colour signalling reliably.
func hlsTransports(client ClientProfile, fmp4 bool, video *SourceVideo) []ClientTransport {
	order := []string{TransportHLSTS, TransportHLSFMP4}
	if video != nil && ((video.Codec != "h264" && video.Codec != "mpeg2video") || (video.DynamicRange != "" && video.DynamicRange != assets.RangeSDR)) {
		order = []string{TransportHLSFMP4, TransportHLSTS}
	}
	var out []ClientTransport
	for _, kind := range order {
		if kind == TransportHLSFMP4 && !fmp4 {
			continue
		}
		for _, t := range client.Transports {
			if t.Transport == kind {
				out = append(out, t)
			}
		}
	}
	return out
}

func serverCopyVideo(transport, codec string) bool {
	if transport == TransportHLSFMP4 {
		return containsString(fmp4CopyVideo, codec)
	}
	return containsString(tsCopyVideo, codec)
}

func serverCopyAudio(transport, codec string) bool {
	if transport == TransportHLSFMP4 {
		return containsString(fmp4CopyAudio, codec)
	}
	return containsString(tsCopyAudio, codec)
}

// convertedAudio is the target of an audio conversion for one device.
type convertedAudio struct {
	Codec    string
	Channels int
	Bitrate  int
	Downmix  bool
	// Lossless is true when the output keeps every bit of the source.
	Lossless bool
	// SampleRate is the rate to resample to; zero keeps the source's.
	SampleRate int
	// NoRate means no codec this server converts to is encoded at a rate the
	// device takes for it: the track cannot be converted for this device.
	NoRate        bool
	NoOutput      bool
	LosslessCodec bool
}

// convertedAudioRates are the rates each output codec is encoded at that
// converted audio is resampled to, highest first. Every AAC, AC-3 and E-AC-3
// decoder takes 48 kHz; many televisions, Cast devices and set-top boxes take
// nothing above it.
var convertedAudioRates = map[string][]int{
	"aac":  {48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000, 7350},
	"ac3":  {48000, 44100, 32000},
	"eac3": {48000, 44100, 32000},
	"flac": {192000, 176400, 96000, 88200, 64000, 48000, 44100, 32000, 24000, 22050, 16000, 12000, 11025, 8000},
	"opus": {48000, 24000, 16000, 12000, 8000},
}

// convertedSampleRate is the rate a track converted to codec is resampled to,
// with its existing rate chosen explicitly when supported. The output
// may not exceed 48 kHz or what the device declares for that codec, and must
// be a rate the codec is encoded at. Whether any such rate exists is decided
// first, whatever is known of the source: when none does the answer is false,
// never a rate the device said it cannot take. A source at one of those rates
// keeps it; any other takes the lowest of them at or above its own, else the
// highest below it; and a source whose rate was never observed is given the
// highest of them outright, because a rate nobody saw cannot be kept on trust.
func convertedSampleRate(a *SourceAudio, codec string, output *ClientAudioCodec) (int, bool) {
	if a == nil {
		return 48000, true
	}
	limit := 48000
	if codec == "flac" {
		limit = 192000
	}
	if output != nil && output.MaxSampleRate > 0 && output.MaxSampleRate < limit {
		limit = output.MaxSampleRate
	}
	if codec == "flac" && a.SampleRate > 0 && a.SampleRate <= limit {
		return a.SampleRate, true
	}
	chosen := 0
	for _, rate := range convertedAudioRates[codec] {
		if rate > limit {
			continue
		}
		if a.SampleRate == 0 && rate == 48000 {
			return rate, true
		}
		if a.SampleRate > 0 && rate == a.SampleRate {
			return rate, true
		}
		if chosen == 0 || a.SampleRate > 0 && rate > a.SampleRate {
			chosen = rate
		}
	}
	return chosen, chosen != 0
}

// audioOutputFor picks what a track is converted to for one device, from what
// this server can produce and what the device's engine takes on the transport
// (nil: not checked).
//
// The order is the most of the source the device can use. A losslessly coded
// track goes to a lossless output when one exists, then to Dolby Digital Plus
// or Dolby Digital at their full rate, and only then to AAC. A lossy surround
// track goes to AAC in 5.1 first, then to the Dolby codecs for a device that
// has no multichannel AAC but a receiver. Stereo AAC is the floor: every
// device this server plans for decodes it.
func audioOutputFor(a *SourceAudio, client ClientProfile, target QualityTarget, transport *ClientTransport, outputs ConversionOutputs) convertedAudio {
	floor := convertedAudio{Codec: "aac", Channels: 2, Bitrate: audioBudget(target)}
	source := 2
	if a != nil && a.Channels > 0 {
		source = a.Channels
	}
	lossless := losslessAudio(a)
	layouts := []int{}
	if source >= 8 {
		layouts = append(layouts, 8)
	}
	if source >= 6 {
		layouts = append(layouts, 6)
	}
	if source < 2 {
		layouts = append(layouts, 1)
	} else {
		layouts = append(layouts, 2)
	}
	type candidate struct {
		codec    string
		channels int
	}
	var candidates []candidate
	// Keep the widest audible layout first. Within it, keep lossless samples,
	// then receiver-friendly Dolby, Opus and AAC, before dropping channels.
	for _, n := range layouts {
		for _, o := range outputs.Audio {
			if o.Lossless {
				candidates = append(candidates, candidate{o.Codec, n})
			}
		}
		order := []string{"eac3", "ac3", "opus", "aac"}
		if !lossless {
			order = []string{"aac", "eac3", "ac3", "opus"}
		}
		for _, codec := range order {
			candidates = append(candidates, candidate{codec, n})
		}
	}
	for _, c := range candidates {
		output := outputs.AudioOutput(c.codec)
		if output == nil {
			continue
		}
		rate, produced := output.Bitrates[c.channels]
		if !produced {
			continue
		}
		if transport != nil && (!containsString(transport.Audio, c.codec) || !containsString(output.Transports, transport.Transport)) {
			continue
		}
		// What the device does with it: decodes it to the channels its output
		// has, or hands it to a receiver.
		decoder := client.audioCodec(c.codec)
		decoded := decoder != nil && (decoder.MaxChannels == 0 || decoder.MaxChannels >= c.channels) && (c.channels <= 2 || (client.AudioOutput.MaxChannels == 0 || client.AudioOutput.MaxChannels >= c.channels))
		passed := false
		if p := client.passthroughCodec(c.codec); p != nil && (p.MaxChannels == 0 || p.MaxChannels >= c.channels) {
			passed = true
		}
		if c.codec == "flac" && decoder != nil && decoder.MaxBitDepth > 0 && decoder.MaxBitDepth < 16 {
			continue
		}
		if !decoded && !passed {
			continue
		}
		// The viewer's or the owner's audio ceiling.
		ceiling := target.MaxAudioBitrateBPS
		switch {
		case output.Lossless:
			if ceiling > 0 {
				continue
			}
		case c.codec == "aac" || c.codec == "opus":
			if c.channels > 2 && ceiling > 0 && ceiling < 256_000 {
				continue
			}
			if ceiling > 0 && ceiling < rate {
				rate = ceiling
			}
		default:
			if ceiling > 0 && ceiling < rate {
				continue
			}
		}
		out := convertedAudio{Codec: c.codec, Channels: c.channels, Bitrate: rate, Downmix: source > c.channels, LosslessCodec: output.Lossless, Lossless: output.Lossless && source == c.channels && (a == nil || !strings.HasPrefix(a.NativeCodec, "pcm_f"))}
		resample, ok := convertedSampleRate(a, c.codec, decoder)
		if !ok {
			if c.codec != "aac" {
				// A codec with no rate this device takes is passed over, for
				// the next one and then for AAC.
				continue
			}
			out.NoRate = true
		}
		if output.Lossless && decoder != nil && decoder.MaxBitDepth > 0 && sourceAudioDepth(a) > decoder.MaxBitDepth {
			out.Lossless = false
		}
		out.SampleRate = resample
		if a != nil && a.SampleRate > 0 && resample != a.SampleRate {
			out.Lossless = false
		}
		return out
	}
	// No invented AAC floor when that codec/layout cannot reach this engine.
	floor.NoRate = true
	floor.NoOutput = true
	return floor
}

// passthroughRoute reports whether an audio output route carries an undecoded
// bitstream to something that decodes it.
func passthroughRoute(route string) bool {
	switch route {
	case "hdmi", "arc", "earc", "optical", "spdif":
		return true
	}
	return false
}

// HLSTSRejections shares on-demand codec, range and output-route checks with Live TV.
func HLSTSRejections(video *SourceVideo, audio *SourceAudio, client ClientProfile) ([]RouteRejection, []RouteRejection) {
	if client.Version == 0 {
		client = BaselineClientProfile()
	}
	t := transportNamed(client.Transports, TransportHLSTS)
	if t == nil {
		return []RouteRejection{{RejectTransport, TransportHLSTS}}, []RouteRejection{{RejectTransport, TransportHLSTS}}
	}
	var vr, ar []RouteRejection
	if video != nil {
		vr = videoRejections(video, client, *t, true, true)
	}
	if audio != nil {
		ar = audioRejections(audio, client, *t)
	}
	return vr, ar
}

func sourceAudioDepth(a *SourceAudio) int {
	if a == nil {
		return 0
	}
	if a.BitsPerSample > 0 {
		return a.BitsPerSample
	}
	return a.BitDepth
}

func flacDepth(a *SourceAudio, c *ClientAudioCodec) int {
	depth := sourceAudioDepth(a)
	if depth == 0 {
		depth = 24
	}
	// Integer FLAC output accepts 16, 24 or 32 bits. Promotion keeps samples;
	// a smaller declared device ceiling is an explicit reduction.
	if depth <= 16 {
		depth = 16
	} else if depth <= 24 {
		depth = 24
	} else {
		depth = 32
	}
	if c != nil && c.MaxBitDepth > 0 {
		for depth > c.MaxBitDepth {
			if depth > 24 {
				depth = 24
			} else {
				depth = 16
				break
			}
		}
	}
	return depth
}
