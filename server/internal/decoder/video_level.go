package decoder

import "math"

// H264OutputLevel covers the declared display dimensions and cadence. Unknown
// dimensions/cadence use the safe upper envelope accepted by this pipeline.
func H264OutputLevel(width, height int, rate float64) int {
	if width <= 0 || height <= 0 {
		return 52
	}
	if rate <= 0 {
		rate = 60
	}
	blocks := math.Ceil(float64(width)/16) * math.Ceil(float64(height)/16)
	for _, bound := range []struct {
		level          int
		blocks, second float64
	}{{31, 3600, 108000}, {40, 8192, 245760}, {42, 8704, 522240}, {50, 22080, 589824}, {51, 36864, 983040}, {52, 36864, 2073600}, {60, 139264, 4177920}, {61, 139264, 8355840}, {62, 139264, 16711680}} {
		if blocks <= bound.blocks && blocks*rate <= bound.second {
			return bound.level
		}
	}
	return 62
}

func HEVCOutputLevel(width, height int, rate float64) int {
	if width <= 0 || height <= 0 {
		return 153
	}
	if rate <= 0 {
		rate = 60
	}
	pixels := float64(width) * float64(height)
	for _, bound := range []struct {
		level          int
		pixels, second float64
	}{{93, 983040, 33177600}, {120, 2228224, 66846720}, {123, 2228224, 133693440}, {150, 8912896, 267386880}, {153, 8912896, 534773760}, {156, 8912896, 1069547520}, {180, 35651584, 1069547520}, {183, 35651584, 2139095040}, {186, 35651584, 4278190080}} {
		if pixels <= bound.pixels && pixels*rate <= bound.second {
			return bound.level
		}
	}
	return 186
}
