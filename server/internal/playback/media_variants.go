package playback

import "github.com/PorticoMediaServer/portico/server/internal/assets"

var audioVariants = map[string]bool{"dts_core": true, "dts_hd_hra": true, "dts_hd_ma": true, "dtsx": true, "dts_express": true, "truehd": true, "truehd_atmos": true, "ac3": true, "eac3": true, "eac3_joc": true, "ac4": true, "lpcm": true}

func validAudioVariants(v []string) bool {
	if len(v) > 16 {
		return false
	}
	for _, x := range v {
		if !audioVariants[x] {
			return false
		}
	}
	return true
}
func validChroma(v []string) bool {
	if len(v) > 4 {
		return false
	}
	for _, x := range v {
		if x != "4:2:0" && x != "4:2:2" && x != "4:4:4" && x != "4:0:0" {
			return false
		}
	}
	return true
}
func validFrameRateModes(v []string) bool {
	if len(v) > 2 {
		return false
	}
	for _, x := range v {
		if x != "constant" && x != "variable" {
			return false
		}
	}
	return true
}
func sourceVariant(a *SourceAudio) string {
	if a == nil {
		return ""
	}
	if a.AudioVariant != "" {
		return a.AudioVariant
	}
	return assets.AudioVariant(a.Codec, a.Profile, a.ObjectAudio)
}

// A family-only DTS declaration does not promise DTS-HD. Object-bearing
// codecs may decode a channel bed; preserved objects require their own claim.
func acceptsAudioVariant(variants []string, a *SourceAudio, objects []string, passthrough bool) bool {
	v := sourceVariant(a)
	if v == "" {
		return true
	}
	if len(variants) > 0 {
		return containsString(variants, v)
	}
	switch v {
	case "dts_hd_hra", "dts_hd_ma", "dtsx", "dts_express":
		return false
	case "truehd_atmos", "eac3_joc":
		if passthrough {
			return containsString(objects, "atmos")
		}
		return true
	}
	return true
}

// declaresHDR10Plus reports whether the engine and the display it is shown on
// both state HDR10+ for the delivered codec. A list that states nothing is not
// a denial; a list that names another range is.
func declaresHDR10Plus(client ClientProfile, codec string) bool {
	if c := client.videoCodec(codec); c != nil && len(c.DynamicRanges) > 0 && !containsString(c.DynamicRanges, "hdr10plus") {
		return false
	}
	if ranges := client.Display.DynamicRanges; len(ranges) > 0 && !containsString(ranges, "hdr10plus") {
		return false
	}
	return true
}
