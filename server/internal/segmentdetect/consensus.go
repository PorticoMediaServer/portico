package segmentdetect

import "sort"

// Detection is one segment found in one file, with the evidence for it.
type Detection struct {
	Kind             string // "intro", "credits"
	StartUS, EndUS   int64
	Confidence       float64
	Provenance       string
	Support          int // other episodes whose audio agrees
	Compared         int // other episodes compared
	FollowedByScene  bool
	MeanDistanceBits float64
}

// Provenance names. A provenance starting with SeasonProvenance is a
// measurement confirmed by other episodes; segmentmarkers lets only that family
// skip without asking.
const (
	SeasonProvenance  = "season_audio_match"
	IntroProvenance   = SeasonProvenance + ":intro:v1"
	CreditsProvenance = SeasonProvenance + ":credits:v1"
)

// Bounds on what an intro or closing segment can be. Intros shorter than 15 s
// are logos and stings; one longer than 200 s, or ending past the middle of
// the episode, is two copies of the same episode, not an intro.
const (
	MinIntroUS   int64 = 15_000_000
	MaxIntroUS   int64 = 200_000_000
	MinCreditsUS int64 = 15_000_000
	MaxCreditsUS int64 = 600_000_000
	// A tail shorter than this after the credits, or silent, is part of the
	// credits (a logo, a fade); a longer audible tail is a scene of its own.
	MinSceneUS     int64 = 8_000_000
	minMatchUS     int64 = 6_000_000
	introSnapUS    int64 = 5_000_000
	silenceTrailUS int64 = 3_000_000
)

// PairMatches is what one other episode shares with this file, found by Match
// with this file's fingerprint as a, and the extent of that episode's region.
type PairMatches struct {
	Segments         []Segment
	BStartUS, BEndUS int64
}

// MatchRegion compares a region of this file with the same region of each
// neighbour.
func MatchRegion(self Fingerprint, neighbours []Fingerprint) []PairMatches {
	out := make([]PairMatches, len(neighbours))
	for i, n := range neighbours {
		out[i] = PairMatches{Segments: Match(self, n, minMatchUS), BStartUS: n.StartUS, BEndUS: n.EndUS()}
	}
	return out
}

// neighbourMask is where one neighbour shares this region: seen where a
// matched stretch covers the point, guessed beyond a stretch cut off by the
// edge of the neighbour's own region (which says nothing about what follows).
type neighbourMask struct{ seen, guessed []bool }

func masks(self Fingerprint, pairs []PairMatches) []neighbourMask {
	out := make([]neighbourMask, len(pairs))
	for k, p := range pairs {
		m := neighbourMask{make([]bool, len(self.Points)), make([]bool, len(self.Points))}
		for _, s := range p.Segments {
			from, to := self.Index(s.AStartUS), self.Index(s.AEndUS)
			for i := from; i < to; i++ {
				m.seen[i] = true
			}
			if p.BStartUS > 0 && s.BStartUS <= p.BStartUS+StepUS {
				for i := 0; i < from; i++ {
					m.guessed[i] = true
				}
			}
			if s.BEndUS >= p.BEndUS-StepUS {
				for i := to; i < len(self.Points); i++ {
					m.guessed[i] = true
				}
			}
		}
		out[k] = m
	}
	return out
}

// coverage counts, for every point of the region, how many neighbours share
// it, counting a cut-off stretch as going on, but only alongside a neighbour
// that really shares the point, so a run never consists of guesses alone.
func coverage(self Fingerprint, pairs []PairMatches) []int {
	c := make([]int, len(self.Points))
	all := masks(self, pairs)
	for i := range c {
		real := 0
		for _, m := range all {
			if m.seen[i] {
				c[i]++
				real++
			} else if m.guessed[i] {
				c[i]++
			}
		}
		if real == 0 {
			c[i] = 0
		}
	}
	return c
}

type run struct{ start, end int } // points, end exclusive

// sharedRuns are the stretches at least need neighbours share, with gaps of up
// to a second bridged.
func sharedRuns(c []int, need int) []run {
	var out []run
	for i := 0; i < len(c); {
		if c[i] < need {
			i++
			continue
		}
		start, end := i, i+1
		gap := 0
		for i < len(c) {
			if c[i] >= need {
				end, gap = i+1, 0
			} else if gap++; gap > 8 {
				break
			}
			i++
		}
		out = append(out, run{start, end})
	}
	return out
}

// support counts the neighbours that share at least 60% of a run (a stretch
// cut off by their region's edge counts as shared beyond it), and their mean
// distance where they really share it.
func support(self Fingerprint, pairs []PairMatches, r run) (int, float64) {
	n, total, weight := 0, 0.0, 0.0
	for k, m := range masks(self, pairs) {
		covered, real := 0, 0
		for i := r.start; i < r.end; i++ {
			if m.seen[i] {
				covered++
				real++
			} else if m.guessed[i] {
				covered++
			}
		}
		if covered*10 < (r.end-r.start)*6 || real == 0 {
			continue
		}
		n++
		for _, s := range pairs[k].Segments {
			lo := max(r.start, self.Index(s.AStartUS))
			hi := min(r.end, self.Index(s.AEndUS))
			if hi > lo {
				total += s.Distance * float64(hi-lo)
				weight += float64(hi - lo)
			}
		}
	}
	if weight == 0 {
		return n, 0
	}
	return n, total / weight
}

