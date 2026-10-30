package playback

import "sort"

// ClientVideoLimit is one operating point of a video decoder: the largest
// picture it decodes at a frame rate, optionally only for some bit depths.
// Decoders trade size for rate ("4K at 30, but 1080p at 60") and decode 10-bit
// pictures within a different ceiling than 8-bit ones (10-bit H.264 is often
// software only), which one ceiling per codec cannot say.
type ClientVideoLimit struct {
	// BitDepths the point applies to. Empty: every depth the codec lists.
	BitDepths    []int   `json:"bitDepths,omitempty"`
	MaxWidth     int     `json:"maxWidth"`
	MaxHeight    int     `json:"maxHeight"`
	MaxFrameRate float64 `json:"maxFrameRate"`
	// MaxLevel, when stated, narrows the codec's own level at this point.
	MaxLevel int `json:"maxLevel,omitempty"`
}

// maxVideoLimits bounds one codec's operating points.
const maxVideoLimits = 16

func (l ClientVideoLimit) appliesTo(depth int) bool {
	return len(l.BitDepths) == 0 || depth <= 0 || containsInt(l.BitDepths, depth)
}

func (l ClientVideoLimit) fits(width, height int, rate float64) bool {
	return (l.MaxWidth == 0 || width <= l.MaxWidth) && (l.MaxHeight == 0 || height <= l.MaxHeight) && (l.MaxFrameRate == 0 || rate <= l.MaxFrameRate+0.01)
}

func (l ClientVideoLimit) sizeFits(width, height int) bool {
	return (l.MaxWidth == 0 || width <= l.MaxWidth) && (l.MaxHeight == 0 || height <= l.MaxHeight)
}

// decodesDepth reports whether the codec decodes pictures of this depth at all:
// it lists the depth (or lists none), and when it states operating points one
// of them applies to the depth.
func (c *ClientVideoCodec) decodesDepth(depth int) bool {
	if len(c.BitDepths) > 0 && depth > 0 && !containsInt(c.BitDepths, depth) {
		return false
	}
	if len(c.Limits) == 0 {
		return true
	}
	for _, l := range c.Limits {
		if l.appliesTo(depth) {
			return true
		}
	}
	return false
}

// at returns the codec with its ceilings narrowed to one operating point. The
// codec's own ceilings still apply: the narrower of the two wins.
func (c ClientVideoCodec) at(l ClientVideoLimit) ClientVideoCodec {
	c.MaxWidth = narrowCeiling(c.MaxWidth, l.MaxWidth)
	c.MaxHeight = narrowCeiling(c.MaxHeight, l.MaxHeight)
	if l.MaxFrameRate > 0 && (c.MaxFrameRate == 0 || l.MaxFrameRate < c.MaxFrameRate) {
		c.MaxFrameRate = l.MaxFrameRate
	}
	c.MaxLevel = narrowCeiling(c.MaxLevel, l.MaxLevel)
	c.Limits = nil
	return c
}

// forStream is the codec as it applies to one original picture: narrowed to
// an operating point that takes the picture when one does, otherwise to the
// nearest one (a point that takes the size but not the rate, else the largest),
// so the ordinary checks name what does not fit. ok is false when no point
// applies to the picture's depth.
func (c *ClientVideoCodec) forStream(depth, width, height int, rate float64) (ClientVideoCodec, bool) {
	if len(c.Limits) == 0 {
		return *c, true
	}
	var nearest *ClientVideoLimit
	for i := range c.Limits {
		l := &c.Limits[i]
		if !l.appliesTo(depth) {
			continue
		}
		if l.fits(width, height, rate) {
			return c.at(*l), true
		}
		if nearest == nil || l.sizeFits(width, height) && !nearest.sizeFits(width, height) || l.sizeFits(width, height) == nearest.sizeFits(width, height) && l.MaxWidth*l.MaxHeight > nearest.MaxWidth*nearest.MaxHeight {
			nearest = l
		}
	}
	if nearest == nil {
		return *c, false
	}
	return c.at(*nearest), true
}

