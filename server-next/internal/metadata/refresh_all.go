package metadata

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
	"github.com/PorticoMediaServer/portico/server-next/internal/scan"
)

// RefreshAll asks the provider again about every matched title of a library not refreshed
// since a time, whatever its age: Refresh metadata on a library. Pass when it was asked for,
// so running it again after a restart carries on where it stopped. Each title is asked about
// outside any transaction and stored in its own small one at Background priority. progress
// is told how many titles are done. A title the provider fails on is passed over (see
// failures); a provider that is down stops the run, and the titles not reached are as they were.
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
	f := &failures{s: s}
	var after *db.RefreshItem
	done := int64(0)
	for {
		page, err := s.DB.ToRefresh(ctx, libraryID, since.UnixMilli()-1, after, 200)
		if err != nil {
			return err
		}
		if len(page) == 0 {
			return f.end(ctx)
		}
		for i := range page {
			after = &page[i]
			if err := ctx.Err(); err != nil {
				return err
			}
			if page[i].ExternalIDs, err = s.DB.ExternalIDs(ctx, page[i].ID); err != nil {
				return err
			}
			// A title the provider fails on is passed over, as in RefreshLibrary.
			if _, stop := f.took(ctx, page[i].ID, s.refresh(ctx, p, locale, page[i], db.Background)); stop != nil {
				return stop
			}
			done++
			progress(done)
		}
	}
}

// RefreshJobTitles refreshes a selection as its current main profile. A title-specific
// provider refusal is recorded and passed over; an outage stops the job without inventing
// failures for titles not reached. Successful root writes clear earlier failures atomically.
func (s *Service) RefreshJobTitles(ctx context.Context, v db.Viewer, jobID int64, itemIDs []string, since time.Time,
	progress func(done int64)) (libraries map[int64]bool, err error) {
	libraries = map[int64]bool{}
	for n, id := range itemIDs {
		if err := ctx.Err(); err != nil {
			return libraries, err
		}
		if err := s.DB.SeesItem(ctx, v, id); err != nil {
			if !errors.Is(err, db.ErrNotFound) {
				return libraries, err
			}
			if err := s.DB.RecordMetadataJobOutcome(ctx, jobID, id, ""); err != nil {
				return libraries, err
			}
			progress(int64(n + 1))
			continue
		}
		item, libraryID, err := s.DB.RefreshTarget(ctx, id)
		if errors.Is(err, db.ErrNotFound) {
			progress(int64(n + 1))
			continue
		}
		if err != nil {
			return libraries, err
		}
		failed, err := s.DB.JobItemFailed(ctx, jobID, item.ID)
		if err != nil {
			return libraries, err
		}
		if item.RefreshedAt < since.UnixMilli() || failed {
			src, err := s.DB.LibrarySource(ctx, libraryID)
			if err != nil {
				return libraries, err
			}
			libraries[libraryID] = true
			p := s.provider(src)
			switch {
			case localReading(item.Kind, src):
				err = s.refreshLocal(ctx, id, item.ID, db.Background, jobID)
			case p == nil || providerKinds[item.Kind] == "":
				if failed {
					err = s.DB.RecordMetadataJobOutcome(ctx, jobID, id, "")
				}
			case ownID(p, item.ExternalIDs) != "":
				err = s.refreshJob(ctx, p, Locale{Language: src.Language, Region: src.Region}, item, db.Background, jobID)
			default:
				_, err = s.rematchJob(ctx, id, jobID)
			}
			if ctx.Err() != nil {
				return libraries, ctx.Err()
			}
			switch {
			case err == nil:
			case errors.Is(err, scan.ErrMetadataUnreadable):
				if err := s.DB.RecordMetadataJobOutcome(ctx, jobID, id, "metadata_file_unavailable"); err != nil {
					return libraries, err
				}
			case errors.Is(err, db.ErrNotFound), errors.Is(err, ErrMatchLocked), errors.Is(err, ErrNoProvider):
				if err := s.DB.RecordMetadataJobOutcome(ctx, jobID, id, ""); err != nil {
					return libraries, err
				}
			case errors.As(err, new(providerError)):
				if providerOutage(err) {
					return libraries, fmt.Errorf("%w: %w", ErrProviderDown, err)
				}
				if err := s.DB.RecordMetadataJobOutcome(ctx, jobID, id, "provider_rejected"); err != nil {
					return libraries, err
				}
			default:
				return libraries, err
			}
		}
		progress(int64(n + 1))
	}
	return libraries, nil
}
