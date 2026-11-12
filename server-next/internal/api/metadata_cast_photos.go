package api

import (
	"context"
	"sync"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// Cast and crew photos (definition area 4, rule 47) come from the provider, recorded by each
// lookup and refresh and fetched by the libraries' picture passes. Turned off, every photo
// fetched from a provider is dropped and none is fetched; turned on again, the passes fetch
// the recorded ones at once, so no title has to be refreshed for its people's photos.

// castPhotos is the switch as last put in force, so a save that leaves it as it was does nothing.
type castPhotos struct {
	mu sync.Mutex
	on bool
}

// watchCastPhotos puts a change of the switch in force. It runs after the save is answered.
func (s *Server) watchCastPhotos(ctx context.Context) {
	s.castPhotos.on = metadata.Document.Get(s.settings).CastPhotos
	metadata.Document.Watch(s.settings, func(next metadata.Setting) {
		s.castPhotos.mu.Lock()
		changed := next.CastPhotos != s.castPhotos.on
		s.castPhotos.on = next.CastPhotos
		s.castPhotos.mu.Unlock()
		if !changed {
			return
		}
		s.goBackground(func() {
			if !next.CastPhotos {
				if n, err := s.db.DropProviderPhotos(ctx); err != nil && ctx.Err() == nil {
					s.log.Error("cast photos turned off: the provider's photos could not all be dropped", "dropped", n, "error", err)
				}
				return
			}
			libraries, err := s.db.AllLibraries(ctx)
			if err != nil {
				if ctx.Err() == nil {
					s.log.Error("cast photos turned on: the libraries could not be read", "error", err)
				}
				return
			}
			for _, lib := range libraries {
				s.fetchPicturesLater(ctx, lib.PublicID)
			}
		})
	})
}
