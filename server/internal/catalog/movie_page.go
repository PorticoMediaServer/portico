package catalog

import (
	"context"
	"errors"

	"github.com/PorticoMediaServer/portico/server/internal/dbwork"
)

// moviePage retains the caller's SQL order while using bounded page queries.
// Item-detail hierarchy remains separate.
func (s *Service) moviePage(profile string, ids []string) ([]Item, error) {
	return s.mediaPage(profile, ids, true)
}
func (s *Service) mediaPage(profile string, ids []string, moviesOnly bool) ([]Item, error) {
	out := []Item{}
	if len(ids) == 0 {
		return out, nil
	}
	if len(ids) > mediaPageBound {
		return nil, errors.New("movie page exceeds bound")
	}
	if dbwork.Snapshot(s.Context()) == nil {
		var page []Item
		err := dbwork.WithReadSnapshot(s.Context(), s.db, func(ctx context.Context) error {
			var readErr error
			page, readErr = s.WithContext(ctx).mediaPage(profile, ids, moviesOnly)
			return readErr
		})
		return page, err
	}
	items, e := s.compactPageItems(profile, ids, moviesOnly)
	if e != nil {
		return nil, e
	}
	// Per-kind detail for the whole page in three statements rather than one to
	// six per item. See media_page_enrichment.go.
	if !moviesOnly {
		if e = s.enrichPage(items); e != nil {
			return nil, e
		}
	}
	targets := make([]artworkTarget, 0, len(ids))
	for _, id := range ids {
		targets = append(targets, artworkTarget{"item", id})
	}
	art, err := s.resolveArtwork(targets)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		if i := items[id]; i != nil {
			projectArtwork(i)
			if a := art[artworkTarget{"item", id}]; a.PosterURL != "" {
				i.PosterURL = a.PosterURL
			}
			if a := art[artworkTarget{"item", id}]; a.BackdropURL != "" {
				i.BackdropURL = a.BackdropURL
			}
			i.StillURL = art[artworkTarget{"item", id}].StillURL
			out = append(out, *i)
		} else {
			return nil, ErrStaleContinuation
		}
	}
	return out, nil
}

// mediaPageIndex loads a bounded set of items and returns
// them by id, tolerating ids that are not there.
//
// It exists for the loops that walked a candidate list calling Get per entry —
// two statements each, which on a detail page with thirteen related titles is
// twenty-six statements to render a strip of posters. The caller keeps its own
// ordering and its own filtering; this only replaces where the rows come from.
// An id the batch did not return is left out of the map, and the caller decides
// what that means, so the error behaviour of the loop it replaces is preserved
// exactly rather than approximately.
func (s *Service) mediaPageIndex(profile string, ids []string) (map[string]Item, error) {
	out := map[string]Item{}
	unique := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
		if len(unique) == mediaPageBound {
			break
		}
	}
	if len(unique) == 0 {
		return out, nil
	}
	items, err := s.mediaPage(profile, unique, false)
	if err != nil {
		// A missing id is what ErrStaleContinuation means here, and it is exactly
		// the case this index is meant to tolerate: fall back to loading what is
		// present one at a time, which is what the caller did before.
		if !errors.Is(err, ErrStaleContinuation) {
			return nil, err
		}
		return out, nil
	}
	for _, item := range items {
		out[item.ID] = item
	}
	return out, nil
}

// mediaPageBound is mediaPage's own ceiling on a page.
const mediaPageBound = 100
