package metadata

import (
	"context"
	"errors"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// RefreshAll asks the provider again about every matched title of a library not refreshed
// since a time, whatever its age: Refresh metadata on a library. Pass when it was asked for,
// so running it again after a restart carries on where it stopped. Each title is asked about
// outside any transaction and stored in its own small one at Background priority. progress
// is told how many titles are done. An error means the provider could not be asked; the
// titles not reached are as they were.
func (s *Service) RefreshAll(ctx context.Context, libraryID int64, since time.Time, progress func(done int64)) error {
	src, err := s.DB.LibrarySource(ctx, libraryID)
	if err != nil {
		return err
	}
	p := s.provider(src)
	if p == nil {
		return nil
	}
	locale := Locale{Language: src.Language, Region: src.Region}
	var after *db.RefreshItem
	done := int64(0)
	for {
		page, err := s.DB.ToRefresh(ctx, libraryID, since.UnixMilli()-1, after, 200)
		if err != nil || len(page) == 0 {
			return err
		}
		for i := range page {
			after = &page[i]
			if err := ctx.Err(); err != nil {
				return err
			}
			if page[i].ExternalIDs, err = s.DB.ExternalIDs(ctx, page[i].ID); err != nil {
				return err
			}
			if err := s.refresh(ctx, p, locale, page[i], db.Background); err != nil {
				return err
			}
			done++
			progress(done)
		}
	}
}

// RefreshTitles is Refresh metadata on a selection: each title not refreshed since a time is
// asked about again, and one with no match is looked up again from its name and year. A title
// that is gone, or that no provider is asked about, is passed over. progress is told how many
// titles are done. An error means the provider could not be asked.
func (s *Service) RefreshTitles(ctx context.Context, itemIDs []string, since time.Time, progress func(done int64)) error {
	for n, id := range itemIDs {
		if err := ctx.Err(); err != nil {
			return err
		}
		item, libraryID, err := s.DB.RefreshTarget(ctx, id)
		switch {
		case errors.Is(err, db.ErrNotFound):
			continue
		case err != nil:
			return err
		}
		if item.RefreshedAt < since.UnixMilli() {
			src, err := s.DB.LibrarySource(ctx, libraryID)
			if err != nil {
				return err
			}
			p := s.provider(src)
			switch {
			case p == nil || providerKinds[item.Kind] == "":
			case ownID(p, item.ExternalIDs) != "":
				err = s.refresh(ctx, p, Locale{Language: src.Language, Region: src.Region}, item, db.Background)
			default:
				_, err = s.Rematch(ctx, id)
			}
			if err != nil && !errors.Is(err, ErrMatchLocked) && !errors.Is(err, ErrNoProvider) {
				return err
			}
		}
		progress(int64(n + 1))
	}
	return nil
}
