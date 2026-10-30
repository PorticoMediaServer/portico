package segmentdetect

import "math"

// FrameWidth and FrameHeight are the size of the grey pictures the visual
// detector reads (the decoder scales and pads to it). Credits type at 1080p is
// two or three of these pixels tall: smaller and it averages into the black.
const (
	FrameWidth  = 256
	FrameHeight = 144
)

// Frame classes.
const (
	FramePicture byte = iota // ordinary footage
	FrameBlack               // a black or nearly black frame
	FrameText                // crisp light type on a near-black field: a credits card or crawl
)

// FrameFeature is what one sampled picture says about credits.
type FrameFeature struct {
	Class byte
	// Scroll is how far the picture moved up since the previous sample, in
	// rows of FrameHeight (0 when it did not move or did not match).
	Scroll int8
}

// FrameReader classifies a sequence of 8-bit grey frames of FrameWidth x
// FrameHeight. It keeps only the previous frame.
type FrameReader struct {
	previous []uint8
}

// NewFrameReader starts a sequence.
func NewFrameReader() *FrameReader { return &FrameReader{} }

// Levels on the 0-255 grey scale. Credits are set on a field at or near video
// black; a dark scene's shadows are noisy greys above it.
const (
	deepBlack = 20
	lightType = 110
	edgeStep  = 50
)

// Read classifies one frame. grey must hold exactly FrameWidth*FrameHeight bytes.
func (r *FrameReader) Read(grey []byte) (FrameFeature, error) {
	n := FrameWidth * FrameHeight
	if len(grey) != n {
		return FrameFeature{}, ErrInput
	}
	deep, light, edges, sum := 0, 0, 0, 0
	for y := range FrameHeight {
		row := grey[y*FrameWidth : (y+1)*FrameWidth]
		for x, v := range row {
			sum += int(v)
			if v < deepBlack {
				deep++
			} else if v > lightType {
				light++
			}
			if x > 0 {
				if d := int(v) - int(row[x-1]); d > edgeStep || d < -edgeStep {
					edges++
				}
			}
		}
	}
	f := FrameFeature{Class: FramePicture}
	switch {
	case sum < n*16 && light*1000 < n*2 && edges*1000 < n*3:
		f.Class = FrameBlack
	// Type: most of the frame is the black field, a little of it is light,
	// and the light has sharp edges (strokes, not a lamp or a moon) in
	// separate lines.
	case deep*100 >= n*55 && light*1000 >= n*2 && light*100 <= n*30 && edges*1000 >= n*4 && edges*10 >= light*8 && textRows(grey, deep*100 >= n*90):
		f.Class = FrameText
	}
	if r.previous != nil && f.Class == FrameText {
		f.Scroll = int8(scrollUp(r.previous, grey))
	}
	if r.previous == nil {
		r.previous = make([]uint8, n)
	}
	copy(r.previous, grey)
	return f, nil
}

// textRows says the light pixels sit in separated horizontal bands, the way
// lines of type do, and leave most rows dark. A single line is enough on an
// almost entirely black frame (a name card); otherwise it takes two.
func textRows(grey []uint8, card bool) bool {
	bands, inBand, rows := 0, false, 0
	for y := range FrameHeight {
		lit := 0
		for _, v := range grey[y*FrameWidth : (y+1)*FrameWidth] {
			if v > lightType {
				lit++
			}
		}
		on := lit > 0 && lit*100 < FrameWidth*80
		if on {
			rows++
		}
		if on && !inBand {
			bands++
		}
		inBand = on
	}
	return (bands >= 2 || card && bands == 1) && rows*100 < FrameHeight*90
}

// scrollUp finds the upward shift (1..maxScroll rows) that best maps the previous
// picture onto this one, when it fits clearly better than no shift at all.
const maxScroll = 32 // a crawl that crosses the frame in under five seconds is a fast one

func scrollUp(previous, now []uint8) int {
	diff := func(shift int) float64 {
		total, n := 0, 0
		for y := 0; y+shift < FrameHeight; y++ {
			a := previous[(y+shift)*FrameWidth : (y+shift+1)*FrameWidth]
			b := now[y*FrameWidth : (y+1)*FrameWidth]
			for x := range FrameWidth {
				d := int(a[x]) - int(b[x])
				if d < 0 {
					d = -d
				}
				total += d
				n++
			}
		}
		return float64(total) / float64(max(1, n))
	}
	still := diff(0)
	best, bestShift := math.Inf(1), 0
	for s := 1; s <= maxScroll; s++ {
		if d := diff(s); d < best {
			best, bestShift = d, s
		}
	}
	if best < still*0.6 {
		return bestShift
	}
	return 0
}

