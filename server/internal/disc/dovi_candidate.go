package disc

// DolbyVisionLayerCandidate names the only primary HEVC and explicit DV EL
// STN entries for a title. It is a candidate for packet qualification, not a
// declared PMT relationship or proof that pictures/RPU/clocks can be joined.
// The actual demuxer must validate those facts before delivering any DV data.
type DolbyVisionLayerCandidate struct {
	BasePID        int
	EnhancementPID int
}

// DolbyVisionCandidate never selects the first video, largest picture or
// neighbouring PID. Every ordered clip must expose exactly one primary HEVC
// entry and one explicit enhancement entry, with stable PIDs. A conflicting
// PMT dependency, ambiguous/missing role or a material clip change stays closed.
func (t Title) DolbyVisionCandidate() (DolbyVisionLayerCandidate, bool) {
	if len(t.Clips) == 0 {
		return DolbyVisionLayerCandidate{}, false
	}
	for _, stream := range t.Streams {
		if stream.ClipSequence < 0 || stream.ClipSequence >= len(t.Clips) ||
			(stream.Kind == "video" && (stream.VideoRole == "enhancement") != stream.DolbyVisionEnhancementLayer) {
			return DolbyVisionLayerCandidate{}, false
		}
	}
	var candidate DolbyVisionLayerCandidate
	for clip := range t.Clips {
		if t.Clips[clip].Sequence != clip {
			return DolbyVisionLayerCandidate{}, false
		}
		var base, enhancement Stream
		primaryCount, enhancementCount := 0, 0
		seen := map[int]bool{}
		for _, stream := range t.Streams {
			if stream.ClipSequence != clip || stream.Kind != "video" {
				continue
			}
			if seen[stream.PID] || stream.VideoRole == "" {
				return DolbyVisionLayerCandidate{}, false
			}
			seen[stream.PID] = true
			switch stream.VideoRole {
			case "primary":
				primaryCount++
				base = stream
			case "enhancement":
				enhancementCount++
				enhancement = stream
			case "secondary":
				// An explicit PiP stream is retained but is not the primary.
			default:
				return DolbyVisionLayerCandidate{}, false
			}
		}
		if primaryCount != 1 || enhancementCount != 1 || base.Coding != 0x24 || enhancement.Coding != 0x24 ||
			base.PID <= 31 || base.PID >= 8191 || enhancement.PID <= 31 || enhancement.PID >= 8191 ||
			base.PID == enhancement.PID || base.DolbyVisionEnhancementLayer ||
			!enhancement.DolbyVisionEnhancementKnown || !enhancement.DolbyVisionEnhancementLayer ||
			(enhancement.DolbyVisionBasePID != nil && *enhancement.DolbyVisionBasePID != base.PID) {
			return DolbyVisionLayerCandidate{}, false
		}
		pair := DolbyVisionLayerCandidate{base.PID, enhancement.PID}
		if clip == 0 {
			candidate = pair
		} else if candidate != pair {
			return DolbyVisionLayerCandidate{}, false
		}
	}
	return candidate, true
}
