package decoder

// PreparedProducible reports whether the conversion code can build this recipe
// today. It asks the same builder a real encode uses, so the answer cannot
// drift from what an encode would do: a recipe the builder learns to make is
// offered from then on, and one it refuses is never offered.
func PreparedProducible(r PreparedRecipe) bool {
	_, err := preparedArgs("http://127.0.0.1/input", r, false)
	if err != nil {
		return false
	}
	if r.MusicQuality != "" {
		o, err := PreparedMusicFormat(r)
		return err == nil && AvailableConversionOutputs(CurrentToolchain()).SupportsConversion("", o.Depth, o.Codec, o.Channels)
	}
	video, depth, channels := "h264", 8, 2
	if r.AudioOnly {
		video = ""
	}
	audio := "aac"
	if r.VideoCodec == "hevc" {
		video, depth, channels = "hevc", 10, 6
		// A KeepSeventhOne recipe asks for its real codec when the source's
		// channels are known and valid; unknown or nonpositive facts need the
		// 7.1 carrier before the recipe can be promised at all.
		unknown := len(r.Source.AudioChannels) == 0
		for _, c := range r.Source.AudioChannels {
			if c <= 0 {
				unknown = true
			}
		}
		if r.KeepSeventhOne && (unknown || PreparedAudioCodec(true, r.Source.AudioChannels) == "flac") {
			audio, channels = "flac", 8
		}
	}
	return AvailableConversionOutputs(CurrentToolchain()).SupportsConversion(video, depth, audio, channels)
}

// PreparedToneMapAvailable reports whether an H.264 recipe can tone-map an HDR
// source on the processor. Without it such a source is refused rather than
// written as a flat, grey standard picture.
func PreparedToneMapAvailable() bool {
	f := CurrentToolchain()
	return f != nil && f.Filters["zscale"] && f.Filters["tonemap"]
}

// PreparedDolbyVisionBaseAvailable reports whether the units a Dolby Vision
// source carries beside its base layer can be removed from a prepared HEVC
// stream.
func PreparedDolbyVisionBaseAvailable() bool {
	f := CurrentToolchain()
	return f != nil && f.BitstreamFilters["filter_units"]
}
