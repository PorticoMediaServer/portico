package sonic

import (
	"math"
	"sort"
)

type JourneyTrack struct {
	ID     int64
	Vector Embedding
}

const MaxJourneyTracks = 50000
const MaxJourneyLength = 128

type journeyCandidate struct {
	id                 int64
	progress, residual float64
}

// JourneyPlanner streams an authorized current-model corpus in entity-id
// order. Its memory is bounded by path length, not the size of the library.
// Seeing both exact anchors in that corpus is required before any result.
type JourneyPlanner struct {
	from, to, lastID             int64
	a, b                         Embedding
	cosine, angle, sine          float64
	bins                         [][]journeyCandidate
	seenFrom, seenTo, degenerate bool
}

func NewJourneyPlanner(from, to int64, a, b Embedding, length int) (*JourneyPlanner, error) {
	if from <= 0 || to <= 0 || from == to || length < 2 || length > MaxJourneyLength || !a.valid() || !b.valid() {
		return nil, ErrEmbedding
	}
	p := &JourneyPlanner{from: from, to: to, a: a, b: b, bins: make([][]journeyCandidate, length-2)}
	p.cosine = clusterDot(a, b)
	p.angle = math.Acos(p.cosine)
	p.sine = math.Sin(p.angle)
	p.degenerate = length == 2 || p.angle < 1e-6 || math.Abs(p.sine) < 1e-6
	return p, nil
}
func (p *JourneyPlanner) Add(track JourneyTrack) error {
	if p == nil || track.ID <= p.lastID || !track.Vector.valid() {
		return ErrEmbedding
	}
	p.lastID = track.ID
	switch track.ID {
	case p.from:
		if track.Vector != p.a {
			return ErrEmbedding
		}
		p.seenFrom = true
		return nil
	case p.to:
		if track.Vector != p.b {
			return ErrEmbedding
		}
		p.seenTo = true
		return nil
	}
	if p.degenerate {
		return nil
	}
	x := clusterDot(p.a, track.Vector)
	y := (clusterDot(p.b, track.Vector) - p.cosine*x) / p.sine
	projection := x*x + y*y
	if projection < 1e-10 {
		return nil
	}
	progress := math.Atan2(y, x) / p.angle
	if progress <= 0 || progress >= 1 {
		return nil
	}
	candidate := journeyCandidate{track.ID, progress, max(0, 1-projection)}
	bin := max(0, min(len(p.bins)-1, int(math.Round(progress*float64(len(p.bins)+1)))-1))
	values := append(p.bins[bin], candidate)
	sort.Slice(values, func(i, j int) bool {
		if values[i].residual != values[j].residual {
			return values[i].residual < values[j].residual
		}
		return values[i].id < values[j].id
	})
	p.bins[bin] = values[:min(16, len(values))]
	return nil
}
func (p *JourneyPlanner) Tracks() ([]int64, error) {
	if p == nil || !p.seenFrom || !p.seenTo {
		return nil, ErrEmbedding
	}
	if p.degenerate {
		return []int64{p.from, p.to}, nil
	}
	result := []int64{p.from}
	used := map[int64]bool{p.from: true, p.to: true}
	last := 0.0
	for bin := range p.bins {
		target := float64(bin+1) / float64(len(p.bins)+1)
		best := journeyCandidate{}
		score := math.Inf(1)
		for near := max(0, bin-1); near <= min(len(p.bins)-1, bin+1); near++ {
			for _, candidate := range p.bins[near] {
				if used[candidate.id] || candidate.progress <= last {
					continue
				}
				next := candidate.residual + 4*math.Pow(candidate.progress-target, 2)
				if next < score-1e-12 || math.Abs(next-score) <= 1e-12 && (best.id == 0 || candidate.id < best.id) {
					best = candidate
					score = next
				}
			}
		}
		if best.id > 0 {
			result = append(result, best.id)
			used[best.id] = true
			last = best.progress
		}
	}
	return append(result, p.to), nil
}

// Journey is the bounded slice adapter. Catalogue readers use JourneyPlanner
// directly so large libraries are never retained as a whole numeric graph.
func Journey(tracks []JourneyTrack, from, to int64, length int) ([]int64, error) {
	if len(tracks) > MaxJourneyTracks {
		return nil, ErrEmbedding
	}
	sorted := append([]JourneyTrack(nil), tracks...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })
	var a, b Embedding
	for _, track := range sorted {
		if track.ID == from {
			a = track.Vector
		}
		if track.ID == to {
			b = track.Vector
		}
	}
	planner, err := NewJourneyPlanner(from, to, a, b, length)
	if err != nil {
		return nil, err
	}
	for _, track := range sorted {
		if err = planner.Add(track); err != nil {
			return nil, err
		}
	}
	return planner.Tracks()
}
