package api

import (
	"context"
	"net/http"
	"time"
)

type conversionStorage interface {
	ConversionFolders() []string
	CachedConversions(context.Context, bool) (int64, error)
}

// StorageCacheReport is disk space that can be freed now. After emptying it is
// what was actually removed; a reader or new request can reduce it since the preview.
type StorageCacheReport struct {
	Bytes int64 `json:"bytes"`
}

func (s *Server) storageCacheRoutes(cache *storageCache) {
	look := func(r *http.Request, empty bool) (StorageCacheReport, error) {
		if empty {
			defer func() { cache.mu.Lock(); cache.at = time.Time{}; cache.mu.Unlock() }()
		}
		bytes, err := s.prepare.CachedCopies(r.Context(), empty)
		if err != nil {
			return StorageCacheReport{}, err
		}
		if engine, ok := s.playback.Engine().(conversionStorage); ok {
			n, err := engine.CachedConversions(r.Context(), empty)
			bytes += n
			if err != nil {
				return StorageCacheReport{}, err
			}
		}
		return StorageCacheReport{Bytes: bytes}, nil
	}
	Handle(s, "GET /v1/admin/storage/cache", Owner, func(r *http.Request, _ None) (StorageCacheReport, error) { return look(r, false) })
	Handle(s, "POST /v1/admin/storage/cache/empty", Owner, func(r *http.Request, _ None) (StorageCacheReport, error) { return look(r, true) })
}
