package playback

import (
	"github.com/PorticoMediaServer/portico/server/internal/assets"
	"github.com/PorticoMediaServer/portico/server/internal/decoder"
)

// PlaybackInfo describes the committed source and delivery recipe. It never
// substitutes a recipe for renderer observations (PCM, sink or screen mode).
// The existing separately fenced ObservedPlayback is the sole output report.
type PlaybackPictureFacts struct {
	Container                  string                     `json:"container,omitempty"`
	Codec                      string                     `json:"codec"`
	Profile                    string                     `json:"profile,omitempty"`
	BitDepth                   int                        `json:"bitDepth,omitempty"`
	Width                      int                        `json:"width,omitempty"`
	Height                     int                        `json:"height,omitempty"`
	FrameRate                  float64                    `json:"frameRate,omitempty"`
	FrameRateNumerator         int                        `json:"frameRateNumerator,omitempty"`
	FrameRateDenominator       int                        `json:"frameRateDenominator,omitempty"`
	FrameRateMode              string                     `json:"frameRateMode,omitempty"`
	Interlaced                 *bool                      `json:"interlaced,omitempty"`
	ChromaSubsampling          string                     `json:"chromaSubsampling,omitempty"`
	DynamicRange               string                     `json:"dynamicRange,omitempty"`
	DolbyVisionProfile         int                        `json:"dolbyVisionProfile,omitempty"`
	DolbyVisionCompatibility   int                        `json:"dolbyVisionCompatibility,omitempty"`
	DolbyVisionRPU             *bool                      `json:"dolbyVisionRpu,omitempty"`
	DolbyVisionBaseLayer       *bool                      `json:"dolbyVisionBaseLayer,omitempty"`
	DolbyVisionEnhancementType string                     `json:"dolbyVisionEnhancementType,omitempty"`
	DolbyVisionCMVersion       string                     `json:"dolbyVisionCmVersion,omitempty"`
	HDR10Plus                  *bool                      `json:"hdr10Plus,omitempty"`
	MasterDisplay              string                     `json:"masterDisplay,omitempty"`
	MaxCLL                     string                     `json:"maxCll,omitempty"`
	Conversion                 *PlaybackPictureConversion `json:"conversion,omitempty"`
}
type PlaybackPictureConversion struct {
	Kind                     string `json:"kind"`
	SourceDolbyVisionProfile int    `json:"sourceDolbyVisionProfile"`
	SourceEnhancementType    string `json:"sourceEnhancementType,omitempty"`
	EnhancementDiscarded     bool   `json:"enhancementDiscarded"`
	FELResidualDiscarded     bool   `json:"felResidualDiscarded"`
}
type PlaybackSoundFacts struct {
	Codec       string `json:"codec"`
	Profile     string `json:"profile,omitempty"`
	Variant     string `json:"variant,omitempty"`
	ObjectAudio string `json:"objectAudio,omitempty"`
	Channels    int    `json:"channels,omitempty"`
	Layout      string `json:"layout,omitempty"`
	SampleRate  int    `json:"sampleRate,omitempty"`
	BitDepth    int    `json:"bitDepth,omitempty"`
	// Lossless describes this track's coding. Nil is unknown, including an
	// unmeasured WavPack mode. It is not a renderer or conversion-fidelity fact.
	Lossless *bool `json:"lossless,omitempty"`
	// Delivery is how the sink receives the delivered track: handed over
	// undecoded ("passthrough") or decoded to PCM ("decoded"). It is never
	// stated for the source, which is a file, not an output.
	Delivery string `json:"delivery,omitempty"`
}

