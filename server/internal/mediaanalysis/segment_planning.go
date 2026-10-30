package mediaanalysis

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// segmentPlanning is captured once in each existing producer transaction.
// Its identity fences queued work and publication when the owner changes limits.
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
func segmentPlanningTx(ctx context.Context, q queryer) (segmentPlanning, error) {
	p := defaultSegmentPlanning()
	var episode, film sql.NullInt64
	var intro, credits, filmCredits sql.NullString
	err := q.QueryRowContext(ctx, `SELECT "segments.shortestEpisode","segments.shortestFilm","segments.introWindow","segments.creditsWindow","segments.filmCreditsWindow" FROM server_settings WHERE singleton=1`).Scan(&episode, &film, &intro, &credits, &filmCredits)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return p, err
	}
	if episode.Valid {
		p.EpisodeMinutes = episode.Int64
	}
	if film.Valid {
		p.FilmMinutes = film.Int64
	}
	if p.EpisodeMinutes < 1 || p.EpisodeMinutes > 30 || p.FilmMinutes < 5 || p.FilmMinutes > 60 || p.FilmMinutes%5 != 0 {
		return p, ErrInput
	}
	if intro.Valid {
		p.IntroWindow = intro.String
	}
	if credits.Valid {
		p.CreditsWindow = credits.String
	}
	if filmCredits.Valid {
		p.FilmCreditsWindow = filmCredits.String
	}
	for _, window := range []string{p.IntroWindow, p.CreditsWindow, p.FilmCreditsWindow} {
		if window != "usual" && window != "long" {
			return p, ErrInput
		}
	}
	return p, nil
}
