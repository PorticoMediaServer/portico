package api

import (
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/db"
)

// EpisodeDisplay is the owner's choice for this show's episode list on every device.
type EpisodeDisplay = db.EpisodeDisplay

func (s *Server) episodeDisplayRoutes() {
	Handle(s, "PUT /v1/admin/items/{id}/episode-display", Owner, func(r *http.Request, req EpisodeDisplay) (EpisodeDisplay, error) {
		err := s.db.SetEpisodeDisplay(r.Context(), viewer(r), r.PathValue("id"), req)
		return req, refuse(err)
	})
}
