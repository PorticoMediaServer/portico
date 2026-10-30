package remotesources

import (
	"context"
	"sync"

	"github.com/PorticoMediaServer/portico/server/internal/mounts"
	"github.com/PorticoMediaServer/portico/server/internal/storage"
)

// guardSourceRemoval owns only a short admission barrier, not source or owner
// authority. The command's authorized transaction still checks the exact source
// association and generation. Never stop a current playback/reading session to
// make removal succeed; refuse it and keep the source unchanged.
func (s *Service) guardSourceRemoval(ctx context.Context, source string) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.serveMu.Lock()
	defer s.mu.Unlock()
	defer s.serveMu.Unlock()
	if s.serveClosed {
		return nil, storage.ErrRemoteOffline
	}
	if s.sourceRemovals[source] || s.sourceTests[source] != nil {
		return nil, mounts.ErrSourceInUse
	}
	for _, active := range s.active[source] {
		if active.lane == "playback" {
			return nil, mounts.ErrSourceInUse
		}
	}
	for _, lease := range s.readLeases[source] {
		if lease.binding.Purpose == SourcePlayback || lease.binding.Purpose == SourceReading {
			return nil, mounts.ErrSourceInUse
		}
	}
	if s.sourceRemovals == nil {
		s.sourceRemovals = map[string]bool{}
	}
	s.sourceRemovals[source] = true
	var once sync.Once
	return func() { once.Do(func() { s.serveMu.Lock(); delete(s.sourceRemovals, source); s.serveMu.Unlock() }) }, nil
}