// Subtitle facts come from the actual selected resource, not a rendition label
// or a URL. A nil selection is unknown; an explicitly off selection says off.
type PlaybackSubtitleFacts struct {
	Format    string `json:"format,omitempty"`
	Language  string `json:"language,omitempty"`
	Title     string `json:"title,omitempty"`
	Origin    string `json:"origin,omitempty"`
	Rendering string `json:"rendering,omitempty"`
}
type PlaybackMediaFacts struct {
	Video     *PlaybackPictureFacts  `json:"video,omitempty"`
	Audio     *PlaybackSoundFacts    `json:"audio,omitempty"`
	Subtitles *PlaybackSubtitleFacts `json:"subtitles,omitempty"`
}
type PlaybackRoutes struct {
	Picture   string `json:"picture,omitempty"`
	Sound     string `json:"sound,omitempty"`
	Subtitles string `json:"subtitles,omitempty"`
}
type PlaybackReason struct {
	Kind           string `json:"kind"`
	VideoCodec     string `json:"videoCodec,omitempty"`
	AudioCodec     string `json:"audioCodec,omitempty"`
	Container      string `json:"container,omitempty"`
	MaxHeight      int    `json:"maxHeight,omitempty"`
	MaxBitrateKbps int    `json:"maxBitrateKbps,omitempty"`
}
type PlaybackReplacement struct {
	DynamicRange string `json:"dynamicRange,omitempty"`
	AudioCodec   string `json:"audioCodec,omitempty"`
	Channels     int    `json:"channels,omitempty"`
	Layout       string `json:"layout,omitempty"`
}
type PlaybackLoss struct {
	Feature     string              `json:"feature"`
	Replacement PlaybackReplacement `json:"replacement"`
}
type PlaybackInfo struct {
	Route                string             `json:"route"`
	Routes               PlaybackRoutes     `json:"routes"`
	Reason               PlaybackReason     `json:"reason"`
	Source               PlaybackMediaFacts `json:"source"`
	Delivered            PlaybackMediaFacts `json:"delivered"`
	Losses               []PlaybackLoss     `json:"losses"`
	Network              string             `json:"network"`
	EstimatedBitrateKbps int                `json:"estimatedBitrateKbps,omitempty"`
	Basis                string             `json:"basis"`
}

func playbackRoute(action string, repackaged bool) string {
	switch action {
	case "convert":
		return "converted"
	case "copy":
		if repackaged {
			return "repackaged"
		}
		return "original"
	case "original":
		return "original"
	}
	return ""
}
func strongerPlaybackRoute(current, next string) string {
	if next == "converted" || next == "repackaged" && current != "converted" {
		return next
	}
	return current
}
func copiedBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

