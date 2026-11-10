package metadata

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// How long a matched title's details stand before its provider is asked again.
const (
	refreshFilm = 30 * 24 * time.Hour
	refreshShow = 14 * 24 * time.Hour
	// A show still running and a title just added change often: new episodes, a rating, a
	// corrected description.
	refreshSoon = 2 * 24 * time.Hour
	justAdded   = 14 * 24 * time.Hour
)

// due reports whether a title's details are old enough to ask about again.
func due(item db.RefreshItem, now time.Time) bool {
	age := now.Sub(time.UnixMilli(item.RefreshedAt))
	switch {
	case item.Continuing || now.Sub(time.UnixMilli(item.AddedAt)) < justAdded:
		return age >= refreshSoon
	case item.Kind == db.KindShow:
		return age >= refreshShow
	}
	return age >= refreshFilm
}

// ownID is the title's id at the provider, or "" when it was never matched to it.
func ownID(p Provider, ids []db.ExternalID) string {
	for _, id := range ids {
		if id.Provider == p.Name() {
			return id.ID
		}
	}
	return ""
}

// refresh asks the provider about one matched title again and stores the answer. What a
// person locked and what the title's files gave stay as they are, and a field the provider
// no longer gives keeps its value: the database write decides that, not this code. A provider
// that cannot be asked changes nothing, and the title stays due.
func (s *Service) refresh(ctx context.Context, p Provider, locale Locale, item db.RefreshItem, priority db.Priority) error {
	ref := Ref{Provider: p.Name(), Kind: providerKinds[item.Kind], ID: ownID(p, item.ExternalIDs)}
	if ref.ID == "" || ref.Kind == "" {
		return s.DB.MarkRefreshed(ctx, priority, item.ID)
	}
	record, err := p.Details(ctx, ref, locale)
	if errors.Is(err, ErrNotFound) {
		// The provider dropped the title: what it has stays, and it is not asked about daily.
		return s.DB.MarkRefreshed(ctx, priority, item.ID)
	}
	if err != nil {
		return err
	}
	stored := details(record, db.MatchNone)
	stored.Continuing = &record.Continuing
	if err := s.DB.ApplyDetails(ctx, priority, item.ID, stored); err != nil {
		return err
	}
	return s.children(ctx, p, locale, item.ID, ref)
}

// Refresh asks the provider about one title now, because a person chose Refresh metadata.
// It reports false for a title that has no match to refresh.
func (s *Service) Refresh(ctx context.Context, itemID string) (bool, error) {
	item, libraryID, err := s.DB.RefreshTarget(ctx, itemID)
	if err != nil {
		return false, err
	}
	src, err := s.DB.LibrarySource(ctx, libraryID)
	if err != nil {
		return false, err
	}
	p := s.provider(src)
	if p == nil || ownID(p, item.ExternalIDs) == "" || providerKinds[item.Kind] == "" {
		return false, nil
	}
	return true, s.refresh(ctx, p, Locale{Language: src.Language, Region: src.Region}, item, db.User)
}

// RefreshLibrary refreshes the library's matched titles whose details are due: every 30 days
// for a film and 14 for a show, and every 2 for a show still running or a title added in the
// last 14 days. It refreshes at most limit titles, oldest first, and says how many it did. It
// is background work: each title is asked about outside any transaction and stored in its own
// small one at Background priority.
//
// An error means the provider could not be asked; the titles not reached stay due.
func (s *Service) RefreshLibrary(ctx context.Context, libraryID int64, now time.Time, limit int) (int, error) {
	src, err := s.DB.LibrarySource(ctx, libraryID)
	if err != nil {
		return 0, err
	}
	p := s.provider(src)
	if p == nil {
		return 0, nil
	}
	locale := Locale{Language: src.Language, Region: src.Region}
	// Nothing refreshed more recently than the shortest wait can be due. A title this run has
	// refreshed moves to the end of the read's order and can come round again: it is passed over.
	before := now.Add(-refreshSoon).UnixMilli()
	done, refreshed := 0, map[int64]bool{}
	var after *db.RefreshItem
	for done < limit {
		page, err := s.DB.ToRefresh(ctx, libraryID, before, after, 200)
		if err != nil || len(page) == 0 {
			return done, err
		}
		for i := range page {
			after = &page[i]
			if done >= limit || refreshed[page[i].ID] || !due(page[i], now) {
				continue
			}
			if err := s.refresh(ctx, p, locale, page[i], db.Background); err != nil {
				return done, err
			}
			refreshed[page[i].ID] = true
			done++
		}
	}
	return done, nil
}
