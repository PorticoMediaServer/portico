package sonic

// SmoothTrack is one real queue occurrence with current-model numeric facts.
// Duplicate songs keep different occurrences; credited artists come from the
// catalogue rather than an album-name heuristic.
type SmoothTrack struct {
	Occurrence, ID int64
	Vector         Embedding
	Artists        []int64
}

const SmoothWindowMax = 256
const smoothLookahead = 64

func smoothArtistOverlap(a, b []int64) bool {
	for _, x := range a {
		for _, y := range b {
			if x == y {
				return true
			}
		}
	}
	return false
}
func validSmoothTrack(track SmoothTrack) bool {
	if track.ID <= 0 || !track.Vector.valid() {
		return false
	}
	for _, artist := range track.Artists {
		if artist <= 0 {
			return false
		}
	}
	return true
}

// SmoothWindow shapes an already seeded shuffle with bounded lookahead. It
// preserves every occurrence exactly once and the first occurrence when no
// previous track is supplied. The caller retains its seed and source order;
// this helper neither invents a new random order nor changes playback facts.
func SmoothWindow(input []SmoothTrack, previous *SmoothTrack) ([]int64, error) {
	if len(input) > SmoothWindowMax || previous != nil && !validSmoothTrack(*previous) {
		return nil, ErrEmbedding
	}
	seen := map[int64]bool{}
	pending := make([]int, len(input))
	for n, track := range input {
		if track.Occurrence < 0 || seen[track.Occurrence] || !validSmoothTrack(track) {
			return nil, ErrEmbedding
		}
		seen[track.Occurrence] = true
		pending[n] = n
	}
	out := []int64{}
	var last SmoothTrack
	if previous != nil {
		last = *previous
	} else if len(pending) > 0 {
		last = input[0]
		out = append(out, last.Occurrence)
		pending = pending[1:]
	}
	for len(pending) > 0 {
		count := min(smoothLookahead, len(pending))
		otherSong := false
		for _, n := range pending[:count] {
			if input[n].ID != last.ID {
				otherSong = true
				break
			}
		}
		otherArtist := false
		for _, n := range pending[:count] {
			candidate := input[n]
			if (!otherSong || candidate.ID != last.ID) && len(last.Artists) > 0 && len(candidate.Artists) > 0 && !smoothArtistOverlap(last.Artists, candidate.Artists) {
				otherArtist = true
				break
			}
		}
		chosen := -1
		score := -2.0
		for at, n := range pending[:count] {
			candidate := input[n]
			if otherSong && candidate.ID == last.ID || otherArtist && (len(candidate.Artists) == 0 || smoothArtistOverlap(last.Artists, candidate.Artists)) {
				continue
			}
			next := clusterDot(last.Vector, candidate.Vector)
			if chosen < 0 || next > score+1e-12 {
				chosen = at
				score = next
			}
		}
		if chosen < 0 {
			return nil, ErrEmbedding
		}
		last = input[pending[chosen]]
		out = append(out, last.Occurrence)
		pending = append(pending[:chosen], pending[chosen+1:]...)
	}
	return out, nil
}
