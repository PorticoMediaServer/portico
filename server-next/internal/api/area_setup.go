package api

import (
	"context"

	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
)

// setupArea is what setup and remote access (area 2) keeps on the server. Add this area's fields here and its start-up
// to startSetup, in this file, so that building the area never edits server.go.
type setupArea struct {
	// hosted is what request handlers ask of Portico Services: approvals, and checking what
	// it signed. It is the link's client; tests put a client for a fake service here.
	hosted hosted.Service
	// link keeps this server's link to Portico Services up to date in the background. It
	// contacts nothing unless the server is linked to a Portico Account, so a server with
	// local sign-in never does. See portico_link.go.
	link *porticoLink
}

// startSetup builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startSetup(ctx context.Context) error {
	return s.startPortico(ctx)
}
