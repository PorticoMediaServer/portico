package decoder

import "regexp"

var hlsInitName = regexp.MustCompile(`^(?:init|init-[0-9]{1,6}|audio-[0-9]{1,2}-init-[0-9]{1,6})\.mp4$`)

// HEVCSampleEntry names a copied Dolby Vision picture's required MP4 entry.
// A zero profile describes ordinary HEVC, including converted pictures.
func HEVCSampleEntry(dolbyVisionProfile int) string {
	switch dolbyVisionProfile {
	case 5, 8:
		return "dvh1"
	case 7:
		return "dvhe"
	}
	return "hvc1"
}

// HLSMuxArgs keeps live and VOD codec signalling identical. The caller supplies
// only a delivered copy profile and copied audio codec; converted Dolby Vision
// must not retain its source profile's entry. Empty codecs mean no such stream.
func HLSMuxArgs(fragmented bool, videoCodec, copiedAudioCodec string, dolbyVisionProfile int) []string {
	if !fragmented {
		return nil
	}
	var args []string
	strict := ""
	if videoCodec == "hevc" {
		entry := HEVCSampleEntry(dolbyVisionProfile)
		args = append(args, "-tag:v", entry)
		if entry == "dvh1" || entry == "dvhe" {
			strict = "unofficial"
		}
	}
	if copiedAudioCodec == "truehd" || copiedAudioCodec == "flac" {
		// Experimental also permits Dolby Vision's unofficial signalling; one
		// strictness option avoids a later, weaker value overriding this one.
		strict = "-2"
	}
	if strict != "" {
		args = append(args, "-strict", strict)
	}
	return args
}

// HLSPackagingArgs selects the container and the per-window init segment. The
// producer promotes an init only after checking it matches the presentation.
func HLSPackagingArgs(packaging, initName string) ([]string, string, error) {
	switch packaging {
	case "mpegts_hls":
		return []string{"-hls_segment_type", "mpegts"}, "ts", nil
	case "fmp4_hls":
		if !hlsInitName.MatchString(initName) {
			return nil, "", ErrInvalidConfiguration
		}
		return []string{"-hls_segment_type", "fmp4", "-hls_fmp4_init_filename", initName, "-hls_segment_options", "use_editlist=0:avoid_negative_ts=disabled", "-map_metadata", "-1", "-map_chapters", "-1", "-fflags", "+bitexact"}, "m4s", nil
	}
	return nil, "", ErrInvalidConfiguration
}
