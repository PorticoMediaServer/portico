package assets

import (
	"strconv"
	"strings"
)

// VideoProbeBitDepth uses the actual probe depth, then its pixel format. A
// missing pixel format and depth remain unknown, rather than an eight-bit fact.
func VideoProbeBitDepth(rawDepth, pixelFormat string) int {
	if depth, err := strconv.Atoi(rawDepth); err == nil && depth > 0 && depth <= 16 {
		return depth
	}
	return pixelBitDepth(pixelFormat)
}

// VideoProbeInterlace retains an explicit field-order assertion separately
// from the legacy boolean. Unknown field orders do not assert progressive.
func VideoProbeInterlace(fieldOrder string) (bool, string) {
	switch strings.ToLower(fieldOrder) {
	case "tt", "bb", "tb", "bt":
		return true, InterlaceFieldOrderInterlaced
	case "progressive":
		return false, InterlaceFieldOrderProgressive
	default:
		return false, ""
	}
}

// VideoProbeFrameRate preserves the measured rational alongside the normalized
// rate. The real-rate fallback supplies a fact only when the probe reports it;
// neither an absent nor a malformed rate establishes a frame-rate assertion.
func VideoProbeFrameRate(average, real string) (float64, int, int) {
	numerator, denominator := exactProbeRate(average)
	if numerator == 0 {
		numerator, denominator = exactProbeRate(real)
	}
	rate := probeRate(average)
	if rate == 0 {
		rate = probeRate(real)
	}
	return rate, numerator, denominator
}
