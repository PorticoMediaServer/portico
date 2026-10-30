package assets

import (
	"encoding/json"
	"math"
	"strconv"
)

// Decoded flags detect H.264 PAFF/MBAFF missed by field_order. Frame cadence
// is measured from timestamps; differing nominal and average rates alone do
// not establish VFR. Failed or short windows remain unobserved.
func analyzeFrames(raw []byte, d *StreamDetail) {
	var doc struct {
		Frames []struct {
			Timestamp  string `json:"best_effort_timestamp_time"`
			Interlaced int    `json:"interlaced_frame"`
			Repeat     int    `json:"repeat_pict"`
		} `json:"frames"`
	}
	if json.Unmarshal(raw, &doc) != nil || len(doc.Frames) < 2 {
		return
	}
	previous, firstStep := 0.0, 0.0
	steps := 0
	variable := false
	valid := true
	for i, f := range doc.Frames {
		if f.Interlaced != 0 {
			d.Interlaced = true
			d.InterlaceAnalysis = InterlaceDecodedInterlaced
		}
		timestamp, e := strconv.ParseFloat(f.Timestamp, 64)
		if e != nil || math.IsNaN(timestamp) || math.IsInf(timestamp, 0) {
			valid = false
			continue
		}
		if i > 0 {
			delta := timestamp - previous
			if delta <= 0 {
				valid = false
			} else {
				steps++
				if firstStep == 0 {
					firstStep = delta
				} else if math.Abs(delta-firstStep) > math.Max(0.001, firstStep*0.05) {
					variable = true
				}
			}
		}
		previous = timestamp
	}
	if d.InterlaceAnalysis == "" {
		d.InterlaceAnalysis = "decoded_frames"
	}
	if valid && steps >= 8 {
		if variable {
			d.FrameRateMode = "variable"
		} else {
			d.FrameRateMode = "constant"
		}
	}
	// A repeated picture is not enough to prove a complete telecine cadence.
}
