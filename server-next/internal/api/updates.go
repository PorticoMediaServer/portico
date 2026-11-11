package api

import (
	"net/http"

	"github.com/PorticoMediaServer/portico/server-next/internal/updates"
)

// Updates: this version, the newest, and how to update this kind of install. The server only
// tells; it never installs. How it looks is in the `general` settings document.

// Updates is the checker; the server runs its daily check.
func (s *Server) Updates() *updates.Checker { return s.updates }

func (s *Server) updateRoutes() {
	Handle(s, "GET /v1/admin/updates", Owner, func(r *http.Request, _ None) (updates.State, error) {
		return s.updates.State(), nil
	})
	Handle(s, "POST /v1/admin/updates/check", Owner, func(r *http.Request, _ None) (updates.State, error) {
		return s.updates.Check(r.Context()), nil
	})
}
