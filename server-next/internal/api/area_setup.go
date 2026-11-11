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
	// setupCode is what creating the owner needs. It is made at start while there is no
	// account, and written to the log and to setupFile, never sent to an app. See setup.go.
	setupCode, setupFile string
	// hosted is Portico Services. Only a server choosing or using Portico Accounts asks it
	// anything; tests replace it.
	hosted hosted.Service
}

// startSetup builds this area's services and registers its routes and job kinds. New calls it
// once, after the job runner exists and before the jobs left from the last run are taken up.
func (s *Server) startSetup(ctx context.Context) error {
	s.portico = certclient.New(certclient.Options{ServerID: s.id, Key: s.identity.key, Keys: filepath.Join(s.dataDir, "Keys"),
		Log: s.log, Now: func() time.Time { return s.now() }})
	go s.portico.Run(ctx)
	s.hosted = hosted.NotConnected{}
	if err := s.loadSetupCode(ctx, s.dataDir); err != nil {
		return err
	}
	if err := s.resetRemoteAccess(ctx, s.dataDir); err != nil {
		return err
	}
	s.setupRoutes()
	return nil
}
