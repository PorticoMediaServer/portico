package api

import "github.com/PorticoMediaServer/portico/server-next/internal/artwork"

func (s *Server) registerArtworkRoutes() {
	s.artwork = artwork.New(s.dataDir)
	s.imageRoutes()
}
