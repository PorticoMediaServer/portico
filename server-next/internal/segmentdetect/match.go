package segmentdetect

import "sort"

// Segment is one stretch of audio two fingerprints share, in each file's time.
type Segment struct {
	AStartUS, AEndUS int64
	BStartUS, BEndUS int64
	// Distance is the mean number of differing bits per point (of 32).
	Distance float64
}

// LengthUS is the segment's length in the first file.
func (s Segment) LengthUS() int64 { return s.AEndUS - s.AStartUS }

const (
	// matchBits is the mean point distance at or under which two windows are
	// the same audio. Measured on real programme audio (AAC re-encode, -6 dB,
	// worst-case half-point misalignment) the same audio averages 4-7 bits and
	// unrelated audio 13-14, so the threshold sits well clear of both.
	matchBits   = 10.0
	matchWindow = 16 // two seconds of points per decision
	bridgeGap   = 12 // a second and a half of disagreement inside a run
	maxOffsets  = 12
	minVotes    = 6
	popularKey  = 48 // a key this common is a steady sound, not evidence
)

// Match finds the stretches of at least minUS that a and b share. Each
// returned segment is aligned at one offset (with a point of slack either
// way); segments never overlap in a's time.
func Match(a, b Fingerprint, minUS int64) []Segment {
	if len(a.Points) < matchWindow || len(b.Points) < matchWindow {
		return nil
	}
	offsets := candidateOffsets(a, b)
	var found []Segment
	for _, offset := range offsets {
		found = append(found, alignedRuns(a, b, offset, minUS)...)
	}
	// The best segment wins any stretch of a; overlapping weaker ones go.
	sort.SliceStable(found, func(i, j int) bool {
		if found[i].LengthUS() != found[j].LengthUS() {
			return found[i].LengthUS() > found[j].LengthUS()
		}
		return found[i].Distance < found[j].Distance
	})
	var out []Segment
	for _, s := range found {
		clear := true
		for _, kept := range out {
			if s.AStartUS < kept.AEndUS && kept.AStartUS < s.AEndUS {
				clear = false
				break
			}
		}
		if clear {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AStartUS < out[j].AStartUS })
	return out
}

// informative says a point can vote: it is not quiet and not the same as the
// point before it (a held tone or a frozen frame of noise says nothing about
// where two files line up).
func informative(f Fingerprint, i int) bool {
	return f.Energy[i] > QuietEnergy && (i == 0 || f.Points[i] != f.Points[i-1])
}

// candidateOffsets votes for alignments: every half-point (16 bits) a shares
// exactly with b votes for the offset between them. Shared audio puts many
// votes on one offset; unrelated audio scatters a few everywhere.
func candidateOffsets(a, b Fingerprint) []int {
	index := map[uint32][]int32{}
	for j := range b.Points {
		if !informative(b, j) {
			continue
		}
		for _, k := range halves(b.Points[j]) {
			index[k] = append(index[k], int32(j))
		}
	}
	base := len(a.Points) // offset o = j - i is stored at o+base
	votes := make([]int32, len(a.Points)+len(b.Points))
	for i := range a.Points {
		if !informative(a, i) {
			continue
		}
		for _, k := range halves(a.Points[i]) {
			hits := index[k]
			if len(hits) > popularKey {
				continue
			}
			for _, j := range hits {
				votes[int(j)-i+base]++
			}
		}
	}
	type peak struct{ offset, votes int }
	var peaks []peak
	for o := 1; o < len(votes)-1; o++ {
		v := int(votes[o-1] + votes[o] + votes[o+1])
		if v >= minVotes && v >= int(votes[o-1]+votes[max(0, o-2)]+votes[o]) && v >= int(votes[o]+votes[o+1]+votes[min(len(votes)-1, o+2)]) {
			peaks = append(peaks, peak{o - base, v})
		}
	}
	sort.Slice(peaks, func(i, j int) bool { return peaks[i].votes > peaks[j].votes })
	var out []int
	for _, p := range peaks {
		near := false
		for _, o := range out {
			near = near || abs(o-p.offset) <= 2
		}
		if !near {
			out = append(out, p.offset)
		}
		if len(out) == maxOffsets {
			break
		}
	}
	return out
}

