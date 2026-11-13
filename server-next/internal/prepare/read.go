package prepare

import (
	"context"

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
