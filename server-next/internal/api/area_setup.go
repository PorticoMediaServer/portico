package api

import (
	"context"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/certclient"
)

// setupArea is what setup and remote access (area 2) keeps on the server. Add this area's fields here and its start-up
// to startSetup, in this file, so that building the area never edits server.go.
type setupArea struct {
	// portico gets a server linked to a Portico Account its Portico address and certificate,
	// in the background. It contacts nothing until the link's client says the server is
	// linked (portico.Linked, with the link's issue time), so a server with local sign-in
	// never does.
	portico *certclient.Client
}

// startSetup builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startSetup(ctx context.Context) error {
	s.portico = certclient.New(certclient.Options{ServerID: s.id, Key: s.identity.key, Keys: filepath.Join(s.dataDir, "Keys"),
		Log: s.log, Now: func() time.Time { return s.now() }})
	s.goBackground(func() { s.portico.Run(ctx) })
	return nil
}