func required(neighbours int) int {
	if neighbours >= 2 {
		return 2
	}
	return 1
}

// confidence grows with the number of other episodes that independently
// share the segment. One neighbour is a pair: it could be a recap of the same
// scene or a reused cue, so it is a suggestion. Two or more is a measurement.
func confidence(support int) float64 {
	switch {
	case support >= 3:
		return 0.95
	case support == 2:
		return 0.9
	case support == 1:
		return 0.7
	}
	return 0
}

// Intro finds the intro in the opening region of one episode from what its
// neighbours share with it. durationUS is the whole file's length.
func Intro(self Fingerprint, pairs []PairMatches, durationUS int64) (Detection, bool) {
	if len(pairs) == 0 || len(self.Points) == 0 {
		return Detection{}, false
	}
	c := coverage(self, pairs)
	best, bestSupport, bestDist := run{}, 0, 0.0
	for _, r := range sharedRuns(c, required(len(pairs))) {
		length := int64(r.end-r.start) * StepUS
		if length < MinIntroUS || length > MaxIntroUS || self.At(r.end) > durationUS/2 {
			continue
		}
		n, dist := support(self, pairs, r)
		if n < required(len(pairs)) {
			continue
		}
		if n > bestSupport || n == bestSupport && r.end-r.start > best.end-best.start {
			best, bestSupport, bestDist = r, n, dist
		}
	}
	if bestSupport == 0 {
		return Detection{}, false
	}
	start, end := self.At(best.start), self.At(best.end)
	if start <= introSnapUS {
		start = 0
	}
	end = self.At(trailSilence(self, best.end, silenceTrailUS))
	return Detection{Kind: "intro", StartUS: start, EndUS: min(end, durationUS), Confidence: confidence(bestSupport), Provenance: IntroProvenance, Support: bestSupport, Compared: len(pairs), MeanDistanceBits: bestDist}, true
}

// Credits finds the closing credits in the closing region of one episode: the
// longest stretch its neighbours share, joined to shared stretches that follow
// it closely. Audible unshared audio after it is a scene (a stinger, a preview
// of the next episode) and is left outside the marker so Skip Credits lands on
// it; a short or silent tail is part of the credits.
func Credits(self Fingerprint, pairs []PairMatches, durationUS int64) (Detection, bool) {
	if len(pairs) == 0 || len(self.Points) == 0 {
		return Detection{}, false
	}
	need := required(len(pairs))
	runs := sharedRuns(coverage(self, pairs), need)
	type candidate struct {
		r       run
		support int
		dist    float64
	}
	var supported []candidate
	for _, r := range runs {
		if n, dist := support(self, pairs, r); n >= need {
			supported = append(supported, candidate{r, n, dist})
		}
	}
	sort.Slice(supported, func(i, j int) bool { return supported[i].r.start < supported[j].r.start })
	bestIndex := -1
	for i, c := range supported {
		length := int64(c.r.end-c.r.start) * StepUS
		if length < MinCreditsUS || self.At(c.r.start) < durationUS/2 {
			continue
		}
		if bestIndex < 0 || c.r.end-c.r.start > supported[bestIndex].r.end-supported[bestIndex].r.start {
			bestIndex = i
		}
	}
	if bestIndex < 0 {
		return Detection{}, false
	}
	best := supported[bestIndex]
	end := best.r.end
	for _, c := range supported[bestIndex+1:] {
		if int64(c.r.start-end)*StepUS > silenceTrailUS {
			break
		}
		end = c.r.end
	}
	if int64(end-best.r.start)*StepUS > MaxCreditsUS {
		return Detection{}, false
	}
	d := Detection{Kind: "credits", StartUS: self.At(best.r.start), Confidence: confidence(best.support), Provenance: CreditsProvenance, Support: best.support, Compared: len(pairs), MeanDistanceBits: best.dist}
	tailStart := trailSilence(self, end, silenceTrailUS)
	if audibleUS(self, tailStart, len(self.Points)) >= MinSceneUS && self.At(tailStart) < durationUS-MinSceneUS {
		d.EndUS = self.At(tailStart)
		d.FollowedByScene = true
	} else {
		d.EndUS = durationUS
	}
	return d, true
}

// trailSilence extends point index i over quiet points that follow it, by at
// most limit.
func trailSilence(f Fingerprint, i int, limit int64) int {
	stop := min(len(f.Points), i+int(limit/StepUS))
	for i < stop && f.Energy[i] <= QuietEnergy {
		i++
	}
	return i
}

// audibleUS is how long the audio between two points is above silence.
func audibleUS(f Fingerprint, from, to int) int64 {
	n := 0
	for i := from; i < min(to, len(f.Points)); i++ {
		if f.Energy[i] > QuietEnergy {
			n++
		}
	}
	return int64(n) * StepUS
}
