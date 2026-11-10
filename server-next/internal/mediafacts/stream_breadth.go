package mediafacts

import (
	"strings"
)

// StreamBreadth contains observed facts introduced after the original stream
// detail schema. They live in scalar relational columns, not detail_json.
// Empty values mean unobserved. A configuration record is not proof of an RPU.
type StreamBreadth struct {
	AudioDescription           bool   `json:"audioDescription,omitempty"`
	AudioVariant               string `json:"audioVariant,omitempty"`
	ChromaSubsampling          string `json:"chromaSubsampling,omitempty"`
	FrameRateNumerator         int    `json:"frameRateNumerator,omitempty"`
	FrameRateDenominator       int    `json:"frameRateDenominator,omitempty"`
	FrameRateMode              string `json:"frameRateMode,omitempty"`
	InterlaceAnalysis          string `json:"interlaceAnalysis,omitempty"`
	Telecine                   bool   `json:"telecine,omitempty"`
	DolbyVisionRPU             bool   `json:"dolbyVisionRpu,omitempty"`
	DolbyVisionBaseLayer       bool   `json:"dolbyVisionBaseLayer,omitempty"`
	DolbyVisionMetadataKnown   bool   `json:"dolbyVisionMetadataKnown,omitempty"`
	DolbyVisionEnhancementType string `json:"dolbyVisionEnhancementType,omitempty"`
	DolbyVisionCMVersion       string `json:"dolbyVisionCmVersion,omitempty"`
	// PlaybackFramesRevision proves the recipe applied to this exact stream set;
	// it does not assert an observation for any individual optional fact.
	PlaybackFramesRevision string `json:"playbackFramesRevision,omitempty"`
	PacketPeakRevision     string `json:"packetPeakRevision,omitempty"`
}

// AudioVariant names the bitstream, not the decoder family. The distinction is
// required even when DTS-HD and DTS core use the same FFmpeg codec name.
func AudioVariant(codec, profile, objects string) string {
	p := strings.ToLower(strings.TrimSpace(profile))
	switch {
	case codec == "dts" || codec == "dca":
		switch {
		case objects == "dtsx":
			return "dtsx"
		case p == "dts-hd ma" || p == "dts hd ma" || p == "dts-hd master audio" || p == "dts hd master audio":
			return "dts_hd_ma"
		case p == "dts-hd hra" || p == "dts hd hra" || p == "dts-hd high resolution audio" || p == "dts hd high resolution audio":
			return "dts_hd_hra"
		case p == "dts express" || p == "dts-express":
			return "dts_express"
		case "dts" == p || p == "dts-es" || p == "dts es" || p == "dts-es matrix" || p == "dts es matrix" || p == "dts 96/24":
			return "dts_core"
		default:
			return ""
		}
	case codec == "truehd" || codec == "mlp":
		if objects == "atmos" {
			return "truehd_atmos"
		}
		return "truehd"
	case codec == "eac3":
		if objects == "atmos" || strings.Contains(p, "joc") {
			return "eac3_joc"
		}
		return "eac3"
	case codec == "ac3":
		return "ac3"
	case codec == "ac4":
		return "ac4"
	case strings.HasPrefix(codec, "pcm_") || codec == "pcm":
		return "lpcm"
	}
	return ""
}

// These analysis values carry the actual scan assertion in the normalized
// scalar row. Older decoded_frames means a window was read, not that the whole
// stream was proved progressive. Its false legacy Interlaced value is unknown.
const (
	InterlaceFieldOrderProgressive = "field_order_progressive"
	InterlaceFieldOrderInterlaced  = "field_order_interlaced"
	InterlaceDecodedInterlaced     = "decoded_frames_interlaced"
)

func (b StreamBreadth) ObservedInterlaced() *bool {
	var v bool
	switch b.InterlaceAnalysis {
	case InterlaceFieldOrderProgressive:
		v = false
	case InterlaceFieldOrderInterlaced, InterlaceDecodedInterlaced:
		v = true
	default:
		return nil
	}
	return &v
}
