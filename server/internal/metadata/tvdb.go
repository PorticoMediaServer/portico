package metadata

import (
	"context"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/metadataprovider"
)

type TVDBProvider interface {
	SearchSeries(context.Context, string, int) ([]metadataprovider.SeriesCandidate, error)
	Episodes(context.Context, int64, metadataprovider.EpisodeOrder, int) (metadataprovider.EpisodePage, error)
}

var errTVDBStale = errors.New("TVDB publication authority changed")

// One current pipeline: provider evidence is acquired separately from a fresh,
// bounded claim over concrete local targets. No alternate publication writer.
func (s *Service) TVDBStep(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := s.claimTVDBProjection(ctx)
	if err != nil {
		return err
	}
	if p != nil {
		if err = s.publishTVDBProjection(ctx, *p); err != nil {
			return s.failTVDBPublication(ctx, *p, err)
		}
		return nil
	}
	p, err = s.claimTVDBAcquisition(ctx)
	if err != nil {
		return err
	}
	if p != nil {
		return s.runTVDBAcquisition(ctx, *p)
	}
	// No show work: backfill per-episode credits for matched episodes, a
	// bounded page per step, so guest stars arrive without delaying shows.
	return s.tvdbEpisodeCreditsStep(ctx)
}
