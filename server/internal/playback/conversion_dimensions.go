package playback

import (
	"math"
	"strconv"
	"strings"
)

func convertedDimensions(p DeliveryPlan) (int, int) {
	if p.Trace == nil || p.Trace.Video == nil {
		return p.TargetWidth, p.TargetHeight
	}
	v := p.Trace.Video
	if v.Width <= 0 || v.Height <= 0 {
		return p.TargetWidth, p.TargetHeight
	}
	w, h := float64(v.Width), float64(v.Height)
	if v.Rotation%180 != 0 {
		w, h = h, w
	}
	// Pixel aspect applies along the original picture's horizontal axis.
	parts := strings.Split(v.SampleAspectRatio, ":")
	if len(parts) == 2 {
		n, ne := strconv.ParseFloat(parts[0], 64)
		d, de := strconv.ParseFloat(parts[1], 64)
		if ne == nil && de == nil && n > 0 && d > 0 {
			if v.Rotation%180 != 0 {
				h *= n / d
			} else {
				w *= n / d
			}
		}
	}
	ratio := 1.0
	if p.TargetWidth > 0 && w > 0 {
		ratio = math.Min(ratio, float64(p.TargetWidth)/w)
	}
	if p.TargetHeight > 0 && h > 0 {
		ratio = math.Min(ratio, float64(p.TargetHeight)/h)
	}
	return max(2, int(w*ratio)/2*2), max(2, int(h*ratio)/2*2)
}
