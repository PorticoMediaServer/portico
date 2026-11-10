package mediaanalysis

import (
	"bufio"
	"io"
	"math"
	"strconv"
	"strings"
)

// readFullLoudness consumes ebur128 metadata incrementally. Its memory is
// independent of track length; only the first and last three seconds survive.
// ebur128 applies BS.1770 channel weights (including LFE exclusion) to native
// rate, double precision PCM and measures true peaks before any downmix.
func readFullLoudness(r io.Reader) (map[string]any, error) {
	scan := bufio.NewScanner(r)
	scan.Buffer(make([]byte, 4096), 65536)
	integrated, peak, lra := -70.0, 0.0, 0.0
	start, end := []float64{}, []float64{}
	frames := int64(0)
	for scan.Scan() {
		line := scan.Text()
		if !strings.HasPrefix(line, "lavfi.r128.") {
			continue
		}
		k, raw, ok := strings.Cut(strings.TrimPrefix(line, "lavfi.r128."), "=")
		if !ok {
			continue
		}
		v, e := strconv.ParseFloat(raw, 64)
		if e != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, ErrUnsupported
		}
		switch k {
		case "I":
			integrated = v
		case "LRA":
			lra = v
		case "true_peak":
			peak = max(peak, v)
		case "M":
			frames++
			v = max(-120, min(10, v))
			if len(start) < 30 {
				start = append(start, v)
			}
			end = append(end, v)
			if len(end) > 30 {
				copy(end, end[1:])
				end = end[:30]
			}
		}
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	if frames == 0 {
		return nil, ErrUnsupported
	}
	return map[string]any{
		"algorithm":            "ITU-R-BS.1770-4",
		"integratedLUFS":       integrated,
		"truePeakLinear":       peak,
		"loudnessRangeLU":      lra,
		"frames":               frames,
		"measuredOn":           "source-native-rate-multichannel-f64",
		"normalizationApplied": false,
		"startEnvelopeLUFS":    start,
		"endEnvelopeLUFS":      end,
		"envelopeIntervalMs":   100,
	}, nil
}
