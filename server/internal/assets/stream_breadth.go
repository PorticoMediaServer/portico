package assets

import (
	"context"
	"database/sql"
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

type BreadthReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func ReadStreamBreadth(ctx context.Context, q BreadthReader, asset string) (map[int]StreamBreadth, error) {
	rows, e := q.QueryContext(ctx, `SELECT stream_index,audio_description,audio_variant,chroma_subsampling,fps_numerator,fps_denominator,frame_rate_mode,interlace_analysis,telecine,dovi_rpu,dovi_base_layer,dovi_metadata_known,dovi_enhancement_type,dovi_cm_version FROM asset_stream_breadth WHERE asset_id=? ORDER BY stream_index`, asset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := map[int]StreamBreadth{}
	for rows.Next() {
		var i int
		var b StreamBreadth
		if e = rows.Scan(&i, &b.AudioDescription, &b.AudioVariant, &b.ChromaSubsampling, &b.FrameRateNumerator, &b.FrameRateDenominator, &b.FrameRateMode, &b.InterlaceAnalysis, &b.Telecine, &b.DolbyVisionRPU, &b.DolbyVisionBaseLayer, &b.DolbyVisionMetadataKnown, &b.DolbyVisionEnhancementType, &b.DolbyVisionCMVersion); e != nil {
			return nil, e
		}
		out[i] = b
	}
	return out, rows.Err()
}

func persistStreamBreadth(tx *sql.Tx, asset string, s Stream) error {
	b := s.Detail.StreamBreadth
	_, e := tx.Exec(`INSERT INTO asset_stream_breadth(asset_id,stream_index,audio_description,audio_variant,chroma_subsampling,fps_numerator,fps_denominator,frame_rate_mode,interlace_analysis,telecine,dovi_rpu,dovi_base_layer,dovi_metadata_known,dovi_enhancement_type,dovi_cm_version) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, asset, s.Index, b.AudioDescription, b.AudioVariant, b.ChromaSubsampling, b.FrameRateNumerator, b.FrameRateDenominator, b.FrameRateMode, b.InterlaceAnalysis, b.Telecine, b.DolbyVisionRPU, b.DolbyVisionBaseLayer, b.DolbyVisionMetadataKnown, b.DolbyVisionEnhancementType, b.DolbyVisionCMVersion)
	return e
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
