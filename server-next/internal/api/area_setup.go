package api

import (
	"context"
	"path/filepath"
	"time"

	"github.com/PorticoMediaServer/portico/server-next/internal/certclient"
	"github.com/PorticoMediaServer/portico/server-next/internal/hosted"
)

// setupArea is what setup and remote access (area 2) keeps on the server. Add this area's fields here and its start-up
// to startSetup, in this file, so that building the area never edits server.go.
type setupArea struct {
	// portico gets a server linked to a Portico Account its Portico address and certificate,
	// in the background. It contacts nothing until the link's client says the server is
	// linked (portico.Linked, with the link's issue time), so a server with local sign-in
	// never does.
	portico *certclient.Client
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
	s.portico = certclient.New(certclient.Options{ServerID: s.id, Key: s.identity.key, Keys: filepath.Join(s.dataDir, "Keys"),
		Log: s.log, Now: func() time.Time { return s.now() }})
	go s.portico.Run(ctx)
	if err := s.startPortico(ctx); err != nil {
		return err
	}
	// Whatever must hear that the server is linked, or no longer is, is set on s.link here
	// (onLinked, onUnlinked, published), above the line that starts its background work.
	// The certificate client asks for the server's address and certificate only between the
	// two: told of a link it begins, and told the link has ended it stops for good.
	s.link.onLinked = s.portico.Linked
	s.link.onUnlinked = s.portico.Unlinked
	s.link.run()
	return nil
}
