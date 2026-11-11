package metadata

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

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
		return providerError{err}
	}
	stored := details(record, db.MatchNone)
	stored.Continuing = &record.Continuing
	if err := s.DB.ApplyDetails(ctx, priority, item.ID, stored); err != nil {
		return err
	}
	if err := s.storePictures(ctx, item.ID, record.Pictures, "", db.Background); err != nil {
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

// RefreshLibrary refreshes the library's matched titles that are due at now: each is due a
// set time after it was last refreshed (see db.DueForRefresh), and a show a scan just gave a new
// episode is due at once. Finding them reads only the titles that are due, in the order they
// fell due, so it costs what is due and not the size of the library. It is safe to call as
// often as wanted, and is called after every scan and every hour.
//
// It refreshes at most limit titles and says how many it did. It is background work: each title
// is asked about outside any transaction and stored in its own small one at Background
// priority. A title the provider fails on is passed over and waits before it is due again (see
// failures); a provider that is down stops the run, and the titles not reached stay due.
func (s *Service) RefreshLibrary(ctx context.Context, libraryID int64, now time.Time, limit int) (int, error) {
	src, err := s.DB.LibrarySource(ctx, libraryID)
	if err != nil {
		return 0, err
	}
	p := s.provider(src)
	if p == nil {
		return 0, nil
	}
	// A title matched before due times were kept has none: it is due at once.
	if err := s.DB.ScheduleUnscheduled(ctx, libraryID); err != nil {
		return 0, err
	}
	locale := Locale{Language: src.Language, Region: src.Region}
	f := &failures{s: s}
	done := 0
	// A title refreshed in this run falls due later and may come round again in the read: it is
	// passed over.
	refreshed := map[int64]bool{}
	var after *db.RefreshItem
	for done < limit {
		page, err := s.DB.DueForRefresh(ctx, libraryID, now.UnixMilli(), after, min(200, limit-done+downAfter))
		if err != nil {
			return done, err
		}
		if len(page) == 0 {
			break
		}
		for i := range page {
			after = &page[i]
			if done >= limit {
				break
			}
			if refreshed[page[i].ID] {
				continue
			}
			refreshed[page[i].ID] = true
			if page[i].ExternalIDs, err = s.DB.ExternalIDs(ctx, page[i].ID); err != nil {
				return done, err
			}
			err := s.refresh(ctx, p, locale, page[i], db.Background)
			if ctx.Err() != nil {
				return done, ctx.Err()
			}
			handled, stop := f.took(ctx, page[i].ID, err)
			if stop != nil {
				return done, stop
			}
			if !handled {
				done++
			}
		}
	}
	return done, f.end(ctx)
}
