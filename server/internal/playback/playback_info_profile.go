package playback

import "github.com/PorticoMediaServer/portico/server/internal/decoder"

// encodedPictureProfile is the explicit profile option in BuildConversion's
// committed recipe, not a decoder capability or an observed bitstream profile.
// Other encoders without an explicit graph profile remain unknown here.
func encodedPictureProfile(target decoder.ConversionTarget) string {
	target, err := target.Normalized()
	if err != nil || target.CopyDolbyVision {
		return ""
	}
	switch target.VideoCodec {
	case "h264":
		if target.BitDepth != 8 || target.DynamicRange != "sdr" {
			return ""
		}
		if target.VideoProfile != "" {
			return target.VideoProfile
		}
		return "high"
	case "hevc":
		switch target.BitDepth {
		case 8:
			return "main"
		case 10:
			return "main10"
		}
	}
	return ""
}

// encodedPictureChroma describes BuildConversion's explicit encoded pixel
// format/surface. It is a committed recipe, not a report from the renderer.
// Copied streams and targets outside this encoder recipe keep their own facts.
func encodedPictureChroma(target decoder.ConversionTarget) string {
	target, err := target.Normalized()
	if err != nil || target.CopyDolbyVision || target.BitDepth != 8 && target.BitDepth != 10 {
		return ""
	}
	switch target.VideoCodec {
	case "h264":
		if target.BitDepth != 8 || target.DynamicRange != "sdr" {
			return ""
		}
	case "hevc", "av1":
	default:
		return ""
	}
	return "4:2:0"
}
