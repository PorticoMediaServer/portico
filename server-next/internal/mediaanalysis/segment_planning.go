package mediaanalysis

import (
	"fmt"
)

// segmentPlanning is captured once in each existing producer transaction.
// Its identity guards queued work and publication when the owner changes limits.
type segmentPlanning struct {
	EpisodeMinutes, FilmMinutes                   int64
	IntroWindow, CreditsWindow, FilmCreditsWindow string
}

func defaultSegmentPlanning() segmentPlanning {
	return segmentPlanning{2, 10, "usual", "usual", "usual"}
}
func (p segmentPlanning) digest() string {
	return token("segment-planning-1", fmt.Sprint(p.EpisodeMinutes), fmt.Sprint(p.FilmMinutes), p.IntroWindow, p.CreditsWindow, p.FilmCreditsWindow)
}