func halves(v uint32) [2]uint32 { return [2]uint32{v & 0xffff, 1<<16 | v>>16} }

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// alignedRuns walks a against b at one offset (b index = a index + offset) and
// returns the runs where they agree.
func alignedRuns(a, b Fingerprint, offset int, minUS int64) []Segment {
	first := max(0, -offset)
	last := min(len(a.Points), len(b.Points)-offset) // exclusive
	if last-first < matchWindow {
		return nil
	}
	n := last - first
	dist := make([]float64, n)
	quiet := make([]bool, n)
	for k := range n {
		i := first + k
		j := i + offset
		d := Distance(a.Points[i], b.Points[j])
		if j > 0 {
			d = min(d, Distance(a.Points[i], b.Points[j-1]))
		}
		if j+1 < len(b.Points) {
			d = min(d, Distance(a.Points[i], b.Points[j+1]))
		}
		dist[k] = float64(d)
		quiet[k] = a.Energy[i] <= QuietEnergy && b.Energy[j] <= QuietEnergy
	}
	// A point agrees when the two-second window around it, counting only the
	// points where something is audible, is under the threshold. A window that
	// is nearly all silence neither agrees nor disagrees.
	const (
		agree = iota
		neutral
		differ
	)
	state := make([]int, n)
	sum, heard := 0.0, 0
	add := func(k int, sign int) {
		if !quiet[k] {
			sum += float64(sign) * dist[k]
			heard += sign
		}
	}
	half := matchWindow / 2
	for k := 0; k < min(n, half); k++ {
		add(k, 1)
	}
	for k := range n {
		if k+half < n {
			add(k+half, 1)
		}
		if k-half-1 >= 0 {
			add(k-half-1, -1)
		}
		switch {
		case heard < matchWindow/4:
			state[k] = neutral
		case sum/float64(heard) <= matchBits:
			state[k] = agree
		default:
			state[k] = differ
		}
	}
	var out []Segment
	for k := 0; k < n; {
		if state[k] != agree {
			k++
			continue
		}
		start, end, gap := k, k, 0
		for k < n {
			switch state[k] {
			case agree:
				end, gap = k, 0
			case neutral:
			default:
				gap++
			}
			if gap > bridgeGap {
				break
			}
			k++
		}
		start, end = trimRun(dist, quiet, start, end)
		if end <= start {
			continue
		}
		seg := Segment{AStartUS: a.At(first + start), AEndUS: a.At(first + end + 1), BStartUS: b.At(first + start + offset), BEndUS: b.At(first + end + 1 + offset)}
		if seg.LengthUS() < minUS {
			continue
		}
		total, count := 0.0, 0
		for i := start; i <= end; i++ {
			if !quiet[i] {
				total += dist[i]
				count++
			}
		}
		seg.Distance = total / float64(max(1, count))
		out = append(out, seg)
	}
	return out
}

// trimRun moves a run's edges to where the points themselves change from
// disagreeing to agreeing: the smoothing window lets a decision lead or trail
// the real boundary by up to half its width. Each edge goes to the point that
// best separates the two sides, so one lucky window cannot pull it early.
func trimRun(dist []float64, quiet []bool, start, end int) (int, int) {
	const side = 8
	mean := func(from, to int) float64 {
		sum, heard := 0.0, 0
		for i := max(0, from); i < min(len(dist), to); i++ {
			if !quiet[i] {
				sum += dist[i]
				heard++
			}
		}
		if heard == 0 {
			return 16 // nothing heard: as unlike as unrelated audio
		}
		return sum / float64(heard)
	}
	best, score := start, -1e9
	for s := max(0, start-matchWindow/2); s <= min(len(dist)-1, start+matchWindow/2); s++ {
		if v := mean(s-side, s) - mean(s, s+side); v > score {
			best, score = s, v
		}
	}
	start = best
	best, score = end, -1e9
	for e := max(start, end-matchWindow/2); e <= min(len(dist)-1, end+matchWindow/2); e++ {
		// e is the last agreeing point: agreement up to it, disagreement after.
		if v := mean(e+1, e+1+side) - mean(e+1-side, e+1); v > score {
			best, score = e, v
		}
	}
	return start, best
}
