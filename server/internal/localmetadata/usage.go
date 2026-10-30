package localmetadata

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
)

// MeasuredArtworkUsage returns the same byte ledger used to enforce the
// local-artwork limit, measuring the directory first when ingestion has not
// yet done so. Imported provider caches are separate and do not count here.
// ArtworkUsage is the variant that never waits and may answer "not measured".
func (s *Service) MeasuredArtworkUsage(ctx context.Context) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.accountArtworkLocked(ctx); err != nil {
		return 0, err
	}
	return s.artworkBytes, nil
}

// accountArtworkLocked measures the local artwork directory once per process,
// a page of entries at a time and cancellably. The caller holds s.mu.
func (s *Service) accountArtworkLocked(ctx context.Context) error {
	if s.artworkAccounted {
		return nil
	}
	dir, err := os.Open(s.root)
	if err != nil {
		return err
	}
	defer dir.Close()
	var total int64
	for {
		if err = ctx.Err(); err != nil {
			return err
		}
		entries, e := dir.ReadDir(128)
		for _, entry := range entries {
			info, e := entry.Info()
			if errors.Is(e, fs.ErrNotExist) {
				// Removed since the directory was read: it holds no bytes.
				continue
			}
			if e != nil {
				return e
			}
			total += info.Size()
		}
		if errors.Is(e, io.EOF) {
			break
		}
		if e != nil {
			return e
		}
	}
	s.artworkBytes, s.artworkAccounted = total, true
	return nil
}
