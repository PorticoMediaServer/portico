package playback

import (
	"strings"

	"github.com/PorticoMediaServer/portico/server/internal/assets"
)

// knownAudioEncodingLossless describes coding, not whether a conversion kept
// every source sample or what a renderer outputs. Unknown codecs and WavPack
// need an independent measured fact; depth and bitrate never supply one.
func knownAudioEncodingLossless(codec, profile, variant string) *bool {
	codec = strings.ToLower(strings.TrimSpace(codec))
	var value bool
	switch {
	case codec == "wavpack" || codec == "":
		return nil
	case codec == "pcm" || strings.HasPrefix(codec, "pcm_") || strings.HasPrefix(codec, "dsd_"):
		value = true
	case codec == "flac", codec == "alac", codec == "ape", codec == "tta", codec == "mlp", codec == "truehd", codec == "wmalossless":
		value = true
	case codec == "dts" || codec == "dca":
		// Object audio alone does not establish lossless channel-bed coding.
		observed := assets.AudioVariant(codec, profile, "")
		if observed == "" {
			observed = variant
		}
		switch observed {
		case "dts_hd_ma":
			value = true
		case "dts_core", "dts_hd_hra", "dts_express":
		default:
			return nil
		}
	case codec == "aac", codec == "ac3", codec == "eac3", codec == "ac4", codec == "mp1", codec == "mp2", codec == "mp3", codec == "opus", codec == "vorbis", codec == "wmav1", codec == "wmav2", codec == "wmapro", codec == "mpc7", codec == "mpc8", codec == "speex":
	default:
		return nil
	}
	return &value
}
