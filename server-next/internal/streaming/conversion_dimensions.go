package streaming

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

// conversionBox is the most a conversion to one of a device's codecs may be: the codec's own
// limits at this bit depth, and the quality target's box (its height and the width that
// height names). A conversion makes the source fitted into it (convertedDimensions).
func conversionBox(codec *ClientVideoCodec, depth int, video *SourceVideo, maxHeight, maxWidth int) (width, height int) {
	height = maxHeight
	if codec == nil {
		return maxWidth, height
	}
	sourceHeight := 0
	if video != nil {
		point := codec.forConversion(depth, video.Width, video.Height, video.FrameRate)
		codec, sourceHeight = &point, video.Height
	}
	width = narrowCeiling(codec.MaxWidth, maxWidth)
	if codec.MaxHeight > 0 && (height == 0 || codec.MaxHeight < height) && sourceHeight > codec.MaxHeight {
		height = codec.MaxHeight
	}
	return width, height
}

// producedSize is the picture a conversion to this codec and bit depth makes of a source.
func producedSize(codec *ClientVideoCodec, depth int, video *SourceVideo, maxHeight, maxWidth int) (int, int) {
	if video == nil {
		return 0, 0
	}
	width, height := conversionBox(codec, depth, video, maxHeight, maxWidth)
	return convertedDimensions(DeliveryPlan{TargetWidth: width, TargetHeight: height, Trace: &DecisionTrace{Video: traceVideoSource(video)}})
}
