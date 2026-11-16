package prepare

import (
	"context"
	"errors"
	"os"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// ReadCopy holds a ready copy while its media or subtitles are being fetched.
// The download closes this read with its file; Empty cache uses the same lock.
func (s *Service) ReadCopy(ctx context.Context, id int64) (func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, err := s.DB.PreparedByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if p.State != db.PreparedReady {
		return nil, ErrSource
	}
	if s.reading == nil {
		s.reading = map[int64]int{}
	}
	s.reading[id]++
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.reading[id]--
		if s.reading[id] == 0 {
			delete(s.reading, id)
		}
	}, nil
}

// Release removes a copy at once when no download's title waits for it any more: the last
// device that asked for it has said it has the file. One being fetched now stays, for Tidy.
func (s *Service) Release(ctx context.Context, id int64) error {
	s.mu.Lock()
	if s.reading[id] > 0 {
		s.mu.Unlock()
		return nil
	}
	p, err := s.DB.PreparedByID(ctx, id)
	if err != nil {
		s.mu.Unlock()
		if errors.Is(err, db.ErrNotFound) {
			return nil
		}
		return err
	}
	removed, err := s.DB.RemoveUnawaitedPrepared(ctx, id)
	s.mu.Unlock()
	if err != nil || !removed {
		return err
	}
	files, err := s.copyFiles()
	if err != nil {
		return err
	}
	for _, file := range files[p.PublicID] {
		os.Remove(file)
	}
	return nil
}
