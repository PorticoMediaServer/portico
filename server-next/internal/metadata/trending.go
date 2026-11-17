package metadata

import (
	"context"
	"log/slog"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

const trendingEvery = 6 * time.Hour

// RefreshTrending uses the same registered client as metadata lookups. Library choices are
// checked before any request; a failed update leaves the last complete provider order intact.
func (s *Service) RefreshTrending(ctx context.Context, now time.Time) error {
	enabled, err := s.DB.TMDBTrendingEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	s.mu.RLock()
	p, ok := s.Providers["tmdb"].(interface {
		Trending(context.Context) ([]Ref, error)
	})
	s.mu.RUnlock()
	if !ok {
		return nil
	}
	checked, err := s.DB.TMDBTrendingCheckedAt(ctx)
	if err != nil {
		return err
	}
	if !checked.IsZero() && now.Before(checked.Add(trendingEvery)) {
		return nil
	}
	refs, err := p.Trending(ctx)
	if err != nil {
		return err
	}
	titles := make([]db.TMDBTrendingTitle, 0, len(refs))
	for _, r := range refs {
		kind := db.KindMovie
		if r.Kind == "show" {
			kind = db.KindShow
		}
		titles = append(titles, db.TMDBTrendingTitle{Kind: kind, ID: r.ID})
	}
	return s.DB.SaveTMDBTrending(ctx, titles, now)
}

func (s *Service) KeepTrending(ctx context.Context, log *slog.Logger) {
	tick := time.NewTicker(5 * time.Minute)
	defer tick.Stop()
	for {
		if err := s.RefreshTrending(ctx, time.Now()); err != nil && ctx.Err() == nil {
			log.Warn("updating TMDB trending failed; keeping the last list", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