// Visual provenance: a single file's picture, never confirmed by another
// episode, so it is a suggestion for the owner rather than an automatic skip.
const VisualCreditsProvenance = "visual_credits:v1"

// VisualCredits finds closing credits in the frames sampled from startUS
// every stepUS to the end of a file of durationUS. Each credits block becomes
// a detection; a stretch of ordinary picture of at least MinSceneUS between
// or after blocks is a mid- or post-credits scene and stays outside them.
func VisualCredits(frames []FrameFeature, startUS, stepUS, durationUS int64) []Detection {
	if len(frames) == 0 || stepUS <= 0 {
		return nil
	}
	// Smooth single-frame noise: a text frame needs text around it.
	class := make([]byte, len(frames))
	for i := range frames {
		text, picture := 0, 0
		for j := max(0, i-1); j <= min(len(frames)-1, i+1); j++ {
			switch frames[j].Class {
			case FrameText:
				text++
			case FramePicture:
				picture++
			}
		}
		class[i] = frames[i].Class
		if frames[i].Class == FrameText && text == 1 && picture >= 1 {
			class[i] = FramePicture
		}
	}
	at := func(i int) int64 { return min(durationUS, startUS+int64(i)*stepUS) }
	sceneFrames := int(MinSceneUS / stepUS)
	type block struct{ start, end, text, scrolling int }
	var blocks []block
	for i := 0; i < len(class); {
		if class[i] != FrameText {
			i++
			continue
		}
		// A block runs until five seconds of unbroken picture (black and a
		// single stray frame do not break the count).
		b := block{start: i}
		pictures, other := 0, 0
		for i < len(class) {
			switch class[i] {
			case FrameText:
				b.end, pictures, other = i+1, 0, 0
				b.text++
				if frames[i].Scroll > 0 {
					b.scrolling++
				}
			case FramePicture:
				pictures++
				other = 0
			default:
				if other++; other > 1 {
					pictures = 0
				}
			}
			if pictures > 4 {
				break
			}
			i++
		}
		if int64(b.end-b.start)*stepUS >= 20_000_000 {
			blocks = append(blocks, b)
		}
	}
	if len(blocks) == 0 {
		return nil
	}
	// The credits start at the first block after which the file never returns
	// to a long stretch of ordinary picture (four minutes would be more film).
	first := len(blocks) - 1
	for first > 0 {
		gap := pictureFrames(class, blocks[first-1].end, blocks[first].start)
		if int64(gap)*stepUS > 240_000_000 {
			break
		}
		first--
	}
	if at(blocks[first].start) < durationUS/2 {
		return nil
	}
	var out []Detection
	for k := first; k < len(blocks); k++ {
		b := blocks[k]
		next := len(class)
		if k+1 < len(blocks) {
			next = blocks[k+1].start
		}
		d := Detection{Kind: "credits", StartUS: at(b.start), Confidence: 0.65, Provenance: VisualCreditsProvenance}
		if b.scrolling*2 >= b.text {
			d.Confidence = 0.75
		}
		if b.start == 0 {
			// Credits already running when the frames begin may have begun
			// earlier: a suggestion with its start in doubt.
			d.Confidence = 0.55
		}
		if scene := pictureFrames(class, b.end, next); scene >= sceneFrames {
			// The marker ends where the scene begins (after any black lead-in).
			e := b.end
			for e < next && class[e] != FramePicture {
				e++
			}
			d.EndUS = at(e)
			d.FollowedByScene = true
		} else if k+1 < len(blocks) {
			// A short gap joins this block to the next.
			blocks[k+1].start = b.start
			blocks[k+1].text += b.text
			blocks[k+1].scrolling += b.scrolling
			continue
		} else {
			d.EndUS = durationUS
		}
		out = append(out, d)
	}
	return out
}

// pictureFrames is the longest unbroken stretch of ordinary picture between
// two frames (a single other frame does not break it). Cards cut with black
// are not a scene however many picture frames fall between them.
func pictureFrames(class []byte, from, to int) int {
	longest, current, other := 0, 0, 0
	for i := from; i < min(to, len(class)); i++ {
		if class[i] == FramePicture {
			current++
			other = 0
		} else if other++; other > 1 {
			current = 0
		}
		longest = max(longest, current)
	}
	return longest
}