// PlaybackInfoForPlan is pure and allowlisted: source identifiers, tool paths,
// private hardware devices, raw diagnostics, grants and presentation URLs never
// enter it. Missing source snapshots yield no comparison rather than joining
// current catalogue facts onto a different committed source generation.
func PlaybackInfoForPlan(p DeliveryPlan, subtitle *PlaybackSubtitleFacts) *PlaybackInfo {
	if p.Trace == nil || p.Trace.Video == nil && p.Trace.Audio == nil && subtitle == nil {
		return nil
	}
	out := &PlaybackInfo{Route: "original", Reason: PlaybackReason{Kind: "compatible"}, Losses: []PlaybackLoss{}, Network: "unknown", Basis: "committed_delivery_plan"}
	if p.TargetReach == "local" {
		out.Network = "home"
	} else if p.TargetReach == "remote" {
		out.Network = "away"
	} else if p.Policy != nil {
		switch p.Policy.NetworkClass {
		case NetworkLocal:
			out.Network = "home"
		case NetworkRemote:
			out.Network = "away"
		}
	}
	repackaged := p.Strategy != DeliveryOriginal
	if v := p.Trace.Video; v != nil {
		src := PlaybackPictureFacts{Container: p.Trace.Container, Codec: v.Codec, Profile: v.Profile, BitDepth: v.BitDepth, Width: v.Width, Height: v.Height, FrameRate: v.FrameRate, FrameRateNumerator: v.FrameRateNumerator, FrameRateDenominator: v.FrameRateDenominator, FrameRateMode: v.FrameRateMode, Interlaced: copiedBool(v.Interlaced), ChromaSubsampling: v.ChromaSubsampling, DynamicRange: v.DynamicRange, DolbyVisionProfile: v.DolbyVisionProfile, DolbyVisionCompatibility: v.DolbyVisionCompatibility, DolbyVisionEnhancementType: v.DolbyVisionEnhancementType, DolbyVisionCMVersion: v.DolbyVisionCMVersion, DolbyVisionRPU: copiedBool(v.DolbyVisionRPU), DolbyVisionBaseLayer: copiedBool(v.DolbyVisionBaseLayer), MasterDisplay: v.MasterDisplay, MaxCLL: v.MaxCLL}
		if v.HDR10Plus {
			value := true
			src.HDR10Plus = &value
		}
		out.Source.Video = &src
		dst := src
		dst.Interlaced = copiedBool(src.Interlaced)
		dst.HDR10Plus = copiedBool(src.HDR10Plus)
		dst.DolbyVisionRPU, dst.DolbyVisionBaseLayer = copiedBool(src.DolbyVisionRPU), copiedBool(src.DolbyVisionBaseLayer)
		if p.OutputContainer != "" {
			dst.Container = p.OutputContainer
		}
		if p.OutputVideoCodec != "" {
			dst.Codec = p.OutputVideoCodec
		}
		out.Routes.Picture = playbackRoute(p.VideoAction, repackaged)
		if p.VideoAction == "convert" {
			dst.Profile = ""
			dst.BitDepth = p.OutputBitDepth
			dst.DynamicRange = p.OutputRange
			dst.Width, dst.Height = convertedDimensions(p)
			dst.ChromaSubsampling = ""
			dst.HDR10Plus = nil
			dst.FrameRateNumerator, dst.FrameRateDenominator, dst.FrameRateMode = 0, 0, ""
			if p.MaxFrameRate > 0 && dst.FrameRate > p.MaxFrameRate {
				dst.FrameRate = p.MaxFrameRate
			}
			dst.MasterDisplay, dst.MaxCLL = "", ""
			if p.Deinterlace {
				value := false
				dst.Interlaced = &value
			} else {
				dst.Interlaced = nil
			}
			if target := p.ConversionTarget; target != nil {
				dst.Profile, dst.BitDepth, dst.DynamicRange = encodedPictureProfile(*target), target.BitDepth, target.DynamicRange
				dst.ChromaSubsampling = encodedPictureChroma(*target)
				dst.MasterDisplay, dst.MaxCLL = target.MasterDisplay, target.MaxCLL
			}
			dst.DolbyVisionProfile, dst.DolbyVisionCompatibility = 0, 0
			dst.DolbyVisionRPU, dst.DolbyVisionBaseLayer = nil, nil
			dst.DolbyVisionEnhancementType, dst.DolbyVisionCMVersion = "", ""
		} else if p.VideoAction == "copy" {
			if src.DynamicRange != "" && v.OutputRange != "" {
				dst.DynamicRange = v.OutputRange
			}
			if target := p.ConversionTarget; target != nil {
				if src.DynamicRange != "" {
					dst.DynamicRange = target.DynamicRange
				}
				if target.CopyDolbyVision {
					dst.DolbyVisionProfile, dst.DolbyVisionCompatibility = target.DeliveredDolbyVision()
				}
				if target.DolbyVisionConversion == decoder.DolbyVisionProfile7To81 {
					// This packet transformation is Converted even though its raw delivery
					// strategy remains copy-remux. FEL residual reconstruction is not claimed.
					out.Routes.Picture = "converted"
					dst.Conversion = &PlaybackPictureConversion{Kind: decoder.DolbyVisionProfile7To81, SourceDolbyVisionProfile: src.DolbyVisionProfile, SourceEnhancementType: src.DolbyVisionEnhancementType, EnhancementDiscarded: containsString(p.Trace.Notes, NoteDolbyVisionEnhancementDiscarded), FELResidualDiscarded: containsString(p.Trace.Notes, NoteDolbyVisionFELDiscarded)}
					dst.DolbyVisionEnhancementType = ""
				}
			}
		}
		if dst.DynamicRange != "" && dst.DynamicRange != assets.RangeDolbyVision {
			dst.DolbyVisionProfile, dst.DolbyVisionCompatibility = 0, 0
			dst.DolbyVisionRPU, dst.DolbyVisionBaseLayer = nil, nil
			dst.DolbyVisionEnhancementType, dst.DolbyVisionCMVersion = "", ""
		}
		if dst.DynamicRange == assets.RangeSDR {
			dst.MasterDisplay, dst.MaxCLL = "", ""
		}
		// The base picture survives where the dynamic metadata does not, so
		// say which one the device actually shows (VIDEO.md VS-2b).
		if containsString(p.Trace.Notes, NoteHDR10PlusLost) {
			dst.HDR10Plus = nil
		}
		if src.HDR10Plus != nil && *src.HDR10Plus && (dst.HDR10Plus == nil || !*dst.HDR10Plus) {
			out.Losses = append(out.Losses, PlaybackLoss{Feature: "hdr10plus", Replacement: PlaybackReplacement{DynamicRange: dst.DynamicRange}})
		}
		if src.DynamicRange == assets.RangeDolbyVision && dst.DynamicRange != "" && dst.DynamicRange != assets.RangeDolbyVision {
			out.Losses = append(out.Losses, PlaybackLoss{Feature: "dolbyVision", Replacement: PlaybackReplacement{DynamicRange: dst.DynamicRange}})
		}
		if src.DynamicRange != "" && src.DynamicRange != assets.RangeSDR && dst.DynamicRange == assets.RangeSDR {
			out.Losses = append(out.Losses, PlaybackLoss{Feature: "hdr", Replacement: PlaybackReplacement{DynamicRange: assets.RangeSDR}})
		}
		if out.Routes.Picture != "" {
			out.Delivered.Video = &dst
		}
	}
	if a := p.Trace.Audio; a != nil {
		src := PlaybackSoundFacts{Codec: a.Codec, Profile: a.Profile, Variant: a.Variant, ObjectAudio: a.ObjectAudio, Channels: a.Channels, Layout: a.Layout, SampleRate: a.SampleRate, BitDepth: a.BitDepth, Lossless: copiedBool(a.Lossless)}
		out.Source.Audio = &src
		dst := src
		dst.Lossless = copiedBool(src.Lossless)
		if p.OutputAudioCodec != "" {
			dst.Codec = p.OutputAudioCodec
			if audioFamily(dst.Codec) != audioFamily(src.Codec) {
				dst.Lossless = nil
			}
		}
		out.Routes.Sound = playbackRoute(p.AudioAction, repackaged)
		dst.Delivery = "decoded"
		if p.AudioPassthrough {
			dst.Delivery = "passthrough"
		}
		if p.AudioAction == "convert" {
			dst.Lossless = nil
			if p.OutputAudioCodec != "" {
				dst.Lossless = knownAudioEncodingLossless(p.OutputAudioCodec, "", "")
			}
			dst.Profile, dst.Variant, dst.ObjectAudio = "", "", ""
			dst.Channels, dst.SampleRate, dst.BitDepth, dst.Layout = p.AudioChannels, p.AudioSampleRate, 0, ""
			if target := p.ConversionTarget; target != nil {
				dst.BitDepth, dst.Layout = target.AudioBitDepth, target.AudioChannelLayout
			}
			if src.ObjectAudio == "atmos" || src.ObjectAudio == "dtsx" {
				out.Losses = append(out.Losses, PlaybackLoss{Feature: src.ObjectAudio, Replacement: PlaybackReplacement{AudioCodec: dst.Codec, Channels: dst.Channels, Layout: dst.Layout}})
			}
		}
		if out.Routes.Sound != "" {
			// Objects the engine decodes without are not on the delivered
			// track, whatever the codec still carries.
			if containsString(p.Trace.Notes, NoteObjectAudioDecoded) && dst.ObjectAudio != "" {
				feature := dst.ObjectAudio
				dst.ObjectAudio = ""
				out.Losses = append(out.Losses, PlaybackLoss{Feature: feature, Replacement: PlaybackReplacement{AudioCodec: dst.Codec, Channels: dst.Channels, Layout: dst.Layout}})
			}
			out.Delivered.Audio = &dst
		}
	}
	if subtitle != nil {
		selected := *subtitle
		selected.Rendering = ""
		if selected.Format != "" {
			out.Source.Subtitles = &selected
		}
		shown := *subtitle
		switch shown.Rendering {
		case "burnedIn":
			shown.Format = ""
			out.Routes.Subtitles = "converted"
			if out.Delivered.Video != nil {
				out.Routes.Picture = "converted"
			}
		case "drawnByDevice", "externalText", "off":
			out.Routes.Subtitles = "original"
		default:
			shown.Rendering = ""
		}
		out.Delivered.Subtitles = &shown
	}
	if out.Routes.Picture == "" && out.Routes.Sound == "" && out.Routes.Subtitles == "" {
		return nil
	}
	for _, route := range []string{out.Routes.Picture, out.Routes.Sound, out.Routes.Subtitles} {
		out.Route = strongerPlaybackRoute(out.Route, route)
	}
	switch out.Route {
	case "repackaged":
		out.Reason.Kind = "container"
		out.Reason.Container = p.OutputContainer
	case "converted":
		out.Reason.Kind = "format"
	}
	if p.VideoAction == "convert" {
		out.Reason.VideoCodec = p.SourceVideoCodec
	}
	if p.AudioAction == "convert" {
		out.Reason.AudioCodec = p.SourceAudioCodec
	}
	if containsString(p.ReasonCodes, ReasonHeightExceedsPolicy) || containsString(p.ReasonCodes, ReasonBitrateExceedsPolicy) || containsString(p.ReasonCodes, ReasonQualityRungSelected) {
		out.Reason.Kind = "quality"
		out.Reason.MaxHeight = p.TargetHeight
		out.Reason.MaxBitrateKbps = p.VideoBitrateBPS / 1000
	}
	if out.Routes.Subtitles == "converted" {
		out.Reason.Kind = "subtitles"
	}
	if p.VideoAction == "convert" || p.AudioAction == "convert" {
		// A rate for only one converted stream is not an estimate of the
		// whole delivery. In particular FLAC and quality-mode video have no
		// fixed rate; leave the total unknown until the renderer measures it.
		if (out.Delivered.Video == nil || p.VideoBitrateBPS > 0) && (out.Delivered.Audio == nil || p.AudioBitrateBPS > 0) {
			out.EstimatedBitrateKbps = (p.VideoBitrateBPS + p.AudioBitrateBPS) / 1000
		}
	} else if p.Trace.BitRate > 0 {
		out.EstimatedBitrateKbps = int(p.Trace.BitRate / 1000)
	}
	return out
}
