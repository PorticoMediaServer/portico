package mediaanalysis

import (
	"math"
	"time"
)

type sonicProgressSample struct {
	tracks  int
	elapsed time.Duration
}

// Progress is measured only after a successful fenced publication. Cancelled,
// stale and paused work cannot fabricate a completion or improve throughput.
// The bounded in-memory observation is intentionally lost on restart: prior
// timestamps cannot tell how much of the interval was active work.
func (s *Service) recordSonicProgress(library string, tracks int, elapsed time.Duration) {
	if library == "" || tracks < 1 || elapsed <= 0 {
		return
	}
	s.sonicProgressMu.Lock()
	defer s.sonicProgressMu.Unlock()
	if s.sonicProgress == nil {
		s.sonicProgress = map[string][]sonicProgressSample{}
	}
	samples := append(s.sonicProgress[library], sonicProgressSample{tracks, elapsed})
	if len(samples) > 32 {
		samples = samples[len(samples)-32:]
	}
	s.sonicProgress[library] = samples
}

func (s *Service) sonicRemainingSeconds(library string, remaining int64) *int64 {
	if remaining <= 0 {
		return nil
	}
	s.sonicProgressMu.Lock()
	defer s.sonicProgressMu.Unlock()
	samples := s.sonicProgress[library]
	if len(samples) < 2 {
		return nil
	}
	var tracks int
	var elapsed time.Duration
	for _, sample := range samples {
		tracks += sample.tracks
		elapsed += sample.elapsed
	}
	seconds := math.Ceil(float64(remaining) * elapsed.Seconds() / float64(tracks))
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 1 || seconds > 365*86400 {
		return nil
	}
	estimate := int64(seconds)
	return &estimate
}
