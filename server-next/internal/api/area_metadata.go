package api

import (
	"context"

	"github.com/PorticoMediaServer/portico/server-next/internal/metadata"
)

// metadataArea is what metadata (area 04) keeps on the server. Add this area's fields here and its
// start-up to startMetadata, in this file, so that building the area never edits server.go.
type metadataArea struct {
	// metadata looks titles up at their library's provider. See metadata.go.
	metadata *metadata.Service
}

// startMetadata builds this area's service and registers its routes. New calls it once, after
// the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startMetadata(ctx context.Context) error {
	s.metadata = &metadata.Service{DB: s.db, Providers: map[string]metadata.Provider{}}
	s.metadataRoutes()
	return nil
}

// UseProvider gives the server an online metadata source. Without one, titles are looked up
// nowhere and keep what their files say.
func (s *Server) UseProvider(p metadata.Provider) { s.metadata.Providers[p.Name()] = p }
