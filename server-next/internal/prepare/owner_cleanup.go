package prepare

import (
	"context"
	"errors"
	"os"
)

// ErrReading leaves an in-flight transfer and its request alone.
var ErrReading = errors.New("a prepared download is being fetched; try again when the transfer has finished")

// ExpirePersonDownloads frees this person's unshared copies, not the person's files.
func (s *Service) ExpirePersonDownloads(ctx context.Context, publicID string) error {
	s.mu.Lock()
	copies, err := s.DB.PersonDownloadCopies(ctx, publicID)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	ids := make([]int64, 0, len(copies))
	names := map[int64]string{}
	for _, p := range copies {
		if s.reading[p.ID] > 0 {
			s.mu.Unlock()
			return ErrReading
		}
		ids = append(ids, p.ID)
		names[p.ID] = p.PublicID
	}
	removed, err := s.DB.ExpirePersonDownloadCopies(ctx, publicID, ids)
	if err != nil {
		s.mu.Unlock()
		return err
	}
	var stopped []<-chan struct{}
	for _, id := range removed {
		if work := s.running[id]; work != nil {
			work.stop()
			stopped = append(stopped, work.finished)
		}
	}
	s.mu.Unlock()
	for _, done := range stopped {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-done:
		}
	}
	if len(removed) == 0 {
		return nil
	}
	files, err := s.copyFiles()
	if err != nil {
		return err
	}
	for _, id := range removed {
		for _, file := range files[names[id]] {
			if err := os.Remove(file); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