// forConversion is the codec as it applies to a picture the server is about to
// produce at this depth from a source of this size and rate. Among the points
// for the depth it prefers one that keeps the source's motion (its frame rate)
// and, among those, the largest picture; when none keeps the rate, the point
// that keeps the most pixels per second. A 4K60 source for a decoder that does
// 4K30 or 1080p60 therefore becomes 1080p60, not a juddering 4K30.
func (c *ClientVideoCodec) forConversion(depth, width, height int, rate float64) ClientVideoCodec {
	if len(c.Limits) == 0 {
		return *c
	}
	if width <= 0 || height <= 0 {
		width, height = 1920, 1080
	}
	retained := func(l ClientVideoLimit) float64 {
		scale := 1.0
		if l.MaxWidth > 0 && width > l.MaxWidth {
			scale = min(scale, float64(l.MaxWidth)/float64(width))
		}
		if l.MaxHeight > 0 && height > l.MaxHeight {
			scale = min(scale, float64(l.MaxHeight)/float64(height))
		}
		return float64(width) * float64(height) * scale * scale
	}
	keeps := func(l ClientVideoLimit) bool { return rate <= 0 || l.MaxFrameRate == 0 || rate <= l.MaxFrameRate+0.01 }
	pace := func(l ClientVideoLimit) float64 {
		r := rate
		if r <= 0 {
			r = 30
		}
		if l.MaxFrameRate > 0 {
			r = min(r, l.MaxFrameRate)
		}
		return retained(l) * r
	}
	var best *ClientVideoLimit
	for i := range c.Limits {
		l := &c.Limits[i]
		if !l.appliesTo(depth) {
			continue
		}
		if best == nil {
			best = l
			continue
		}
		switch {
		case keeps(*l) != keeps(*best):
			if keeps(*l) {
				best = l
			}
		case keeps(*l):
			if retained(*l) > retained(*best) {
				best = l
			}
		default:
			if pace(*l) > pace(*best) {
				best = l
			}
		}
	}
	if best == nil {
		return *c
	}
	return c.at(*best)
}

// narrowGeometry lowers the codec's picture ceilings and those of each of its
// operating points to an explicit width and height ceiling.
func (c *ClientVideoCodec) narrowGeometry(width, height *int) {
	limits := append([]ClientVideoLimit(nil), c.Limits...)
	for i := range limits {
		if width != nil {
			limits[i].MaxWidth = narrowCeiling(limits[i].MaxWidth, *width)
		}
		if height != nil {
			limits[i].MaxHeight = narrowCeiling(limits[i].MaxHeight, *height)
		}
	}
	c.Limits = limits
	if width != nil {
		c.MaxWidth = narrowCeiling(c.MaxWidth, *width)
	}
	if height != nil {
		c.MaxHeight = narrowCeiling(c.MaxHeight, *height)
	}
}

func validVideoLimits(limits []ClientVideoLimit) bool {
	if len(limits) > maxVideoLimits {
		return false
	}
	for _, l := range limits {
		if len(l.BitDepths) > 8 || l.MaxWidth < 0 || l.MaxWidth > 32768 || l.MaxHeight < 0 || l.MaxHeight > 32768 || l.MaxFrameRate < 0 || l.MaxFrameRate > 1000 || l.MaxLevel < 0 || l.MaxLevel > 1000 {
			return false
		}
		if l.MaxWidth == 0 && l.MaxHeight == 0 && l.MaxFrameRate == 0 && l.MaxLevel == 0 {
			// A point that bounds nothing says nothing.
			return false
		}
		for _, depth := range l.BitDepths {
			if depth < 1 || depth > 16 {
				return false
			}
		}
	}
	return true
}

// normalizeVideoLimits sorts operating points into one order so equal
// documents keep one revision.
func normalizeVideoLimits(limits []ClientVideoLimit) []ClientVideoLimit {
	if len(limits) == 0 {
		return nil
	}
	for i := range limits {
		if len(limits[i].BitDepths) == 0 {
			limits[i].BitDepths = nil
		} else {
			sort.Ints(limits[i].BitDepths)
		}
	}
	sort.SliceStable(limits, func(i, j int) bool {
		a, b := limits[i], limits[j]
		if da, db := firstDepth(a.BitDepths), firstDepth(b.BitDepths); da != db {
			return da < db
		}
		if a.MaxWidth*a.MaxHeight != b.MaxWidth*b.MaxHeight {
			return a.MaxWidth*a.MaxHeight > b.MaxWidth*b.MaxHeight
		}
		if a.MaxFrameRate != b.MaxFrameRate {
			return a.MaxFrameRate > b.MaxFrameRate
		}
		return a.MaxLevel > b.MaxLevel
	})
	return limits
}

func firstDepth(depths []int) int {
	if len(depths) == 0 {
		return 0
	}
	return depths[0]
}
